package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"zord-outcome-engine/config"
	"zord-outcome-engine/db"
	"zord-outcome-engine/handlers"
	"zord-outcome-engine/internal/auth"
	"zord-outcome-engine/internal/bankingest"
	"zord-outcome-engine/internal/close"
	"zord-outcome-engine/internal/health"
	"zord-outcome-engine/internal/imports"
	"zord-outcome-engine/internal/observe"
	"zord-outcome-engine/internal/persistence"
	"zord-outcome-engine/internal/poll"
	"zord-outcome-engine/internal/poll/providers/razorpay"
	"zord-outcome-engine/internal/recon"
	"zord-outcome-engine/kafka"
	"zord-outcome-engine/routes"
	"zord-outcome-engine/services"
	"zord-outcome-engine/storage"
	"zord-outcome-engine/tracing"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/pressly/goose/v3"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
)

func main() {
	cleanup := tracing.InitTracing("zord-outcome-engine")
	defer cleanup()

	gin.SetMode(gin.ReleaseMode)
	server := gin.New()
	server.Use(gin.Recovery())
	server.Use(otelgin.Middleware("zord-outcome-engine"))
	ctx := context.Background()
	config.InitDB()
	if db.DB == nil {
		log.Fatal("DB is nil after InitDB")
	}

	goose.SetBaseFS(nil)
	if err := goose.SetDialect("postgres"); err != nil {
		log.Fatal("goose dialect error:", err)
	}
	if err := goose.Up(db.DB, "db/migrations"); err != nil {
		log.Fatal("migrations failed:", err)
	}
	err := godotenv.Load()
	if err != nil {
		log.Println("No .env file found")
	}
	if err := auth.InitJWTSigningSecret(); err != nil {
		log.Fatal("JWT auth init failed:", err)
	}

	brokers := strings.Split(os.Getenv("KAFKA_BROKERS"), ",")
	producer, err := kafka.NewProducer(brokers)
	if err != nil {
		log.Fatalf("Kafka producer creation failure: %v", err)
	}
	services.SetVectorIndexPublisher(producer)
	defer producer.Close()

	dispatchTopic := os.Getenv("KAFKA_TOPIC")
	if strings.TrimSpace(dispatchTopic) == "" {
		// Default to the relay's dispatch event stream topic so that
		// dispatch_index is populated even if KAFKA_TOPIC is not set.
		dispatchTopic = "payments.dispatch.events.v1"
	}
	intentTopic := os.Getenv("KAFKA_INTENT_TOPIC")
	if strings.TrimSpace(intentTopic) == "" {
		intentTopic = "payments.intent.events.v1"
	}

	groupID := "outcome-engine-dispatch-group"
	intentGroupID := "outcome-engine-intent-group"

	// OUT-02: durable failure recording is a precondition for offset
	// advancement. A later successful message must never commit past an
	// earlier failure unless that failure has a durable receipt.
	recordConsumerFailure := services.NewConsumerFailureRecorder(db.DB)

	// Dispatch consumer — runs in its own goroutine.
	go func() {
		err := kafka.StartConsumer(ctx, brokers, groupID, dispatchTopic, handlers.HandleDispatchEvent, recordConsumerFailure)
		if err != nil {
			log.Fatalf("Dispatch Kafka consumer failed: %v", err)
		}
	}()

	// Intent consumer — runs in its own goroutine.
	go func() {
		err := kafka.StartConsumer(ctx, brokers, intentGroupID, intentTopic, handlers.HandleIntentEvent, recordConsumerFailure)
		if err != nil {
			log.Fatalf("Intent Kafka consumer failed: %v", err)
		}
	}()

	log.Printf("Kafka consumers started dispatch_topic=%s intent_topic=%s", dispatchTopic, intentTopic)

	bucket := os.Getenv("S3_BUCKET")
	region := os.Getenv("AWS_REGION")

	if bucket == "" || region == "" {
		log.Fatal("S3_BUCKET or S3_REGION not set in environment")
	}

	s3store, err := storage.NewS3Store(context.Background(), bucket, region)
	if err != nil {
		log.Fatal("Failed to init S3", err)
	}
	cfg := config.LoadConfig()
	if err := storage.InitEncryptionKey(cfg.VaultKey); err != nil {
		log.Fatal("Failed to init encryption key: ", err)
	}

	h := &handlers.Handler{
		S3store: s3store,
		Kafka:   producer,
	}
	routes.Routes(server, h)
	routes.AttachmentRoutes(server, h)

	// ── Relay outbox routes (outcome_outbox → zord-relay → Kafka) ─────────
	outboxRepo := storage.NewOutboxPullRepo(db.DB)
	outboxHandler := handlers.NewOutboxHandler(outboxRepo)
	routes.OutboxRoutes(server, outboxHandler)

	backfillStore := persistence.NewSQLStore(db.DB)
	edgeURL := os.Getenv("ZORD_EDGE_URL")
	freshness := poll.NewFreshnessService(backfillStore, poll.NewEdgeReceiptClient(edgeURL, os.Getenv("RELAY_AUTH_TOKEN")))
	// D52: per-tenant Razorpay credentials come from edge's internal endpoint
	// (RECON_CREDENTIALS_TOKEN, never the relay token), cached in memory only.
	// Misconfiguration fails closed: every tenant lookup is "unavailable",
	// and there is no platform-key fallback.
	var tenantCredSrc poll.TenantCredentialSource
	if edgeCreds, err := poll.NewEdgeCredentialClientFromEnv(); err != nil {
		log.Printf("ERROR recon: tenant credentials disabled (backfill jobs fail CREDENTIALS_UNAVAILABLE, enrichment skipped): %v", err)
		tenantCredSrc = poll.NewUnavailableCredentialSource("credential client not configured")
	} else {
		tenantCredSrc = &poll.CachedCredentialSource{Src: edgeCreds}
	}
	tenantCreds := poll.EnvCredentialResolver{Tenant: tenantCredSrc}
	backfillSvc := poll.NewBackfillService(backfillStore, freshness, tenantCreds, func(cfg razorpay.Config) (poll.BackfillProvider, error) {
		client, err := razorpay.NewClient(cfg, nil, nil, nil)
		if err != nil {
			return nil, err
		}
		return razorpay.NewBackfillAdapter(client), nil
	})
	routes.BackfillRoutes(server, &handlers.BackfillHandler{Service: backfillSvc, Freshness: freshness})

	observationProc := observe.NewProcessor(backfillStore)
	handlers.SetObservationProcessor(observationProc)
	routes.ObservationRoutes(server, &handlers.ObservationHandler{Processor: observationProc})
	routes.PaymentRoutes(server, &handlers.PaymentHandler{Store: backfillStore})
	routes.PayoutRoutes(server, &handlers.PayoutHandler{Store: backfillStore})
	reconStore := persistence.NewReconSQLStore(db.DB)
	observationProc.Refunds = reconStore
	// Live marketplace graph: Refunds sink alone does not persist transfer edges.
	observationProc.Edges = reconStore
	// D52: transfer enrichment uses each tenant's own key (no platform key).
	// A tenant without creds is skipped (refund still lands, marked skipped_*)
	// and re-enriched by the D26 pull once its creds work.
	tenantTransfers := &observe.TenantTransfers{Resolver: tenantCreds}
	observationProc.TransfersFor = tenantTransfers.For
	observationProc.CredentialInvalidator = tenantCreds
	backfillSvc.ReEnrichSkipped = observationProc.ReEnrichSkipped
	reconSvc := recon.NewService(reconStore)
	financialSvc := recon.NewFinancialService(reconStore)
	importSvc := imports.NewService(persistence.NewImportSQLStore(db.DB))
	bankIngest := bankingest.NewService(importSvc, reconStore)
	bankIngest.AfterMatch = func(ctx context.Context, tenantID, connectorID, accountID string) error {
		_, _, err := financialSvc.Run(ctx, recon.FinancialRunRequest{
			TenantID: tenantID, ConnectorID: connectorID, AccountID: accountID,
		})
		return err
	}
	handlers.SetBankIngestService(bankIngest)
	finHandler := &handlers.FinancialHandler{Service: financialSvc, Store: reconStore}
	closeStore := &close.Store{DB: db.DB}
	closeSvc := close.NewService(financialSvc, closeStore, reconStore)
	closeHandler := &handlers.CloseHandler{Service: closeSvc}
	routes.ReconRoutes(server, &handlers.ReconHandler{Service: reconSvc, Parser: services.BankStatementParser{}}, &handlers.ImportHandler{
		Service: importSvc,
		AfterBankCommit: func(ctx context.Context, tenantID, connectorID, accountID string) error {
			_, err := bankIngest.Match(ctx, tenantID, connectorID, accountID)
			return err
		},
	}, &handlers.BankIngestHandler{Service: bankIngest}, finHandler, closeHandler)

	// Start the observation and bank consumers only after every sink they
	// use is wired (observationProc.Refunds/Edges/Transfers above and
	// handlers.SetBankIngestService). Starting them earlier processed early
	// events without refund/edge sinks, and the bank handler silently
	// dropped events while bankIngestService was still nil. Guarded by
	// TestRecon_SinksWiredBeforeConsumerStart.
	observationTopic := os.Getenv("KAFKA_OBSERVATION_TOPIC")
	if strings.TrimSpace(observationTopic) == "" {
		observationTopic = "payments.ledger.events.v1"
	}
	go func() {
		err := kafka.StartConsumer(ctx, brokers, "outcome-engine-observation-group", observationTopic, handlers.HandleProviderObservation, recordConsumerFailure)
		if err != nil {
			log.Fatalf("Observation Kafka consumer failed: %v", err)
		}
	}()
	log.Printf("Kafka observation consumer topic=%s", observationTopic)

	bankTopic := os.Getenv("KAFKA_BANK_TOPIC")
	if strings.TrimSpace(bankTopic) == "" {
		bankTopic = "payments.bank.events.v1"
	}
	go func() {
		err := kafka.StartConsumer(ctx, brokers, "outcome-engine-bank-group", bankTopic, handlers.HandleBankStatementReceived, recordConsumerFailure)
		if err != nil {
			log.Fatalf("Bank statement Kafka consumer failed: %v", err)
		}
	}()
	log.Printf("Kafka bank statement consumer topic=%s", bankTopic)

	// Readiness endpoint — checks DB connectivity
	readinessHandler := health.NewReadinessHandler([]health.DependencyCheck{
		health.DBCheck("postgres", db.DB),
	})
	server.GET("/ready", readinessHandler.Ready)

	log.Println("Starting Zord Outcome Engine service on port 8081 with observability enabled")

	srv := &http.Server{
		Addr:              ":8081",
		Handler:           server,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      20 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal("Server failed to start:", err)
	}
}
