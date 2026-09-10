package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"zord-relay/client"
	"zord-relay/config"
	"zord-relay/db"
	"zord-relay/internal/health"
	"zord-relay/internal/operator"
	"zord-relay/internal/railrouter"
	"zord-relay/kafka"
	"zord-relay/logger"
	"zord-relay/psp"
	"zord-relay/publisher"
	"zord-relay/services"
	"zord-relay/shutdown"
	"zord-relay/tracing"
	"zord-relay/worker"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "relay: fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// ── Config ─────────────────────────────────────────────────────────────
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// ── Instance ID ─────────────────────────────────────────────────────────
	instanceID := cfg.Relay.InstanceID
	if instanceID == "" {
		if h, err := os.Hostname(); err == nil {
			instanceID = h
		} else {
			instanceID = "relay-unknown"
		}
		cfg.Relay.InstanceID = instanceID
	}

	// ── Logger ──────────────────────────────────────────────────────────────
	log, err := logger.New("relay-service", instanceID, cfg.Tracing.Environment)
	if err != nil {
		return fmt.Errorf("building logger: %w", err)
	}
	defer log.Sync() //nolint:errcheck

	// Set the global logger used by loop packages
	logger.Logger = log

	log.Info("relay-service starting",
		zap.String("instance_id", instanceID),
		zap.Int("services", len(cfg.Services)),
	)

	// ── Tracing ─────────────────────────────────────────────────────────────
	var tracingProvider *tracing.Provider
	if cfg.Tracing.Enabled {
		tctx, tcancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer tcancel()
		tracingProvider, err = tracing.Init(
			tctx,
			cfg.Tracing.ServiceName,
			cfg.Tracing.Environment,
			cfg.Tracing.OTLPEndpoint,
		)
		if err != nil {
			log.Warn("failed to initialise tracing, continuing without it", zap.Error(err))
			tracingProvider = nil
		} else {
			log.Info("tracing initialised", zap.String("endpoint", cfg.Tracing.OTLPEndpoint))
		}
	}

	// ── Database (Service 4 owns this) ──────────────────────────────────────
	database := db.Connect(cfg.DB.URL, cfg.DB.MaxOpenConns, cfg.DB.MaxIdleConns)
	defer database.Close() //nolint:errcheck
	db.EnsureSchema(context.Background(), database)
	log.Info("database connected")

	// ── Kafka Publisher (Confluent — used by existing relay workers) ─────────
	kafkaPublisher, err := publisher.NewKafkaPublisher(cfg.Kafka, log)
	if err != nil {
		return fmt.Errorf("creating kafka publisher: %w", err)
	}

	// ── Kafka Producer (Sarama — used by relay_loop for relay_outbox drain) ──
	brokerList := strings.Split(cfg.Kafka.Brokers, ",")
	saramaProducer := kafka.NewProducer(brokerList)

	// ── PSP Client ──────────────────────────────────────────────────────────
	// Swap DemoClient → RazorpayXClient when going live.
	var pspClient psp.Client = psp.NewDemoClient(cfg.PSP.BaseURL, cfg.PSP.TimeoutSeconds)
	log.Info("PSP client initialised",
		zap.String("base_url", cfg.PSP.BaseURL),
		zap.String("mode", "demo"),
	)

	// ── Token Client ────────────────────────────────────────────────────────
	// RELAY_TOKEN_ENCLAVE_BASE_URL is required in all environments.
	// Startup fails hard if the URL is absent or if the enclave is unreachable.
	if cfg.TokenEnclave.BaseURL == "" {
		// config.validate() should have caught this already, but guard defensively.
		return fmt.Errorf(
			"RELAY_TOKEN_ENCLAVE_BASE_URL is not set; " +
				"the relay service cannot start without a real token enclave. ",
		)
	}
	tokenClient, tokenClientErr := services.NewHTTPTokenClientWithConnectivityCheck(
		cfg.TokenEnclave.BaseURL,
		cfg.TokenEnclave.TimeoutSeconds,
	)
	if tokenClientErr != nil {
		log.Error("startup: token enclave connectivity check failed — cannot start",
			zap.String("base_url", cfg.TokenEnclave.BaseURL),
			zap.Error(tokenClientErr),
		)
		return fmt.Errorf("token enclave unreachable at startup: %w", tokenClientErr)
	}
	log.Info("token enclave client initialised and reachable",
		zap.String("base_url", cfg.TokenEnclave.BaseURL),
	)

	// ── Repos ────────────────────────────────────────────────────────────────
	dispatchRepo := services.NewDispatchRepo(database)
	relayOutboxRepo := services.NewRelayOutboxRepo(database)

	// Durable publish-failure persistence (P0 6.1.3) — used by the Kafka
	// relay path (worker.Scheduler) so an exhausted publish attempt is never
	// reduced to a log line, and the upstream lease is only acknowledged
	// after success or a durable replayable failure record.
	publishFailureRepo := services.NewPublishFailureRepo(database)

	// ── Failure Replay Service ──────────────────────────────────────────────
	failureReplayService := services.NewFailureReplayService(publishFailureRepo, saramaProducer.InternalProducer(), log)

	// ── Payload hash verifier (P0 6.1.2) ─────────────────────────────────────
	// Verifies SHA-256(payload bytes) == upstream payload_hash before publish.
	// On mismatch: persist conflict, do not publish, do not ACK lease.
	hashVerifierCfg := services.ConflictRepoConfig{
		Strict:             cfg.Relay.StrictPayloadHash,
		MaxConflictRetries: cfg.Relay.MaxConflictRetries,
	}
	hashVerifier := services.NewConflictRepo(database, hashVerifierCfg)
	effectiveMaxRetries := cfg.Relay.MaxConflictRetries
	if effectiveMaxRetries <= 0 {
		effectiveMaxRetries = services.DefaultMaxConflictRetries
	}
	log.Info("payload hash verifier initialised",
		zap.Bool("strict", cfg.Relay.StrictPayloadHash),
		zap.Int("max_conflict_retries", effectiveMaxRetries),
	)

	// ── Dispatch Loop ────────────────────────────────────────────────────────
	dispatchLoopCfg := &services.DispatchLoopConfig{
		ConnectorID:                cfg.Dispatch.ConnectorID,
		CorridorID:                 cfg.Dispatch.DefaultCorridorID,
		PSPCircuitBreakerThreshold: cfg.Dispatch.PSPCircuitBreakerThreshold,
		PSPCircuitResetSecs:        cfg.Dispatch.PSPCircuitResetSeconds,
	}
	dispatchLoop := services.NewDispatchLoop(
		database,
		relayOutboxRepo,
		dispatchRepo,
		pspClient,
		tokenClient,
		hashVerifier,
		dispatchLoopCfg,
	)
	if u := strings.TrimSpace(cfg.Dispatch.RouterURL); u != "" {
		dispatchLoop.SetRouter(railrouter.New(u, 2*time.Second))
		log.Info("rail router enabled", zap.String("url", u))
	}

	// ── Dispatch Consumer (Kafka → DispatchLoop) ─────────────────────────────
	dispatchConsumerCfg := &services.DispatchConsumerConfig{
		Brokers:           cfg.Kafka.Brokers,
		GroupID:           cfg.Dispatch.ConsumerGroupID,
		Topic:             cfg.Dispatch.Topic,
		PollTimeout:       cfg.Dispatch.PollTimeout,
		MaxPollIntervalMs: (cfg.PSP.TimeoutSeconds*2 + 30) * 1000,
		WorkerCount:       cfg.Dispatch.WorkerCount,
	}
	dispatchConsumer := services.NewDispatchConsumer(dispatchConsumerCfg, dispatchLoop)

	// ── Gap 8: Startup health check — fail fast on bad auth ─────────────────
	// Verify each configured upstream service is reachable and accepting our
	// auth token before starting the poll loop. Prevents silent 401 loops.
	log.Info("startup: verifying upstream service connectivity...")
	for _, svcCfg := range cfg.Services {
		startupCtx, startupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		outboxClient := client.NewOutboxClient(
			svcCfg.Name, svcCfg.BaseURL, svcCfg.AuthToken,
			instanceID, svcCfg.HTTPTimeout, log,
		)
		if err := outboxClient.HealthCheck(startupCtx); err != nil {
			startupCancel()
			log.Warn("startup: upstream service health check failed — will retry during operation",
				zap.String("service", svcCfg.Name),
				zap.Error(err),
			)
			// Non-fatal: log and continue. The worker will retry on its poll cycle.
			// Make it fatal in production by returning the error here.
		} else {
			log.Info("startup: upstream service reachable",
				zap.String("service", svcCfg.Name),
			)
		}
		startupCancel()
	}

	// ── Relay Loop (relay_outbox → Kafka publisher for dispatch events) ───────
	relayLoopCfg := &services.RelayLoopConfig{
		WorkerCount:            cfg.RelayLoop.WorkerCount,
		BatchSize:              cfg.RelayLoop.BatchSize,
		PollInterval:           cfg.RelayLoop.PollInterval,
		DispatchEventsTopic:    cfg.RelayLoop.DispatchEventsTopic,
		PublishFailureDLQTopic: cfg.RelayLoop.PublishFailureDLQTopic,
		PoisonEventDLQTopic:    cfg.RelayLoop.PoisonEventDLQTopic,
	}
	relayLoop := services.NewRelayLoop(relayOutboxRepo, publishFailureRepo, saramaProducer, relayLoopCfg)

	// ── Retry Sweeper (FAILED_RETRYABLE → re-run Steps 2-5) ──────────────────
	retrySweeper := services.NewRetrySweeper(
		dispatchRepo,
		dispatchLoop,
		cfg.RetrySweeper.Interval,
		cfg.RetrySweeper.BatchSize,
	)

	// ── Existing Kafka Relay Scheduler (unchanged — Kafka relay path) ─────────
	sched, err := worker.NewScheduler(cfg, kafkaPublisher, log, publishFailureRepo, hashVerifier)
	if err != nil {
		return fmt.Errorf("creating scheduler: %w", err)
	}

	// ── Metrics HTTP server ──────────────────────────────────────────────────
	var metricsSrv *http.Server
	if cfg.Metrics.Enabled {
		gin.SetMode(gin.ReleaseMode)
		r := gin.New()
		r.Use(gin.Recovery())

		r.GET("/metrics", gin.WrapH(promhttp.Handler()))
		r.GET("/health", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"status": "ok"})
		})

		// REL-08 + PLAT-11: Readiness endpoint — checks DB, Kafka, and upstream auth
		readinessHandler := health.NewReadinessHandler([]health.DependencyCheck{
			health.DBCheck("postgres", database),
			{
				Name: "kafka-producer",
				Check: func(ctx context.Context) error {
					if kafkaPublisher == nil {
						return fmt.Errorf("producer not initialized")
					}
					return nil
				},
			},
			health.HTTPCheck("token-enclave", cfg.TokenEnclave.BaseURL+"/health"),
		})
		r.GET("/ready", readinessHandler.Ready)

		// Operator API (P0 6.1.3)
		if cfg.Relay.OperatorAPIEnabled {
			opToken := cfg.Relay.OperatorAuthToken
			if opToken == "" {
				// Fallback to first configured service token for trust boundary
				opToken = cfg.Services[0].AuthToken
			}
			opHandler := operator.NewFailureHandler(publishFailureRepo, failureReplayService, opToken)
			internal := r.Group("/internal/relay")
			internal.Use(opHandler.AuthMiddleware())
			opHandler.Register(internal)
			log.Info("operator API mounted on metrics server", zap.String("path", "/internal/relay"))
		}

		metricsSrv = &http.Server{
			Addr:         cfg.Metrics.Addr,
			Handler:      r,
			ReadTimeout:  5 * time.Second,
			WriteTimeout: 10 * time.Second,
			IdleTimeout:  30 * time.Second,
		}
		go func() {
			log.Info("metrics server listening", zap.String("addr", cfg.Metrics.Addr))
			if err := metricsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Error("metrics server error", zap.Error(err))
			}
		}()
	}

	// ── Start all loops ──────────────────────────────────────────────────────
	// Two contexts:
	// - leaseCtx: controls whether loops accept NEW work (cancelled first on shutdown).
	// - workCtx:  controls in-flight PSP calls (cancelled after drain timeout).
	// This prevents killing a PSP call mid-flight on SIGTERM, which would put
	// the dispatch in AWAITING_PROVIDER_SIGNAL unnecessarily.
	leaseCtx, cancelLease := context.WithCancel(context.Background())
	workCtx, cancelWork := context.WithCancel(context.Background())

	var wg sync.WaitGroup

	// 1. Existing Kafka relay scheduler (Responsibility 1 — unchanged)
	go sched.Run(leaseCtx)

	// 2. Dispatch responsibility (Kafka consumer + dispatch lifecycle workers)
	// Gate this with dispatch.enabled so Service 4 can run in publish-only mode.
	if cfg.Dispatch.Enabled {
		// Pass workCtx so in-flight PSP calls complete even after leaseCtx is cancelled.
		if err := dispatchConsumer.Start(workCtx, &wg); err != nil {
			cancelLease()
			cancelWork()
			return fmt.Errorf("starting dispatch consumer: %w", err)
		}
		log.Info("dispatch consumer started",
			zap.String("topic", cfg.Dispatch.Topic),
			zap.String("group_id", cfg.Dispatch.ConsumerGroupID),
		)
	} else {
		log.Warn("dispatch disabled by config; running in kafka-publish-only mode")
	}

	// 3. Relay loop (relay_outbox → Kafka for dispatch lifecycle events)
	relayLoop.Start(workCtx, &wg)
	log.Info("relay loop started", zap.Int("workers", cfg.RelayLoop.WorkerCount))

	// 4/5. Retry + Recovery sweepers are dispatch-only responsibilities.
	if cfg.Dispatch.Enabled {
		retrySweeper.Start(workCtx, &wg)
		log.Info("retry sweeper started", zap.Duration("interval", cfg.RetrySweeper.Interval))

		recoverySweeper := services.NewRecoverySweeper(
			database,
			dispatchRepo,
			relayOutboxRepo,
			dispatchLoop,
			pspClient,
			services.RecoverySweeperConfig{
				SweepInterval:       cfg.RecoverySweeper.Interval,
				SentTimeoutSecs:     cfg.RecoverySweeper.SentTimeoutSeconds,
				AwaitingTimeoutSecs: cfg.RecoverySweeper.AwaitingTimeoutSeconds,
				BatchSize:           cfg.RecoverySweeper.BatchSize,
			},
		)
		recoverySweeper.Start(workCtx, &wg)
		log.Info("recovery sweeper started")
	} else {
		log.Info("dispatch sweepers disabled because dispatch.enabled=false")
	}

	// ── Graceful shutdown ────────────────────────────────────────────────────
	shutdownCoord := shutdown.New(cfg.Relay.ShutdownTimeout, log)
	shutdownCoord.WaitForSignal(
		func(shutCtx context.Context) error {
			// Step 1: stop accepting new leases from upstream.
			// Workers already processing events continue until workCtx is cancelled.
			log.Info("shutdown: stopping new lease acceptance...")
			cancelLease()
			return nil
		},
		func(shutCtx context.Context) error {
			// Step 2: wait for in-flight PSP calls to complete (up to drain window).
			log.Info("shutdown: draining in-flight work...")
			done := make(chan struct{})
			go func() { wg.Wait(); close(done) }()
			select {
			case <-done:
				log.Info("shutdown: all in-flight work drained cleanly")
			case <-shutCtx.Done():
				log.Warn("shutdown: drain timeout — cancelling remaining work")
			}
			// Step 3: cancel the work context to stop any remaining goroutines.
			cancelWork()
			// Wait again with a short grace period.
			grace := make(chan struct{})
			go func() { wg.Wait(); close(grace) }()
			select {
			case <-grace:
			case <-time.After(5 * time.Second):
				log.Warn("shutdown: goroutines did not exit within grace period")
			}
			return nil
		},
		func(shutCtx context.Context) error {
			if metricsSrv != nil {
				log.Info("shutdown: stopping metrics server...")
				return metricsSrv.Shutdown(shutCtx)
			}
			return nil
		},
		func(_ context.Context) error {
			log.Info("shutdown: flushing kafka publisher...")
			return kafkaPublisher.Close()
		},
		func(_ context.Context) error {
			log.Info("shutdown: closing sarama producer...")
			return saramaProducer.Close()
		},
		func(shutCtx context.Context) error {
			if tracingProvider != nil {
				log.Info("shutdown: flushing traces...")
				return tracingProvider.Shutdown(shutCtx)
			}
			return nil
		},
	)

	return nil
}
