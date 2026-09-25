package models

import (
	"encoding/json"
	"time"
)

type DispatchEvent struct {
	EventID       string          `json:"event_id"`
	EventType     string          `json:"event_type"`
	TenantID      string          `json:"tenant_id"`
	IntentID      string          `json:"intent_id"`
	ContractID    string          `json:"contract_id"`
	TraceID       string          `json:"trace_id"`
	SchemaVersion string          `json:"schema_version"`
	CreatedAt     time.Time       `json:"created_at"`
	Payload       json.RawMessage `json:"payload"`
}

type DispatchCreatedPayload struct {
	DispatchID string `json:"dispatch_id"`
	// ConnectorID is the tenant's connectors.id UUID (relay resolves it).
	ConnectorID string `json:"connector_id"`
	// ConnectorRef is the connector slug (e.g. "razorpayx-v1"); display only.
	ConnectorRef string `json:"connector_ref,omitempty"`
	CorridorID   string `json:"corridor_id"`
	AttemptCount int    `json:"attempt_count"`

	CorrelationCarriers struct {
		ReferenceID string `json:"reference_id"`
		Narration   string `json:"narration"`
	} `json:"correlation_carriers"`
}

type ProviderAckedPayload struct {
	DispatchID        string `json:"dispatch_id"`
	ProviderAttemptID string `json:"provider_attempt_id"`
	Status            string `json:"status"`
}

type AttemptSentPayload struct {
	DispatchID   string `json:"dispatch_id"`
	ConnectorID  string `json:"connector_id"`
	CorridorID   string `json:"corridor_id"`
	AttemptCount int    `json:"attempt_count"`

	CorrelationCarriers struct {
		ReferenceID string `json:"reference_id"`
		Narration   string `json:"narration"`
	} `json:"correlation_carriers"`
}

// Dispatch state payloads emitted by relay (zord-relay model/dispatch_events.go).
// They update dispatch_index.status / status_reason only — dispatch state,
// never a recon verdict (D7).

type DispatchFailedPayload struct {
	DispatchID   string    `json:"dispatch_id"`
	AttemptCount int       `json:"attempt_count"`
	Reason       string    `json:"reason"`
	FailedAt     time.Time `json:"failed_at"`
}

type DispatchRetryScheduledPayload struct {
	DispatchID    string    `json:"dispatch_id"`
	RetryClass    string    `json:"retry_class"`
	NextAttemptAt time.Time `json:"next_attempt_at"`
	AttemptCount  int       `json:"attempt_count"`
	FailureReason string    `json:"failure_reason"`
}

type DispatchHeldPayload struct {
	DispatchID  string   `json:"dispatch_id"`
	Reason      string   `json:"reason"`
	ReasonCodes []string `json:"reason_codes"`
}

type DispatchGovernanceEvaluatedPayload struct {
	DispatchID  string   `json:"dispatch_id"`
	Decision    string   `json:"decision"`
	ReasonCodes []string `json:"reason_codes"`
}

type DispatchAwaitingProviderSignalPayload struct {
	DispatchID             string    `json:"dispatch_id"`
	ProviderIdempotencyKey string    `json:"provider_idempotency_key"`
	Reason                 string    `json:"reason"`
	SentAt                 time.Time `json:"sent_at"`
}
