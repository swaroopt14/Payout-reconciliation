package model

import (
	"encoding/json"
	"time"

	"github.com/shopspring/decimal"
)

// OutboxEvent is the normalized event model that works across all upstream
// service schemas. Field mapping is handled in the outbox client per service.
type OutboxEvent struct {
	// --- Identity ---
	EventID           string `json:"event_id"`    // PK from upstream outbox
	EnvelopeID        string `json:"envelope_id"` // logical grouping ID
	TraceID           string `json:"trace_id"`
	TenantID          string `json:"tenant_id"`
	ArtifactID        string `json:"artifact_id,omitempty"`
	ArtifactVersionID string `json:"artifact_version_id,omitempty"`
	ObjectRef         string `json:"object_ref"`
	Source            string `json:"source"`

	// --- Routing ---
	Topic          string `json:"topic"`           // Kafka topic; may come from outbox row or config
	EventType      string `json:"event_type"`      // e.g. intent.created.v1
	IdempotencyKey string `json:"idempotency_key"` // for consumer deduplication

	// --- Aggregate (Service 2 specifics, optional for Service 1) ---
	AggregateType string `json:"aggregate_type,omitempty"`
	AggregateID   string `json:"aggregate_id,omitempty"`
	ContractID    string `json:"contract_id,omitempty"`

	// --- Schema / versioning ---
	EventVersion  string `json:"event_version,omitempty"`
	SchemaVersion string `json:"schema_version,omitempty"`

	// --- Payload ---
	Payload     json.RawMessage `json:"payload"`
	PayloadHash string          `json:"payload_hash"`
	// CanonicalPayloadHash is SHA-256 of the exact bytes in Payload
	// (computed by Postgres as a GENERATED column on zord-intent-engine's
	// outbox table) -- distinct from PayloadHash, which is SHA-256 of the
	// raw, pre-transformation ingest payload and describes a different byte
	// sequence entirely. Payload-integrity verification must compare
	// against this field, since Payload's bytes are all this service ever
	// receives.
	CanonicalPayloadHash string  `json:"canonical_payload_hash,omitempty"`
	RawRowHash           *string `json:"raw_row_hash,omitempty"`
	EnvelopeHash         string  `json:"envelope_hash,omitempty"`
	CanonicalHash        string  `json:"canonical_hash,omitempty"`
	GovernanceState      string  `json:"governance_state,omitempty"`
	GovernanceHash       string  `json:"governance_hash,omitempty"`

	// --- Evidence leaf hashes (Service 2 / zord-intent-engine) ---
	RawRowEvidenceLeafHash       string `json:"raw_row_evidence_leaf_hash,omitempty"`
	CanonicalRowEvidenceLeafHash string `json:"canonical_row_evidence_leaf_hash,omitempty"`
	MappingProfileHash           string `json:"mapping_profile_hash,omitempty"`
	BusinessIdempotencyKey       string `json:"business_idempotency_key,omitempty"`
	TokenizedDataHash            string `json:"tokenized_data_hash,omitempty"`

	// --- Lease ---
	LeaseID    string     `json:"lease_id"`
	LeasedBy   string     `json:"leased_by"`
	LeaseUntil *time.Time `json:"lease_until,omitempty"`

	// --- Retry state ---
	RetryCount  int        `json:"retry_count"`
	NextRetryAt *time.Time `json:"next_retry_at,omitempty"`

	// --- Timestamps ---
	CreatedAt time.Time  `json:"created_at"`
	SentAt    *time.Time `json:"sent_at,omitempty"`

	// --- Status ---
	Status        string  `json:"status"`
	ClientBatchID *string `json:"batchid,omitempty"`

	FileContentHash *string `json:"file_content_hash,omitempty"`
	SourceRowRef    *string `json:"source_row_ref,omitempty"`

	DuplicateRiskFlag      bool       `json:"duplicate_risk_flag,omitempty"`
	IntentQualityScore     float64    `json:"intent_quality_score,omitempty"`
	MatchabilityScore      float64    `json:"matchability_score,omitempty"`
	ProofReadinessScore    float64    `json:"proof_readiness_score,omitempty"`
	BeneficiaryFingerprint string     `json:"beneficiary_fingerprint,omitempty"`
	IntendedExecutionAt    *time.Time `json:"intended_execution_at,omitempty"`

	RequiredFieldsStatus *bool   `json:"required_fields_status,omitempty"`
	TokenizationStatus   *bool   `json:"tokenization_status,omitempty"`
	GovernanceDecision   *string `json:"governance_decision,omitempty"`
	MappingProfileID     *string `json:"mapping_profile_used,omitempty"`

	PaymentInstructionReceived *time.Time `json:"payment_instruction_received,omitempty"`
	CanonicalIntentCreated     *time.Time `json:"canonical_intent_created,omitempty"`

	ClientPayoutRef *string         `json:"client_payout_ref,omitempty"`
	SourceRowNum    *int            `json:"source_row_num,omitempty"`
	Amount          decimal.Decimal `json:"amount,omitempty"`
	Currency        string          `json:"currency,omitempty"`

	// 🆕 Settlement Metadata
	SettlementRecordReceived   *time.Time `json:"settlement_record_received,omitempty"`
	CanonicalSettlementCreated *time.Time `json:"canonical_settlement_created,omitempty"`
	BankID                     *string    `json:"bank_id,omitempty"`
	SourceSystem               *string    `json:"source_system,omitempty"`
	CorridorID                 *string    `json:"corridor_id,omitempty"`
	BankReference              *string    `json:"bank_reference,omitempty"`
	ClientReference            *string    `json:"client_reference,omitempty"`
	AttachmentDecision         *string    `json:"attachment_decision,omitempty"`
	MatchConfidence            *float64   `json:"match_confidence,omitempty"`
	ValueDateCheck             *bool      `json:"value_date_check,omitempty"`
	AmountMatch                *bool      `json:"amount_match,omitempty"`
}

