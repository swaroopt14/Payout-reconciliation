package models

import (
	"encoding/json"
	"time"
)

type CanonicalBatch struct {
	BatchID                     string  `json:"batch_id" db:"batch_id"`
	TenantID                    string  `json:"tenant_id" db:"tenant_id"`
	SourceSystem                string  `json:"source_system" db:"source_system"`
	ReceivedCount               int     `json:"received_count" db:"received_count"`
	CanonicalizedCount          int     `json:"canonicalized_count" db:"canonicalized_count"`
	DLQCount                    int     `json:"dlq_count" db:"dlq_count"`
	ReviewCount                 int     `json:"review_count" db:"review_count"`
	LowMatchabilityCount        int     `json:"low_matchability_count" db:"low_matchability_count"`
	LowProofReadinessCount      int     `json:"low_proof_readiness_count" db:"low_proof_readiness_count"`
	DuplicateRiskCount          int     `json:"duplicate_risk_count" db:"duplicate_risk_count"`
	CanonicalizationSuccessRate float64 `json:"canonicalization_success_rate" db:"canonicalization_success_rate"`
	AvgSchemaCompletenessScore  float64 `json:"avg_schema_completeness_score" db:"avg_schema_completeness_score"`
	AvgMappingConfidenceScore   float64 `json:"avg_mapping_confidence_score" db:"avg_mapping_confidence_score"`
	AvgMatchabilityScore        float64 `json:"avg_matchability_score" db:"avg_matchability_score"`
	AvgProofReadinessScore      float64 `json:"avg_proof_readiness_score" db:"avg_proof_readiness_score"`
	AvgIntentQualityScore       float64 `json:"avg_intent_quality_score" db:"avg_intent_quality_score"`
	DuplicateRiskAmountMinor    int64   `json:"duplicate_risk_amount_minor" db:"duplicate_risk_amount_minor"`
	// TotalAmountMinor is canonical_batches.total_amount as exact int64 paise
	// (D10). Absent (nil) when the stored value is not exact paise.
	TotalAmountMinor   *int64          `json:"total_amount_minor,omitempty" db:"-"`
	BatchQualityScore  float64         `json:"batch_quality_score" db:"batch_quality_score"`
	ScoreBreakdownJSON json.RawMessage `json:"score_breakdown_json" db:"score_breakdown_json"`
	CreatedAt          time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at" db:"updated_at"`
	LeaseID            string          `json:"lease_id,omitempty" db:"lease_id"`
	LeasedBy           string          `json:"leased_by,omitempty" db:"leased_by"`
	LeaseUntil         *time.Time      `json:"lease_until,omitempty" db:"lease_until"`
	RetryCount         int             `json:"retry_count" db:"retry_count"`
	NextAttemptAt      *time.Time      `json:"next_attempt_at,omitempty" db:"next_attempt_at"`
	DispatchedAt       *time.Time      `json:"dispatched_at,omitempty" db:"dispatched_at"`

	// FileManifestHash = SHA-256(JCS_Canonicalize(canonical ordered manifest))
	// over this batch's rows. ManifestSchemaVersion/ArtifactID/
	// ArtifactVersionID/PayloadType are populated by upstream services and are
	// not derived here — they are hashed as-is, including when empty.
	FileManifestHash      string `json:"file_manifest_hash,omitempty" db:"file_manifest_hash"`
	ManifestSchemaVersion string `json:"manifest_schema_version,omitempty" db:"manifest_schema_version"`
	ArtifactID            string `json:"artifact_id,omitempty" db:"artifact_id"`
	ArtifactVersionID     string `json:"artifact_version_id,omitempty" db:"artifact_version_id"`
	PayloadType           string `json:"payload_type,omitempty" db:"payload_type"`
}
