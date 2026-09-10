package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config is the root configuration for the relay service.
type Config struct {
	Relay           RelayConfig           `mapstructure:"relay"`
	Kafka           KafkaConfig           `mapstructure:"kafka"`
	Services        []ServiceConfig       `mapstructure:"services"`
	Tracing         TracingConfig         `mapstructure:"tracing"`
	Metrics         MetricsConfig         `mapstructure:"metrics"`
	DB              DBConfig              `mapstructure:"db"`
	PSP             PSPConfig             `mapstructure:"psp"`
	TokenEnclave    TokenEnclaveConfig    `mapstructure:"token_enclave"`
	Dispatch        DispatchConfig        `mapstructure:"dispatch"`
	RelayLoop       RelayLoopConfig       `mapstructure:"relay_loop"`
	RetrySweeper    RetrySweeperConfig    `mapstructure:"retry_sweeper"`
	RecoverySweeper RecoverySweeperConfig `mapstructure:"recovery_sweeper"`
}

// RelayConfig holds global relay behaviour settings.
type RelayConfig struct {
	// Unique identity for this relay instance (used in lease ownership + logs).
	// Defaults to hostname. Should be set to pod name in Kubernetes.
	InstanceID string `mapstructure:"instance_id"`

	// PollInterval is how long a worker sleeps between lease cycles when the
	// upstream outbox is empty.
	PollInterval time.Duration `mapstructure:"poll_interval"`

	// LeaseLimit is the default batch size for /lease calls.
	LeaseLimit int `mapstructure:"lease_limit"`

	// LeaseTTLSeconds is the lease lock duration requested from upstream.
	LeaseTTLSeconds int `mapstructure:"lease_ttl_seconds"`

	// MaxPublishConcurrency is the semaphore size — max in-flight Kafka publishes
	// per worker at any time. Controls backpressure.
	MaxPublishConcurrency int `mapstructure:"max_publish_concurrency"`

	// ShutdownTimeout is how long we wait for in-flight work to finish on SIGTERM.
	ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout"`

	// StrictPayloadHash controls how empty payload_hash is handled.
	//   false (default): empty payload_hash → skip verification, warn log,
	//                    counter bump. Allows upstreams to ship without hashing.
	//   true:            empty payload_hash → treat as mismatch + conflict record.
	// Flip to true once all upstreams guarantee payload_hash is populated.
	StrictPayloadHash bool `mapstructure:"strict_payload_hash"`

	// MaxConflictRetries is how many times we NACK a mismatching event before
	// giving up and ACKing it out of the upstream outbox (with a permanent
	// conflict record in relay_payload_conflicts). Guards against infinite
	// NACK loops on genuinely corrupt upstream rows. Default 3.
	MaxConflictRetries int `mapstructure:"max_conflict_retries"`

	// OperatorAPIEnabled enables the controlled replay HTTP API.
	OperatorAPIEnabled bool `mapstructure:"operator_api_enabled"`

	// OperatorAuthToken is the secret required in X-Relay-Token header for operator API.
	OperatorAuthToken string `mapstructure:"operator_auth_token"`
}

// KafkaConfig holds all Kafka connection and auth settings.
type KafkaConfig struct {
	// Brokers is the comma-separated list of bootstrap servers.
	Brokers string `mapstructure:"brokers"`

	// Auth — SASL/SCRAM-SHA-512 (recommended for fintech).
	SASLMechanism string `mapstructure:"sasl_mechanism"` // SCRAM-SHA-512
	SASLUsername  string `mapstructure:"sasl_username"`
	SASLPassword  string `mapstructure:"sasl_password"`

	// TLS — should be true in production.
	TLSEnabled bool `mapstructure:"tls_enabled"`

	// Producer tuning.
	// Acks: "all" = strongest durability guarantee (required for fintech).
	Acks            string        `mapstructure:"acks"`
	LingerMs        int           `mapstructure:"linger_ms"`
	CompressionType string        `mapstructure:"compression_type"` // snappy / lz4 / zstd
	MessageMaxBytes int           `mapstructure:"message_max_bytes"`
	DeliveryTimeout time.Duration `mapstructure:"delivery_timeout"`

	// DLQ topics.
	DLQPublishFailureTopic string `mapstructure:"dlq_publish_failure_topic"`
	DLQPoisonTopic         string `mapstructure:"dlq_poison_topic"`
}

