package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"zord-intent-engine/internal/auth"
	"zord-intent-engine/internal/health"
	"zord-intent-engine/internal/models"
	"zord-intent-engine/internal/services"
	"zord-intent-engine/internal/validator"
	"zord-intent-engine/internal/vault"
	"zord-intent-engine/kafka"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"zord-intent-engine/config"
	"zord-intent-engine/db"
	"zord-intent-engine/internal/handlers"

	"zord-intent-engine/internal/etl"
	"zord-intent-engine/internal/persistence"
	"zord-intent-engine/internal/worker"

	//"zord-intent-engine/internal/pii"

	"zord-intent-engine/storage"
	"zord-intent-engine/tracing"

	"github.com/pressly/goose/v3"
)

func main() {
	// -------- INIT --------
	cleanup := tracing.InitTracing("zord-intent-engine")
	defer cleanup()

	config.InitDB()
	goose.SetBaseFS(nil)
	if err := goose.SetDialect("postgres"); err != nil {
		log.Fatal("goose dialect error:", err)
	}
	if err := goose.Up(db.DB, "db/migrations"); err != nil {
		log.Fatal("migrations failed:", err)
	}

	err := vault.InitVault()
	if err != nil {
		log.Fatal("failed to initialize vault:", err)
	}

	// R-01: every public, tenant-scoped handler must run behind a verified
	// principal. Fail startup rather than silently serve with auth disabled.
	if err := auth.InitJWTSigningSecret(); err != nil {
		log.Fatal("failed to initialize JWT signing secret:", err)
	}

	// TOK-04: every outbound call to zord-token-enclave must carry a scoped
	// service JWT (not a static shared secret). Fail startup rather than
	// silently run with every tokenize call doomed to a 401.
	if err := auth.InitServiceJWTSigningSecret(); err != nil {
		log.Fatal("failed to initialize service JWT signing secret:", err)
	}

	if err := services.InitTokenizedDataHashMasterSecret(); err != nil {
		log.Fatal("failed to initialize tokenized data hash master secret:", err)
	}

	// ctx is used for one-shot startup calls and — deliberately — for each
	// Kafka message's own processing (see the handler/resultHandler closures
	// below). It is NEVER cancelled by shutdown: an in-flight message that's
	// already been read off the claim channel must be allowed to finish its
	// DB work to completion, not have it aborted mid-flight by a shutdown
	// signal. consumerCtx (INT-08), below, is the separate context that IS
	// cancelled on shutdown — it only controls whether the Kafka consumer
	// groups accept new sessions/claims, never the processing of a message
	// already in progress.
	ctx := context.Background()
	consumerCtx, cancelConsumers := context.WithCancel(context.Background())
	defer cancelConsumers()
	var consumerWG sync.WaitGroup

	// Seed built-in mapping profiles (TALLY, SAP, etc.) from global_profiles.json
	// into mapping_profiles so they resolve with a real, persisted profile_hash
	// instead of only existing as an in-memory fallback. Non-fatal: the
	// in-memory fallback still works if this fails.
	if err := services.SeedGlobalMappingProfilesFromFile(ctx, db.DB); err != nil {
		log.Printf("⚠️ Failed to seed global mapping profiles: %v", err)
	}

	// 4.2.4: a stuck intent_ingest_runs row (writer crashed mid-batch) would
	// otherwise sit in PROCESSING forever and never become leaseable by
	// Relay. Safe to run on every replica — see SweepStuckIngestRuns.
	go db.StartIngestRunSweeper(ctx, db.DB,
		durationEnv("INGEST_RUN_SWEEP_INTERVAL", 5*time.Minute),
		durationEnv("INGEST_RUN_STALE_AFTER", 30*time.Minute),
		durationEnv("INGEST_RUN_TERMINAL_AFTER", 24*time.Hour),
	)

	brokers := strings.Split(os.Getenv("KAFKA_BROKERS"), ",")
	topic := os.Getenv("KAFKA_TOPIC")
	groupID := "intent-engine-group"

	resultTopic := "pii.tokenize.result"

	// -------- Repositories --------
	dlqRepo := persistence.NewDLQRepo(db.DB)
	intentRepo := persistence.NewPaymentIntentRepo(db.DB)
	intentQueryRepo := persistence.NewIntentQueryRepo(db.DB)
	outboxPullRepo := persistence.NewOutboxPullRepo(db.DB)
	dlqPullRepo := persistence.NewDLQPullRepo(db.DB)
	batchPullRepo := persistence.NewBatchPullRepo(db.DB)
	consumerFailureRepo := persistence.NewConsumerFailureRepo(db.DB)
	tenantDailyUsageRepo := persistence.NewTenantDailyUsageRepo(db.DB)

	// -------- Validator --------
	intentValidator := validator.NewValidator(dlqRepo)

	// -------- PII Tokenizer --------
	//tokenizer, err := pii.NewTokenizer(os.Getenv("PII_TOKEN_SECRET"))
	// if err != nil {
	// 	log.Fatal("failed to init PII tokenizer:", err)
	// }

	// -------- Intent Service --------
	//------Initializing s3
	s3store, err := storage.NewS3Store(
		ctx,
		os.Getenv("CANNONICALS3_BUCKET"),
		os.Getenv("NIRS3_BUCKET"),
		os.Getenv("GOVERNANCES3_BUCKET"),
		os.Getenv("AWS_REGION"),
	)
	if err != nil {
		log.Fatal(err)
	}

	producer, err := kafka.NewProducer(brokers)
	if err != nil {
		log.Fatalf("Failed to create Kafka producer: %v", err)
	}

	tokenizeQueue := services.NewKafkaTokenizeQueue(producer)
	tenantBusinessDateRepo := persistence.NewTenantBusinessDateRepo(db.DB)
	intentService := services.NewIntentService(
		intentValidator,
		intentRepo,
		s3store,
		tokenizeQueue,
		db.DB,
		tenantDailyUsageRepo,
		tenantBusinessDateRepo,
	)
	intentService.SetVectorIndexPublisher(producer)

	// -------- DLQ HTTP (READ-ONLY) --------
	dlqHandler := handlers.NewDLQHandler(dlqRepo)
	intentHandler := handlers.NewIntentHandler(intentQueryRepo)
	outboxHandler := handlers.NewOutboxHandler(outboxPullRepo)
	dlqOutboxHandler := handlers.NewDLQOutboxHandler(dlqPullRepo)
	batchOutboxHandler := handlers.NewBatchOutboxHandler(batchPullRepo)

	runRepo := etl.NewRunRepository(db.DB)
	airflowWorker := worker.NewAirflowWorker(outboxPullRepo, runRepo)
	airflowHandler := handlers.NewAirflowHandler(airflowWorker)
	normHandler := handlers.NewNormalizationHandler(db.DB)

	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		response := map[string]interface{}{
			"service": "zord-intent-engine",
			"status":  "healthy",
			"time":    time.Now().UTC(),
		}

		if err := json.NewEncoder(w).Encode(response); err != nil {
			http.Error(w, "failed to encode health response", http.StatusInternalServerError)
		}
	})

	// Readiness endpoint — checks DB connectivity
	readinessHandler := health.NewReadinessHandler([]health.DependencyCheck{
		health.DBCheck("postgres", db.DB),
	})
	mux.HandleFunc("/ready", readinessHandler.ReadyHTTP)

	// R-01: these routes carry tenant-scoped payment data and are reachable
	// either through the public Kong gateway (/v1/*) or directly by
	// zord-console's server (/api/prod/intents/*) — both must run behind a
	// verified principal so a caller-supplied tenant_id can never diverge
	// from the tenant the caller is actually authenticated as.
	// /internal/* routes are deliberately NOT wrapped with Protect — R-02
	// covers them with RequireInternalScope (service tokens, not end-user JWTs).
	mux.HandleFunc("/v1/dlq", auth.Protect(dlqHandler.List))
	mux.HandleFunc("/v1/dlq/manual-review", auth.Protect(dlqHandler.GetManualReviewDLQ))
	mux.HandleFunc("/v1/dlq/terminal/count", auth.Protect(dlqHandler.GetTerminalDLQCount))
	mux.HandleFunc("/v1/dlq/", auth.Protect(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/dlq" || r.URL.Path == "/v1/dlq/" {
			dlqHandler.List(w, r)
		} else {
			dlqHandler.GetByID(w, r) // NEW: /v1/dlq/{dlq_id}
		}
	}))
	mux.HandleFunc("/v1/intents/", auth.Protect(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/intents" || r.URL.Path == "/v1/intents/" {
			intentHandler.List(w, r)
		} else {
			intentHandler.GetByID(w, r)
		}
	}))
	mux.HandleFunc("/v1/intents", auth.Protect(intentHandler.List))
	mux.HandleFunc("/api/prod/intents/batch-ids", auth.Protect(intentHandler.ListBatchIDs))
	mux.HandleFunc("/api/prod/intents/payment-intents", auth.Protect(intentHandler.ListPaymentIntentLiteByBatch))
	mux.HandleFunc("/api/prod/intents/dlq-items", auth.Protect(intentHandler.ListDLQItemsByBatchSimple))

	// R-02 + Slice 8: every /internal/* route requires an internal service
	// credential carrying the route's scope (see http_routes.go).
	registerInternalRoutes(mux, internalRouteHandlers{
		DLQCount:             dlqHandler.CountAll,
		OutboxLease:          outboxHandler.Lease,
		OutboxAck:            outboxHandler.Ack,
		OutboxNack:           outboxHandler.Nack,
		DLQLease:             dlqOutboxHandler.Lease,
		DLQAck:               dlqOutboxHandler.Ack,
		DLQNack:              dlqOutboxHandler.Nack,
		BatchLease:           batchOutboxHandler.Lease,
		BatchAck:             batchOutboxHandler.Ack,
		BatchNack:            batchOutboxHandler.Nack,
		AirflowTransform:     airflowHandler.Transform,
		NormalizationQuality: normHandler.Quality,
	})

	// Slice 8: admin routes run behind auth.Protect; approval needs a
	// PAYOUT_APPROVER user (see http_routes.go).
	profileHandler := handlers.NewMappingProfileHandler(db.DB)
	tenantSynonymHandler := handlers.NewTenantSynonymHandler(db.DB)
	intentApprovalHandler := handlers.NewIntentApprovalHandler(intentService)
	registerAdminRoutes(mux, adminRouteHandlers{
		MappingProfilesListOrCreate: profileHandler.ListOrCreate,
		MappingProfileItem:          profileHandler.GetUpdateOrDeactivate,
		TenantSynonymsListOrCreate:  tenantSynonymHandler.ListOrCreate,
		TenantSynonymDeactivate:     tenantSynonymHandler.Deactivate,
		IntentApprove:               intentApprovalHandler.Approve,
	})

	handler := func(msg []byte) error {
		var event models.Event
		err := json.Unmarshal(msg, &event)
		if err != nil {
			log.Printf("Invalid Kafka event payload: %v", err)
			return err
		}

		log.Printf("edge event consumed [event_id=%s event_type=%s trace_id=%s tenant_id=%s envelope_id=%s]",
			event.EventID, event.EventType, event.TraceID, event.TenantID, event.EnvelopeID)

		canonical, dlq, err := intentService.ProcessIncomingIntent(ctx, &event)
		if err != nil {
			log.Printf("System error processing intent: %v\n", err)
			return err // Return error to Kafka consumer so it doesn't MarkMessage
		}

		if dlq != nil {
			log.Printf("⚠️ Intent rejected [tenant=%s envelope=%s reason=%s]", event.TenantID, event.EnvelopeID, dlq.ReasonCode)
			if err := intentService.PersistRejectedIntentDLQ(ctx, dlq, &event); err != nil {
				log.Printf("Failed to save DLQ entry: %v", err)
				return err
			}
			return nil // Reject is a terminal state, return nil so message is marked
		}

		if canonical == nil {
			log.Printf("Tokenization queued for async processing [envelope=%s]", event.EnvelopeID)
		} else {
			log.Printf("Intent processed successfully [intent_id=%s envelope=%s]", canonical.IntentID, event.EnvelopeID)
		}

		return nil
	}

	resultHandler := func(msg []byte) error {

		var event models.TokenizeResultEvent

		err := json.Unmarshal(msg, &event)
		if err != nil {
			log.Printf("Invalid tokenize result event: %v", err)
			return err
		}

		log.Printf("Received tokenize result for envelope=%s", event.EnvelopeID)

		_, err = intentService.ProcessTokenizeResult(ctx, &event)
		if err != nil {
			log.Printf("Failed to process tokenize result: %v", err)
			return err
		}

		return nil
	}

	// R-03: durable failure recording for the main event topic — event_id,
	// tenant_id and trace_id come from models.Event when the payload parses;
	// a payload that doesn't even unmarshal still gets a durable record,
	// keyed by topic:partition:offset instead of event_id.
	recordEventFailure := newFailureRecorder(consumerFailureRepo, func(payload []byte) (eventID, tenantID, traceID string) {
		var event models.Event
		if err := json.Unmarshal(payload, &event); err != nil {
			return "", "", ""
		}
		return event.EventID, event.TenantID.String(), event.TraceID.String()
	})

	err = kafka.StartConsumer(
		consumerCtx,
		brokers,
		groupID,
		topic,
		handler,
		recordEventFailure,
		&consumerWG,
	)
	if err != nil {
		log.Fatalf("Kafka consumer failed: %v", err)
	}
	log.Println("Kafka consumer started")

	// -------- TOKENIZE RESULT CONSUMER --------

	// R-03: same durable-failure requirement as the main consumer.
	// TokenizeResultEvent has no event_id field, so its IdempotencyKey
	// stands in for one; falls back to topic:partition:offset if even that
	// isn't present or the payload doesn't parse.
	recordTokenizeResultFailure := newFailureRecorder(consumerFailureRepo, func(payload []byte) (eventID, tenantID, traceID string) {
		var event models.TokenizeResultEvent
		if err := json.Unmarshal(payload, &event); err != nil {
			return "", "", ""
		}
		return event.IdempotencyKey, event.TenantID, event.TraceID
	})

	go func() {
		err := kafka.StartConsumer(
			consumerCtx,
			brokers,
			"intent-engine-tokenize-result-group",
			resultTopic,
			resultHandler,
			recordTokenizeResultFailure,
			&consumerWG,
		)
		if err != nil {
			log.Fatalf("Kafka tokenize result consumer failed: %v", err)
		}
		log.Println("Kafka tokenize result consumer started")
	}()

	// -------- HTTP SERVER --------
	// INT-08: explicit timeouts on every phase of a connection's life --
	// previously all four were unset, meaning a slow/stalled client (or a
	// Slowloris-style attacker) could hold a connection, and its goroutine,
	// open indefinitely.
	server := &http.Server{
		Addr:              ":8083",
		Handler:           otelhttp.NewHandler(mux, "http"),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      20 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Start serving in a goroutine so main() can go on to wait for either a
	// shutdown signal or a startup/listen failure -- log.Fatal(ListenAndServe())
	// previously blocked forever with no path to ever run cleanup.
	serverErrors := make(chan error, 1)
	go func() {
		log.Println("Intent Engine (Service-2) running on :8083")
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- err
			return
		}
		serverErrors <- nil
	}()

	// -------- GRACEFUL SHUTDOWN (INT-08) --------
	shutdownCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-serverErrors:
		if err != nil {
			log.Fatalf("HTTP server failed: %v", err)
		}
	case <-shutdownCtx.Done():
		stop()
		log.Println("shutdown signal received, draining gracefully...")

		// 1. Stop accepting new work first: flip readiness to false so a
		// Kubernetes readiness probe notices and stops routing new traffic
		// for the rest of the drain window below.
		readinessHandler.SetNotReady()

		// 2. Stop the Kafka consumer groups from taking new
		// sessions/claims, then block until their goroutines have actually
		// exited -- this waits for a message already in flight to finish
		// processing (cancelling consumerCtx does not abort the in-flight
		// handler call itself; see the comment where consumerCtx is
		// created).
		cancelConsumers()
		consumerWG.Wait()
		log.Println("Kafka consumers drained")

		// 3. Drain in-flight HTTP requests.
		httpShutdownCtx, cancelHTTP := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancelHTTP()
		if err := server.Shutdown(httpShutdownCtx); err != nil {
			log.Printf("HTTP server forced to shut down: %v", err)
		} else {
			log.Println("HTTP server drained")
		}

		// 4. Close the Kafka producer.
		if err := producer.Close(); err != nil {
			log.Printf("error closing Kafka producer: %v", err)
		} else {
			log.Println("Kafka producer closed")
		}

		// 5. Close the DB pool.
		if err := db.DB.Close(); err != nil {
			log.Printf("error closing DB pool: %v", err)
		} else {
			log.Println("DB pool closed")
		}

		log.Println("shutdown complete")
	}
	// main() now returns normally, running the tracing cleanup() deferred
	// at the top -- previously unreachable, since nothing before this
	// change ever let main() return.
}

