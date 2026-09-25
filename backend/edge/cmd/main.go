package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"zord-edge/config"
	"zord-edge/db"
	"zord-edge/handler"
	"zord-edge/internal/health"
	"zord-edge/logger"
	"zord-edge/middleware"
	"zord-edge/routes"
	"zord-edge/services"
	"zord-edge/storage"
	"zord-edge/tracing"
	"zord-edge/vault"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/pressly/goose/v3"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
)

var (
	// Prometheus metrics
	httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests",
		},
		[]string{"method", "endpoint", "status"},
	)

	httpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "http_request_duration_seconds",
			Help: "Duration of HTTP requests in seconds",
		},
		[]string{"method", "endpoint"},
	)
)

func init() {
	// Register Prometheus metrics
	prometheus.MustRegister(httpRequestsTotal)
	prometheus.MustRegister(httpRequestDuration)
}

func main() {
	logger.Init("zord-edge")

	// Shutdown context — cancelled on SIGTERM/SIGINT.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Initialize tracing
	cleanup := tracing.InitTracing("zord-edge")
	defer cleanup()

	gin.SetMode(gin.ReleaseMode)
	server := gin.Default()
	// gin trusts every proxy by default, letting clients spoof X-Forwarded-For
	// and mint fresh per-IP rate-limit keys. Trust only EDGE_TRUSTED_PROXIES
	// (comma-separated IPs/CIDRs); empty = trust none, ClientIP = RemoteAddr.
	trustedProxies := middleware.TrustedProxiesFromEnv()
	if err := middleware.ApplyTrustedProxies(server, trustedProxies); err != nil {
		logger.Log.Error("invalid EDGE_TRUSTED_PROXIES",
			slog.String("error", err.Error()))
		os.Exit(1)
	}
	logger.Log.Info("trusted proxies configured", slog.Int("count", len(trustedProxies)))
	server.Use(
		otelgin.Middleware("zord-edge"),
		prometheusMiddleware(),
	)

	config.InitDB()
	if db.DB == nil {
		logger.Log.Error("database handle is nil after InitDB")
		os.Exit(1)
	}

	goose.SetBaseFS(nil)
	if err := goose.SetDialect("postgres"); err != nil {
		logger.Log.Error("goose dialect setup failed",
			slog.String("error", err.Error()))
		os.Exit(1)
	}
	if err := goose.Up(db.DB, "db/migrations"); err != nil {
		logger.Log.Error("database migrations failed",
			slog.String("error", err.Error()))
		os.Exit(1)
	}

	err := godotenv.Load()
	if err != nil {
		logger.Log.Error("No .env file found")
	}

	bucket := os.Getenv("S3_BUCKET")
	region := os.Getenv("AWS_REGION")

	if bucket == "" || region == "" {
		logger.Log.Error("required S3 environment variables missing",
			slog.String("S3_BUCKET", bucket),
			slog.String("AWS_REGION", region))
		os.Exit(1)
	}

	s3store, err := storage.NewS3Store(context.Background(), bucket, region)
	if err != nil {
		logger.Log.Error("S3 store initialization failed",
			slog.String("bucket", bucket),
			slog.String("region", region),
			slog.String("error", err.Error()))
		os.Exit(1)
	}

	h := &handler.Handler{
		S3store: s3store,
	}

	err = vault.InitVault()
	if err != nil {
		logger.Log.Error("vault initialization failed",
			slog.String("error", err.Error()))
		os.Exit(1)
	}
	signingKeyPath := os.Getenv("SIGNING_KEY_PATH")
	if signingKeyPath == "" {
		signingKeyPath = "ed25519_private.pem"
	}
	err = vault.InitSigningKey(signingKeyPath)
	if err != nil {
		logger.Log.Error("signing key load failed",
			slog.String("path", signingKeyPath),
			slog.String("error", err.Error()))
		os.Exit(1)
	}

	// Initialize HS256 JWT signing secret (shared with Kong for gateway-level validation).
	if err := services.InitJWTSigningSecret(); err != nil {
		logger.Log.Error("JWT signing secret initialization failed",
			slog.String("error", err.Error()))
		os.Exit(1)
	}

	routes.Routes(server, h)

	// Add metrics endpoint
	server.GET("/metrics", gin.WrapH(promhttp.Handler()))

	// Add health check endpoint (liveness — is the process alive?)
	server.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"status":  "healthy",
			"service": "zord-edge",
			"time":    time.Now().UTC(),
		})
	})

	// Readiness endpoint — checks DB and S3 connectivity
	readinessHandler := health.NewReadinessHandler([]health.DependencyCheck{
		health.DBCheck("postgres", db.DB),
	})
	server.GET("/ready", readinessHandler.Ready)

	outboxPullRepo := services.NewOutboxPullRepo(db.DB)
	outboxHandler := handler.NewOutboxHandler(outboxPullRepo)
	server.GET("/internal/outbox/lease", outboxHandler.Lease)
	server.POST("/internal/outbox/ack", outboxHandler.Ack)
	server.POST("/internal/outbox/nack", outboxHandler.Nack)
	server.GET("/internal/webhooks/receipts/index", h.HandleWebhookReceiptIndex)

	// D52: recon reads per-tenant Razorpay credentials; relay/recon resolve
	// connector slugs to ids. The credentials route fails closed (503) until
	// RECON_CREDENTIALS_TOKEN is set and differs from RELAY_AUTH_TOKEN.
	if _, err := handler.ReconCredentialsToken(); err != nil {
		logger.Log.Error("internal credentials endpoint disabled", slog.String("reason", err.Error()))
	}
	handler.RegisterInternalConnectorRoutes(server, handler.NewInternalConnectorHandler(db.DB))

	logger.Log.Info("starting zord-edge service",
		slog.String("addr", ":8080"),
		slog.Bool("observability", true))
	srv := &http.Server{
		Addr:              ":8080",
		Handler:           server,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       5 * time.Minute,
		WriteTimeout:      10 * time.Minute,
		IdleTimeout:       10 * time.Minute,
	}

	// Start server in a goroutine so it doesn't block.
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Log.Error("server failed to start",
				slog.String("error", err.Error()))
			os.Exit(1)
		}
	}()

	// Wait for interruption signal.
	<-ctx.Done()

	// Restore default behavior on the interrupt signal and notify user of shutdown.
	stop()
	logger.Log.Info("shutting down gracefully, press Ctrl+C again to force")

	// The context is used to inform the server it has 10 seconds to finish
	// the request it is currently handling
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Log.Error("server forced to shutdown",
			slog.String("error", err.Error()))
		os.Exit(1)
	}

	logger.Log.Info("server exited cleanly")
}

// prometheusMiddleware adds Prometheus metrics collection
func prometheusMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		c.Next()

		duration := time.Since(start).Seconds()
		status := strconv.Itoa(c.Writer.Status())

		path := c.FullPath()
		if path == "" {
			// Avoid high-cardinality metrics labels for unmatched routes.
			path = "/unmatched"
		}

		httpRequestsTotal.WithLabelValues(c.Request.Method, path, status).Inc()
		httpRequestDuration.WithLabelValues(c.Request.Method, path).Observe(duration)
	}
}