// ServiceConfig describes one upstream service that relay polls (Kafka relay path).
type ServiceConfig struct {
	// Name is a short identifier used in logs and metrics labels.
	Name string `mapstructure:"name"`

	// BaseURL is the root URL of the upstream service.
	BaseURL string `mapstructure:"base_url"`

	// AuthToken is the shared secret sent as X-Relay-Token header.
	AuthToken string `mapstructure:"auth_token"`

	// HTTPTimeout for lease/ack/nack calls to this service.
	HTTPTimeout time.Duration `mapstructure:"http_timeout"`

	// DefaultTopic is the Kafka topic to publish to if no specific mapping exists.
	DefaultTopic string `mapstructure:"default_topic"`

	// TopicMap allows routing specific event types to different topics.
	// Key: event_type, Value: Kafka topic name.
	TopicMap map[string]string `mapstructure:"topic_map"`

	// RouteAllowList is the explicit allow-list for event routing.
	// Any event NOT matching an entry in this list goes to the poison DLQ (fail-closed).
	// When empty, routing falls back to TopicMap + DefaultTopic (legacy mode).
	RouteAllowList []RouteAllowListEntry `mapstructure:"route_allow_list"`

	// IsDLQ tells relay to use DLQClient/DLQWorker instead of OutboxClient/OutboxWorker
	IsDLQ bool `mapstructure:"is_dlq"`

	// IsBatch tells relay to use BatchClient/BatchWorker instead of OutboxClient/OutboxWorker
	IsBatch bool `mapstructure:"is_batch"`

	// IsEdge tells the worker to lease/publish using zord-edge's own event
	// shape (model.EdgeOutboxEvent) instead of the shared generic OutboxEvent.
	IsEdge bool `mapstructure:"is_edge"`

	// IsIntent tells the worker to lease/publish using zord-intent-engine's
	// own event shape (model.IntentOutboxEvent) instead of the shared
	// generic OutboxEvent.
	IsIntent bool `mapstructure:"is_intent"`

	// Retry settings (Kafka-side) — override global if set.
	MaxRetryAttempts int           `mapstructure:"max_retry_attempts"`
	RetryBaseDelay   time.Duration `mapstructure:"retry_base_delay"`
	RetryMaxDelay    time.Duration `mapstructure:"retry_max_delay"`
}

// RouteAllowListEntry defines an exact match rule for routing.
type RouteAllowListEntry struct {
	SourceService string `mapstructure:"source_service"`
	EventType     string `mapstructure:"event_type"`
	EventVersion  string `mapstructure:"event_version"`
	SchemaVersion string `mapstructure:"schema_version"`
	Topic         string `mapstructure:"topic"`
}

// DBConfig holds connection settings for Service 4's own Postgres database.
type DBConfig struct {
	// URL is the full postgres DSN. Override with RELAY_DB_URL env var.
	URL          string `mapstructure:"url"`
	MaxOpenConns int    `mapstructure:"max_open_conns"`
	MaxIdleConns int    `mapstructure:"max_idle_conns"`
}

// PSPConfig holds settings for the external Payment Service Provider client.
type PSPConfig struct {
	// BaseURL is the PSP API base URL. Override with RELAY_PSP_BASE_URL env var.
	BaseURL        string `mapstructure:"base_url"`
	TimeoutSeconds int    `mapstructure:"timeout_seconds"`
}

// TokenEnclaveConfig holds settings for Service 3 (token enclave / detokenizer).
type TokenEnclaveConfig struct {
	// BaseURL is the token enclave API base URL.
	// Override with RELAY_TOKEN_ENCLAVE_BASE_URL env var.
	BaseURL        string `mapstructure:"base_url"`
	TimeoutSeconds int    `mapstructure:"timeout_seconds"`
}

// DispatchConfig holds all settings for the Kafka-triggered dispatch loop.
type DispatchConfig struct {
	Enabled           bool          `mapstructure:"enabled"`
	ConsumerGroupID   string        `mapstructure:"consumer_group_id"`
	Topic             string        `mapstructure:"topic"`
	PollTimeout       time.Duration `mapstructure:"poll_timeout"`
	ConnectorID       string        `mapstructure:"connector_id"`
	DefaultCorridorID string        `mapstructure:"default_corridor_id"`
	// RouterURL is zord-router (Hyperswitch-style rail/PSP selector). Empty = hardcoded connector.
	RouterURL string `mapstructure:"router_url"`

	// WorkerCount is the size of the dispatch worker pool (Gap 14).
	// Controls max concurrent PSP calls. Default: 4.
	// Must not exceed DB max_open_conns minus relay loop workers.
	WorkerCount int `mapstructure:"worker_count"`

	// Circuit breaker settings.
	PSPCircuitBreakerThreshold int `mapstructure:"psp_circuit_breaker_threshold"`
	PSPCircuitResetSeconds     int `mapstructure:"psp_circuit_reset_seconds"`
}