// LeaseResponse is what the upstream /lease endpoint returns.
type LeaseResponse struct {
	LeaseID    string        `json:"lease_id"`
	LeaseUntil *time.Time    `json:"lease_until,omitempty"`
	Events     []OutboxEvent `json:"events"`
}

// AckRequest is sent to /ack.
type AckRequest struct {
	LeaseID  string   `json:"lease_id"`
	EventIDs []string `json:"event_ids"`
}

// NackRequest is sent to /nack.
// FailureReason is stored for observability; upstream uses it to set failure_reason_code.
type NackRequest struct {
	LeaseID       string   `json:"lease_id"`
	EventIDs      []string `json:"event_ids"`
	FailureReason string   `json:"failure_reason,omitempty"`
}

// AckNackResponse is returned by /ack and /nack.
type AckNackResponse struct {
	Updated int64 `json:"updated"`
}

// DLQMessage is published to the DLQ Kafka topics.
// Both publish-failure DLQ and poison-event DLQ use this envelope.
type DLQMessage struct {
	// Original event — may be nil for extreme corruption cases.
	// Exactly one of Event / EdgeEvent / IntentEvent is set, depending on
	// which worker produced this failure.
	Event       *OutboxEvent       `json:"event,omitempty"`
	EdgeEvent   *EdgeOutboxEvent   `json:"edge_event,omitempty"`
	IntentEvent *IntentOutboxEvent `json:"intent_event,omitempty"`

	// EventID is always set (regardless of which of the above is populated)
	// so PublishDLQ can key the Kafka message without depending on event shape.
	EventID string `json:"event_id,omitempty"`

	// Error details.
	Error       string `json:"error"`
	ReasonCode  string `json:"reason_code"` // see ReasonCode* constants
	ServiceName string `json:"service_name"`

	// Retry metadata.
	AttemptsCount  int       `json:"attempts_count"`
	LastAttemptAt  time.Time `json:"last_attempt_at"`
	FirstAttemptAt time.Time `json:"first_attempt_at"`

	// Relay instance that gave up.
	RelayInstanceID string `json:"relay_instance_id"`
}

// ReasonCode constants — used in DLQMessage.ReasonCode.
const (
	// Publish failure DLQ reason codes.
	ReasonCodeKafkaTimeout     = "KAFKA_TIMEOUT"
	ReasonCodeKafkaBrokerError = "KAFKA_BROKER_ERROR"
	ReasonCodeKafkaMaxRetries  = "KAFKA_MAX_RETRIES_EXCEEDED"

	// Poison event DLQ reason codes.
	ReasonCodeInvalidPayload       = "INVALID_PAYLOAD"
	ReasonCodeSchemaViolation      = "SCHEMA_VIOLATION"
	ReasonCodeMessageTooLarge      = "MESSAGE_TOO_LARGE"
	ReasonCodeMissingRequiredField = "MISSING_REQUIRED_FIELD"

	// ReasonCodePayloadHashMismatch is used when SHA-256(payload) does not
	// match the upstream payload_hash on the Kafka relay path.
	// Events carrying this code have NOT been forwarded to any consumer topic.
	ReasonCodePayloadHashMismatch = "PAYLOAD_HASH_MISMATCH"

	// ReasonCodeUnknownRoute is used when an event does not match the
	// exact allow-list configuration for routing.
	ReasonCodeUnknownRoute = "UNKNOWN_EVENT_ROUTE"
)

// PublishResult carries the outcome of a single Kafka publish attempt.
type PublishResult struct {
	EventID  string
	Err      error
	IsPoison bool // true = don't retry, go straight to poison DLQ
}

