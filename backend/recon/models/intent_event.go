package models

import (
	"encoding/json"
	"time"
)

type IntentOutboxEvent struct {
	EventID       string          `json:"event_id"`
	EventType     string          `json:"event_type"`
	TenantID      string          `json:"tenant_id"`
	TraceID       string          `json:"trace_id"`
	SchemaVersion string          `json:"schema_version"`
	CreatedAt     time.Time       `json:"created_at"`
	Payload       json.RawMessage `json:"payload"`
}

type IntentPayload struct {
	IntentID               string     `json:"intent_id"`
	TenantID               string     `json:"tenant_id"`
	ContractID             string     `json:"contract_id"`
	IntentType             string     `json:"intent_type"`
	ProviderHint           string     `json:"provider_hint,omitempty"`
	SourceSystem           string     `json:"source_system"`
	Amount                 string     `json:"amount"`
	Currency               string     `json:"currency"`
	IntendedExecutionAt    *time.Time `json:"intended_execution_at,omitempty"`
	DeadlineAt             *time.Time `json:"deadline_at,omitempty"`
	CreatedAt              time.Time  `json:"created_at"`
	ClientPayoutRef        string     `json:"client_payout_ref,omitempty"`
	ClientBatchRef         string     `json:"client_batch_ref,omitempty"`
	BusinessIdempotencyKey string     `json:"business_idempotency_key,omitempty"`
	ProofReadinessScore    float64    `json:"proof_readiness_score,omitempty"`
	MatchabilityScore      float64    `json:"matchability_score,omitempty"`
	CanonicalHash          string     `json:"canonical_hash,omitempty"`
	GovernanceState        string     `json:"governance_state,omitempty"`
	CanonicalSnapshotRef   string     `json:"canonical_snapshot_ref,omitempty"`
	SourceRowNum           *int       `json:"source_row_num,omitempty"`

	// BusinessIdempotencyKeyV1 is the legacy (amount-bearing) key, present only
	// during the intent engine's v1->v2 transition. Compare-only.
	BusinessIdempotencyKeyV1 string `json:"business_idempotency_key_v1,omitempty"`
}