// RecoverySweeperConfig holds settings for the SENT + AWAITING_PROVIDER_SIGNAL sweeper.
// This is separate from RetrySweeperConfig which handles FAILED_RETRYABLE only.
type RecoverySweeperConfig struct {
	// Interval between full sweep cycles. Default: 60s.
	Interval time.Duration `mapstructure:"interval"`
	// SentTimeoutSeconds: SENT rows older than this are treated as crash victims.
	// Must be > PSP timeout seconds. Default: PSP timeout * 2.
	SentTimeoutSeconds int `mapstructure:"sent_timeout_seconds"`
	// AwaitingTimeoutSeconds: how long to wait before re-querying PSP for
	// AWAITING_PROVIDER_SIGNAL rows. Default: 300s (5 min).
	AwaitingTimeoutSeconds int `mapstructure:"awaiting_timeout_seconds"`
	// BatchSize limits rows processed per sweep. Default: 50.
	BatchSize int `mapstructure:"batch_size"`
}

// RelayLoopConfig holds settings for the relay_outbox → Kafka publisher loop.
type RelayLoopConfig struct {
	WorkerCount            int           `mapstructure:"worker_count"`
	BatchSize              int           `mapstructure:"batch_size"`
	PollInterval           time.Duration `mapstructure:"poll_interval"`
	DispatchEventsTopic    string        `mapstructure:"dispatch_events_topic"`
	PublishFailureDLQTopic string        `mapstructure:"publish_failure_dlq_topic"`
	PoisonEventDLQTopic    string        `mapstructure:"poison_event_dlq_topic"`
}

// RetrySweeperConfig holds settings for the FAILED_RETRYABLE dispatch sweeper.
type RetrySweeperConfig struct {
	Interval  time.Duration `mapstructure:"interval"`
	BatchSize int           `mapstructure:"batch_size"`
}

// TracingConfig holds OpenTelemetry settings.
type TracingConfig struct {
	Enabled      bool   `mapstructure:"enabled"`
	OTLPEndpoint string `mapstructure:"otlp_endpoint"`
	ServiceName  string `mapstructure:"service_name"`
	Environment  string `mapstructure:"environment"`
}

// MetricsConfig holds Prometheus settings.
type MetricsConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Addr    string `mapstructure:"addr"`
}

// Load reads configuration from file + env vars.
// Environment variables override file values.
// Prefix: RELAY_ (e.g. RELAY_PSP_BASE_URL, RELAY_TOKEN_ENCLAVE_BASE_URL)
func Load() (*Config, error) {
	v := viper.New()

	// ── Relay defaults ──────────────────────────────────────────────────────
	v.SetDefault("relay.poll_interval", "2s")
	v.SetDefault("relay.lease_limit", 500)
	v.SetDefault("relay.lease_ttl_seconds", 120)
	v.SetDefault("relay.max_publish_concurrency", 10)
	v.SetDefault("relay.shutdown_timeout", "30s")

	// ── Kafka defaults ──────────────────────────────────────────────────────
	v.SetDefault("kafka.sasl_mechanism", "SCRAM-SHA-512")
	v.SetDefault("kafka.tls_enabled", true)
	v.SetDefault("kafka.acks", "all")
	v.SetDefault("kafka.linger_ms", 5)
	v.SetDefault("kafka.compression_type", "snappy")
	v.SetDefault("kafka.message_max_bytes", 1048576) // 1 MiB
	v.SetDefault("kafka.delivery_timeout", "30s")
	v.SetDefault("kafka.dlq_publish_failure_topic", "relay.dlq.publish_failure")
	v.SetDefault("kafka.dlq_poison_topic", "relay.dlq.poison")

	// ── DB defaults ─────────────────────────────────────────────────────────
	v.SetDefault("db.max_open_conns", 20)
	v.SetDefault("db.max_idle_conns", 5)

	// ── PSP defaults ────────────────────────────────────────────────────────
	v.SetDefault("psp.base_url", "http://localhost:8099")
	v.SetDefault("psp.timeout_seconds", 30)

	// ── Token enclave defaults ──────────────────────────────────────────────
	v.SetDefault("token_enclave.timeout_seconds", 10)

	// ── Dispatch defaults ───────────────────────────────────────────────────
	v.SetDefault("dispatch.enabled", true)
	v.SetDefault("dispatch.consumer_group_id", "dispatch-loop-group")
	v.SetDefault("dispatch.topic", "payments.intent.events.v1")
	v.SetDefault("dispatch.poll_timeout", "200ms")
	v.SetDefault("dispatch.connector_id", "razorpayx-v1")
	v.SetDefault("dispatch.default_corridor_id", "IMPS")
	v.SetDefault("dispatch.router_url", "")
	v.SetDefault("dispatch.worker_count", 4)
	v.SetDefault("dispatch.psp_circuit_breaker_threshold", 5)
	v.SetDefault("dispatch.psp_circuit_reset_seconds", 60)

	// ── Recovery sweeper defaults (Gaps 1, 2, 3) ─────────────────────────────
	v.SetDefault("recovery_sweeper.interval", "60s")
	v.SetDefault("recovery_sweeper.sent_timeout_seconds", 120) // psp.timeout_seconds * 2
	v.SetDefault("recovery_sweeper.awaiting_timeout_seconds", 300)
	v.SetDefault("recovery_sweeper.batch_size", 50)

	// ── Relay loop defaults ─────────────────────────────────────────────────
	v.SetDefault("relay_loop.worker_count", 2)
	v.SetDefault("relay_loop.batch_size", 50)
	v.SetDefault("relay_loop.poll_interval", "2s")
	v.SetDefault("relay_loop.dispatch_events_topic", "payments.dispatch.events.v1")
	v.SetDefault("relay_loop.publish_failure_dlq_topic", "relay.dlq.publish_failure")
	v.SetDefault("relay_loop.poison_event_dlq_topic", "relay.dlq.poison")

	// ── Retry sweeper defaults ──────────────────────────────────────────────
	v.SetDefault("retry_sweeper.interval", "30s")
	v.SetDefault("retry_sweeper.batch_size", 20)

	// ── Tracing/Metrics defaults ────────────────────────────────────────────
	v.SetDefault("tracing.enabled", true)
	v.SetDefault("tracing.service_name", "relay-service")
	v.SetDefault("tracing.environment", "production")
	v.SetDefault("metrics.enabled", true)
	v.SetDefault("metrics.addr", ":9090")

	// ── File ────────────────────────────────────────────────────────────────
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")
	v.AddConfigPath("./config")
	v.AddConfigPath("/etc/relay")

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("reading config file: %w", err)
		}
		// Config file is optional; env vars alone are valid.
	}

	// ── Environment variable overrides ──────────────────────────────────────
	// All env vars are prefixed RELAY_ with dots replaced by underscores.
	// Examples:
	//   RELAY_DB_URL                    → db.url
	//   RELAY_PSP_BASE_URL              → psp.base_url
	//   RELAY_TOKEN_ENCLAVE_BASE_URL    → token_enclave.base_url
	//   RELAY_KAFKA_SASL_PASSWORD       → kafka.sasl_password
	v.SetEnvPrefix("RELAY")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshalling config: %w", err)
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &cfg, nil
}