type DLQItemEvent struct {
	DLQID          string          `json:"dlq_id"`
	TenantID       string          `json:"tenant_id"`
	EnvelopeID     string          `json:"envelope_id"`
	Stage          string          `json:"stage"`
	ReasonCode     string          `json:"reason_code"`
	ErrorDetail    string          `json:"error_detail"`
	DLQStatus      string          `json:"dlq_status"`
	Replayable     bool            `json:"replayable"`
	ClientBatchRef string          `json:"client_batch_ref"`
	BatchID        string          `json:"batch_id,omitempty"`
	SourceRowNum   *int            `json:"source_row_num,omitempty"`
	IntentContext  json.RawMessage `json:"intent_context,omitempty"`
	TraceID        string          `json:"trace_id,omitempty"`

	// EventType / EventVersion / SchemaVersion / SourceService are the
	// standard cross-service event envelope fields, stamped by
	// zord-intent-engine's DLQ lease handler. PublishDLQItem below copies
	// them into the flat Kafka payload it publishes on payments.intent.dlq.
	EventType     string `json:"event_type,omitempty"`
	EventVersion  string `json:"event_version,omitempty"`
	SchemaVersion string `json:"schema_version,omitempty"`
	SourceService string `json:"source_service,omitempty"`

	LeaseID       string     `json:"lease_id,omitempty"`
	LeasedBy      string     `json:"leased_by,omitempty"`
	LeaseUntil    *time.Time `json:"lease_until,omitempty"`
	RetryCount    int        `json:"retry_count"`
	NextAttemptAt *time.Time `json:"next_attempt_at,omitempty"`
	DispatchedAt  *time.Time `json:"dispatched_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

type DLQLeaseResponse struct {
	LeaseID    string         `json:"lease_id"`
	LeaseUntil *time.Time     `json:"lease_until,omitempty"`
	Events     []DLQItemEvent `json:"events"`
}

type BatchCanonicalizationCompletedEvent struct {
	TenantID                    string          `json:"tenant_id"`
	BatchID                     string          `json:"batch_id"`
	SourceSystem                string          `json:"source_system,omitempty"`
	ReceivedCount               int             `json:"received_count"`
	CanonicalizedCount          int             `json:"canonicalized_count"`
	DLQCount                    int             `json:"dlq_count"`
	ReviewCount                 int             `json:"review_count"`
	LowMatchabilityCount        int             `json:"low_matchability_count"`
	LowProofReadinessCount      int             `json:"low_proof_readiness_count"`
	DuplicateRiskCount          int             `json:"duplicate_risk_count"`
	CanonicalizationSuccessRate float64         `json:"canonicalization_success_rate"`
	AvgSchemaCompletenessScore  float64         `json:"avg_schema_completeness_score"`
	AvgMappingConfidenceScore   float64         `json:"avg_mapping_confidence_score"`
	AvgMatchabilityScore        float64         `json:"avg_matchability_score"`
	AvgProofReadinessScore      float64         `json:"avg_proof_readiness_score"`
	AvgIntentQualityScore       float64         `json:"avg_intent_quality_score"`
	DuplicateRiskAmountMinor    int64           `json:"duplicate_risk_amount_minor"`
	BatchQualityScore           float64         `json:"batch_quality_score"`
	ScoreBreakdownJSON          json.RawMessage `json:"score_breakdown_json,omitempty"`
	// Deprecated: TotalAmount is a float and must never be read for a money
	// decision (D10). The intent-engine lease does not populate it (it is
	// always 0); it is kept only so the published wire shape does not change
	// silently for any external reader. Use TotalAmountMinor.
	TotalAmount float64 `json:"total_amount"`
	// TotalAmountMinor is the batch total in exact int64 paise, produced by
	// the intent engine from canonical_batches.total_amount. Nil = unknown.
	TotalAmountMinor *int64     `json:"total_amount_minor,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	LeaseID          string     `json:"lease_id,omitempty"`
	LeasedBy         string     `json:"leased_by,omitempty"`
	LeaseUntil       *time.Time `json:"lease_until,omitempty"`
	RetryCount       int        `json:"retry_count"`
	NextAttemptAt    *time.Time `json:"next_attempt_at,omitempty"`
	DispatchedAt     *time.Time `json:"dispatched_at,omitempty"`
}

type BatchLeaseResponse struct {
	LeaseID    string                                `json:"lease_id"`
	LeaseUntil *time.Time                            `json:"lease_until,omitempty"`
	Events     []BatchCanonicalizationCompletedEvent `json:"events"`
}

const (
	VectorIndexRequestTopic = "zord.vector.index.request.v1"

	VectorIndexEventRequested = "vector.index.requested"

	VectorIndexOperationUpsert = "upsert"
	VectorIndexOperationDelete = "delete"
)

// VectorIndexRequestEvent is the lightweight Kafka contract consumed later by
// zord-prompt-layer's vector index worker. It carries only pointers, never raw
// sensitive payloads.
type VectorIndexRequestEvent struct {
	EventID         string            `json:"event_id"`
	SchemaVersion   string            `json:"schema_version"`
	EventType       string            `json:"event_type"`
	SourceService   string            `json:"source_service"`
	SourceEventType string            `json:"source_event_type"`
	TenantID        string            `json:"tenant_id"`
	EntityType      string            `json:"entity_type"`
	EntityID        string            `json:"entity_id"`
	BatchID         string            `json:"batch_id,omitempty"`
	Operation       string            `json:"operation"`
	OccurredAt      time.Time         `json:"occurred_at"`
	ContentVersion  string            `json:"content_version,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}