// durationEnv reads a time.ParseDuration-formatted env var (e.g. "5m",
// "30m", "24h"), falling back to def when unset or unparsable.
func durationEnv(key string, def time.Duration) time.Duration {
	val := os.Getenv(key)
	if val == "" {
		return def
	}
	d, err := time.ParseDuration(val)
	if err != nil {
		log.Printf("⚠️ invalid duration for %s=%q, using default %s: %v", key, val, def, err)
		return def
	}
	return d
}

// newFailureRecorder builds a kafka.FailureRecorder (R-03) that durably
// writes a permanently-failed message to consumer_failure_receipts before
// kafka.ConsumeClaim is allowed to mark it. parse extracts whatever
// event_id/tenant_id/trace_id the specific topic's payload schema carries —
// the main event topic and the tokenize-result topic use different Go
// types, so each StartConsumer call supplies its own parse func while
// sharing this same recording/idempotency logic.
func newFailureRecorder(
	repo persistence.ConsumerFailureRepository,
	parse func(payload []byte) (eventID, tenantID, traceID string),
) kafka.FailureRecorder {
	return func(ctx context.Context, f kafka.PermanentFailure) error {
		eventID, tenantID, traceID := parse(f.Value)

		headerMap := make(map[string]string, len(f.Headers))
		for _, h := range f.Headers {
			headerMap[string(h.Key)] = string(h.Value)
		}
		headersJSON, _ := json.Marshal(headerMap)

		errorCategory := "PROCESSING_ERROR"
		if eventID == "" && tenantID == "" && traceID == "" {
			// parse() only returns all-empty when the payload itself didn't
			// unmarshal — a real processing failure always resolves at
			// least a tenant_id, since ProcessIncomingIntent needs one too.
			errorCategory = "PAYLOAD_UNMARSHAL_ERROR"
		}

		return repo.Record(ctx, models.ConsumerFailureReceipt{
			IdempotencyKey: persistence.FailureIdempotencyKey(eventID, f.Topic, f.Partition, f.Offset),
			EventID:        eventID,
			Topic:          f.Topic,
			Partition:      f.Partition,
			Offset:         f.Offset,
			TenantID:       tenantID,
			TraceID:        traceID,
			Payload:        f.Value,
			PayloadHash:    persistence.HashPayload(f.Value),
			HeadersJSON:    headersJSON,
			ErrorCategory:  errorCategory,
			ErrorMessage:   f.LastError.Error(),
			AttemptCount:   f.Attempts,
			LastAttemptAt:  time.Now().UTC(),
		})
	}
}