func (c *Config) validate() error {
	if c.Kafka.Brokers == "" {
		return fmt.Errorf("kafka.brokers is required")
	}
	if len(c.Services) == 0 {
		return fmt.Errorf("at least one service must be configured under services[]")
	}
	for i, svc := range c.Services {
		if svc.Name == "" {
			return fmt.Errorf("services[%d].name is required", i)
		}
		if svc.BaseURL == "" {
			return fmt.Errorf("services[%d].base_url is required (service: %s)", i, svc.Name)
		}
		if svc.AuthToken == "" {
			return fmt.Errorf("services[%d].auth_token is required (service: %s)", i, svc.Name)
		}
		if svc.DefaultTopic == "" {
			return fmt.Errorf("services[%d].default_topic is required (service: %s)", i, svc.Name)
		}
	}
	if c.DB.URL == "" {
		return fmt.Errorf("db.url is required (set RELAY_DB_URL env var)")
	}
	if c.Dispatch.ConsumerGroupID == "" {
		return fmt.Errorf("dispatch.consumer_group_id is required")
	}
	if c.Dispatch.Topic == "" {
		return fmt.Errorf("dispatch.topic is required")
	}
	if c.Tracing.Enabled && c.Tracing.OTLPEndpoint == "" {
		return fmt.Errorf("tracing.otlp_endpoint is required when tracing is enabled")
	}
	// Token enclave is required whenever dispatch is active.
	if c.TokenEnclave.BaseURL == "" {
		return fmt.Errorf(
			"RELAY_TOKEN_ENCLAVE_BASE_URL is required: " +
				"dispatch flows must not proceed without a real token enclave. ",
		)
	}
	return nil
}

// RetryConfig returns resolved retry settings for a given service,
// falling back to sane production defaults.
func (s *ServiceConfig) RetryConfig() (maxAttempts int, baseDelay, maxDelay time.Duration) {
	maxAttempts = s.MaxRetryAttempts
	if maxAttempts <= 0 {
		maxAttempts = 20
	}
	baseDelay = s.RetryBaseDelay
	if baseDelay <= 0 {
		baseDelay = 1 * time.Second
	}
	maxDelay = s.RetryMaxDelay
	if maxDelay <= 0 {
		maxDelay = 5 * time.Minute
	}
	return
}
