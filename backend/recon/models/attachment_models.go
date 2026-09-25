package models

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// ─────────────────────────────────────────────────────────────────────────────
// SERVICE 5C — ATTACHMENT, VARIANCE & AMBIGUITY ENGINE
// Intent-to-Settlement Attachment Truth Models
// ─────────────────────────────────────────────────────────────────────────────

// ─── Decision type constants ──────────────────────────────────────────────────

const (
	DecisionMatchExact          = "MATCH_EXACT"
	DecisionMatchHighConfidence = "MATCH_HIGH_CONFIDENCE"
	DecisionMatchAmbiguous      = "MATCH_AMBIGUOUS"
	DecisionMatchUnresolved     = "MATCH_UNRESOLVED"
	DecisionMatchConflicted     = "MATCH_CONFLICTED"
)

// ─── Confidence bucket constants ──────────────────────────────────────────────

const (
	ConfidenceExact   = "EXACT"
	ConfidenceHigh    = "HIGH"
	ConfidenceMedium  = "MEDIUM"
	ConfidenceLow     = "LOW"
	ConfidenceInvalid = "INVALID"
)

// ─── Variance severity constants ─────────────────────────────────────────────

const (
	VarianceSeverityInfo     = "INFO"
	VarianceSeverityLow      = "LOW"
	VarianceSeverityMedium   = "MEDIUM"
	VarianceSeverityHigh     = "HIGH"
	VarianceSeverityCritical = "CRITICAL"
)

// ─── Variance type constants (PDF review section 9) ──────────────────────────

const (
	VarianceTypeNoVariance        = "NO_VARIANCE"
	VarianceTypeUnderSettlement   = "UNDER_SETTLEMENT"
	VarianceTypeOverSettlement    = "OVER_SETTLEMENT"
	VarianceTypeFeeDeduction      = "FEE_DEDUCTION"
	VarianceTypeTaxTDSDeduction   = "TAX_TDS_DEDUCTION"
	VarianceTypeRounding          = "ROUNDING"
	VarianceTypeStatusMismatch    = "STATUS_MISMATCH"
	VarianceTypeValueDateMismatch = "VALUE_DATE_MISMATCH"
	VarianceTypeCrossPeriod       = "CROSS_PERIOD"
)

// ─── Batch attachment status constants ───────────────────────────────────────

const (
	BatchStatusProcessing       = "PROCESSING"
	BatchStatusFullySettled     = "FULLY_RECONCILED"
	BatchStatusPartiallySettled = "PARTIALLY_RECONCILED"
	BatchStatusFailed           = "FAILED"
	BatchStatusRequiresReview   = "REQUIRES_REVIEW"
	BatchStatusClosed           = "CLOSED"
)

// NormalizeBatchAttachmentStatus maps legacy settlement labels stored in older
// batch_attachment_summaries rows to canonical reconciliation vocabulary.
func NormalizeBatchAttachmentStatus(status string) string {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "", "PENDING", "OPEN":
		return BatchStatusProcessing
	case "FULLY_SETTLED", "SETTLED", BatchStatusFullySettled:
		return BatchStatusFullySettled
	case "PARTIALLY_SETTLED", BatchStatusPartiallySettled:
		return BatchStatusPartiallySettled
	case BatchStatusFailed, "CANCELLED":
		return BatchStatusFailed
	case BatchStatusRequiresReview:
		return BatchStatusRequiresReview
	case BatchStatusClosed:
		return BatchStatusClosed
	case BatchStatusProcessing:
		return BatchStatusProcessing
	default:
		return BatchStatusProcessing
	}
}

// ─── Job scope constants ──────────────────────────────────────────────────────

const (
	JobScopeSettlementBatch = "SETTLEMENT_BATCH"
	JobScopeSingleIntent    = "SINGLE_INTENT"
	JobScopeIngestRun       = "INGEST_RUN"
	JobScopeReplay          = "REPLAY"
	JobScopeBackfill        = "BACKFILL"
)

// ─── Unresolved intent reason code constants (PDF review section 10) ─────────

const (
	UnresolvedReasonNoSettlementObservationFound  = "NO_SETTLEMENT_OBSERVATION_FOUND"
	UnresolvedReasonOnlyAmbiguousCandidatesFound  = "ONLY_AMBIGUOUS_CANDIDATES_FOUND"
	UnresolvedReasonOnlyConflictedCandidatesFound = "ONLY_CONFLICTED_CANDIDATES_FOUND"
	UnresolvedReasonSourceFileNotReceived         = "SOURCE_FILE_NOT_RECEIVED"
)

// ─────────────────────────────────────────────────────────────────────────────
// CANONICAL INTENT — minimal projection consumed from Service 2 output.
// In a full system this would be fetched from the intent service. Here we model
// exactly the fields that the attachment engine needs.
// ─────────────────────────────────────────────────────────────────────────────

type CanonicalIntent struct {
	IntentID               uuid.UUID       `json:"intent_id" db:"intent_id"`
	TenantID               uuid.UUID       `json:"tenant_id" db:"tenant_id"`
	TraceID                *uuid.UUID      `json:"trace_id,omitempty" db:"trace_id"`
	ContractID             uuid.UUID       `json:"contract_id" db:"contract_id"`
	ClientPayoutRef        *string         `json:"client_payout_ref,omitempty" db:"client_payout_ref"`
	ClientBatchRef         *string         `json:"client_batch_ref,omitempty" db:"client_batch_ref"`
	BusinessIdempotencyKey *string         `json:"business_idempotency_key,omitempty" db:"business_idempotency_key"`
	Amount                 decimal.Decimal `json:"amount" db:"amount"`
	CurrencyCode           string          `json:"currency_code" db:"currency_code"`
	IntendedExecutionAt    *time.Time      `json:"intended_execution_at,omitempty" db:"intended_execution_at"`
	PayoutType             *string         `json:"payout_type,omitempty" db:"payout_type"`
	ProviderHint           *string         `json:"provider_hint,omitempty" db:"provider_hint"`
	Corridor               *string         `json:"corridor,omitempty" db:"corridor"`
	ProofReadinessScore    float64         `json:"proof_readiness_score" db:"proof_readiness_score"`
	MatchabilityScore      float64         `json:"matchability_score" db:"matchability_score"`
	CanonicalHash          string          `json:"canonical_hash" db:"canonical_hash"`
	GovernanceState        string          `json:"governance_state" db:"governance_state"`
	ZordSignatureCarrier   *string         `json:"zord_signature_carrier,omitempty" db:"zord_signature_carrier"`
	BeneficiaryFingerprint *string         `json:"beneficiary_fingerprint,omitempty" db:"beneficiary_fingerprint"`
	SourceRowNum           *int            `json:"source_row_num,omitempty" db:"source_row_num"`
	CreatedAt              time.Time       `json:"created_at" db:"created_at"`

	// BusinessIdempotencyKeyV1 is the legacy key the intent engine carries on
	// the event during the v1->v2 key transition. Compare-only; never stored.
	BusinessIdempotencyKeyV1 *string `json:"-" db:"-"`
}

// ─────────────────────────────────────────────────────────────────────────────
// ATTACHMENT JOB — tracks a single attachment processing cycle.
// ─────────────────────────────────────────────────────────────────────────────

type AttachmentJob struct {
	AttachmentJobID        uuid.UUID  `json:"attachment_job_id" db:"attachment_job_id"`
	TenantID               uuid.UUID  `json:"tenant_id" db:"tenant_id"`
	JobScopeType           string     `json:"job_scope_type" db:"job_scope_type"`
	ScopeRef               string     `json:"scope_ref" db:"scope_ref"`
	MatchingRulesetVersion string     `json:"matching_ruleset_version" db:"matching_ruleset_version"`
	Status                 string     `json:"status" db:"status"`
	CandidateCountTotal    int        `json:"candidate_count_total" db:"candidate_count_total"`
	ExactMatchCount        int        `json:"exact_match_count" db:"exact_match_count"`
	HighConfidenceCount    int        `json:"high_confidence_count" db:"high_confidence_count"`
	AmbiguousCount         int        `json:"ambiguous_count" db:"ambiguous_count"`
	UnresolvedCount        int        `json:"unresolved_count" db:"unresolved_count"`
	ConflictedCount        int        `json:"conflicted_count" db:"conflicted_count"`
	StartedAt              *time.Time `json:"started_at,omitempty" db:"started_at"`
	CompletedAt            *time.Time `json:"completed_at,omitempty" db:"completed_at"`
	CreatedAt              time.Time  `json:"created_at" db:"created_at"`
}

// ─────────────────────────────────────────────────────────────────────────────
// ATTACHMENT CANDIDATE — scored candidate before final decision is committed.
// Preserving the full candidate set is mandatory for replay and RCA.
// ─────────────────────────────────────────────────────────────────────────────

type AttachmentCandidate struct {
	CandidateID             uuid.UUID `json:"candidate_id" db:"candidate_id"`
	AttachmentJobID         uuid.UUID `json:"attachment_job_id" db:"attachment_job_id"`
	TenantID                uuid.UUID `json:"tenant_id" db:"tenant_id"`
	SettlementObservationID uuid.UUID `json:"settlement_observation_id" db:"settlement_observation_id"`
	IntentID                uuid.UUID `json:"intent_id" db:"intent_id"`
	CandidateRank           int       `json:"candidate_rank" db:"candidate_rank"`

	// Per-carrier match flags
	ExactRefMatchFlag      bool `json:"exact_ref_match_flag" db:"exact_ref_match_flag"`
	ClientRefMatchFlag     bool `json:"client_ref_match_flag" db:"client_ref_match_flag"`
	ProviderRefMatchFlag   bool `json:"provider_ref_match_flag" db:"provider_ref_match_flag"`
	BankRefMatchFlag       bool `json:"bank_ref_match_flag" db:"bank_ref_match_flag"`
	BatchMatchFlag         bool `json:"batch_match_flag" db:"batch_match_flag"`
	AmountMatchFlag        bool `json:"amount_match_flag" db:"amount_match_flag"`
	CurrencyMatchFlag      bool `json:"currency_match_flag" db:"currency_match_flag"`
	TimeWindowMatchFlag    bool `json:"time_window_match_flag" db:"time_window_match_flag"`
	SourceSystemMatchFlag  bool `json:"source_system_match_flag" db:"source_system_match_flag"`
	ZordSignatureMatchFlag bool `json:"zord_signature_match_flag" db:"zord_signature_match_flag"`
	CompositeMatchFlag     bool `json:"composite_match_flag" db:"composite_match_flag"`

	// Scoring
	ScoreTotal         float64         `json:"score_total" db:"score_total"`
	ScoreBreakdownJSON json.RawMessage `json:"score_breakdown_json" db:"score_breakdown_json"`
	ConfidenceBucket   string          `json:"confidence_bucket" db:"confidence_bucket"`
	CreatedAt          time.Time       `json:"created_at" db:"created_at"`
}

// ─────────────────────────────────────────────────────────────────────────────
// ATTACHMENT DECISION — the formal output of the attachment engine.
// This is the primary truth artifact that Service 6 and Service 7 consume.
// ─────────────────────────────────────────────────────────────────────────────

type AttachmentDecision struct {
	AttachmentDecisionID     uuid.UUID       `json:"attachment_decision_id" db:"attachment_decision_id"`
	TenantID                 uuid.UUID       `json:"tenant_id" db:"tenant_id"`
	SettlementObservationID  *uuid.UUID      `json:"settlement_observation_id,omitempty" db:"settlement_observation_id"`
	IntentID                 uuid.UUID       `json:"intent_id" db:"intent_id"`
	AttachmentJobID          uuid.UUID       `json:"attachment_job_id" db:"attachment_job_id"`
	DecisionType             string          `json:"decision_type" db:"decision_type"`
	DecisionReasonCode       string          `json:"decision_reason_code" db:"decision_reason_code"`
	DecisionReasonDetailJSON json.RawMessage `json:"decision_reason_detail_json" db:"decision_reason_detail_json"`
	MatchingRulesetVersion   string          `json:"matching_ruleset_version" db:"matching_ruleset_version"`
	WinningScore             float64         `json:"winning_score" db:"winning_score"`
	RunnerUpScore            *float64        `json:"runner_up_score,omitempty" db:"runner_up_score"`
	ScoreMargin              *float64        `json:"score_margin,omitempty" db:"score_margin"`
	RelativeScoreMargin      *float64        `json:"relative_score_margin,omitempty" db:"relative_score_margin"`
	ConfidenceScore          float64         `json:"confidence_score" db:"confidence_score"`
	MatchConfidence          float64         `json:"match_confidence" db:"match_confidence"`
	AmbiguityScore           float64         `json:"ambiguity_score" db:"avg_matched_attachment_ambiguity"`
	SupportingCarriersJSON   json.RawMessage `json:"supporting_carriers_json" db:"supporting_carriers_json"`
	CandidateSetHash         string          `json:"candidate_set_hash" db:"candidate_set_hash"`
	CandidateSetSnapshotRef  string          `json:"candidate_set_snapshot_ref,omitempty" db:"candidate_set_snapshot_ref"`
	CandidateSetSize         int             `json:"candidate_set_size" db:"candidate_set_size"`
	CreatedAt                time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt                time.Time       `json:"updated_at" db:"updated_at"`
	Currency                 string          `json:"currency" db:"currency"`
}

// ─────────────────────────────────────────────────────────────────────────────
// VARIANCE RECORD — formalizes what differs between intent and observation.
// Value-date mismatch and cross-period flags are first-class fields per spec.
// PDF review (section 9) adds: VarianceType and whitelist fields.
// ─────────────────────────────────────────────────────────────────────────────

type VarianceRecord struct {
	VarianceRecordID        uuid.UUID `json:"variance_record_id" db:"variance_record_id"`
	TenantID                uuid.UUID `json:"tenant_id" db:"tenant_id"`
	AttachmentDecisionID    uuid.UUID `json:"attachment_decision_id" db:"attachment_decision_id"`
	IntentID                uuid.UUID `json:"intent_id" db:"intent_id"`
	SettlementObservationID uuid.UUID `json:"settlement_observation_id" db:"settlement_observation_id"`

	// Amount deltas
	AmountVariance    decimal.Decimal  `json:"amount_variance" db:"amount_variance"`
	DeductionVariance *decimal.Decimal `json:"deduction_variance,omitempty" db:"deduction_variance"`
	FeeVariance       *decimal.Decimal `json:"fee_variance,omitempty" db:"fee_variance"`

	// Status & timing variance flags
	CurrencyMatchFlag     bool `json:"currency_match_flag" db:"currency_match_flag"`
	StatusVarianceFlag    bool `json:"status_variance_flag" db:"status_variance_flag"`
	ValueDateMismatchFlag bool `json:"value_date_mismatch_flag" db:"value_date_mismatch_flag"`
	SettlementDelayDays   int  `json:"settlement_delay_days" db:"settlement_delay_days"`
	CrossPeriodFlag       bool `json:"cross_period_flag" db:"cross_period_flag"`

	// Evidence quality flags
	ProviderRefMissingFlag bool `json:"provider_ref_missing_flag" db:"provider_ref_missing_flag"`
	BankRefMissingFlag     bool `json:"bank_ref_missing_flag" db:"bank_ref_missing_flag"`
	EvidenceGapFlag        bool `json:"evidence_gap_flag" db:"evidence_gap_flag"`

	// Variance classification (PDF review section 9)
	VarianceType string `json:"variance_type" db:"variance_type"`

	// Severity & classification
	VarianceSeverity        string          `json:"variance_severity" db:"variance_severity"`
	VarianceReasonCodesJSON json.RawMessage `json:"variance_reason_codes_json" db:"variance_reason_codes_json"`

	// Whitelist fields (PDF review section 8 & 9)
	// Service 7 must not count expected PSP fees, TDS, commissions, rounding,
	// or policy-approved deductions as leakage. Variance can exist without being leakage.
	IsWhitelisted          bool    `json:"is_whitelisted" db:"is_whitelisted"`
	WhitelistPolicyID      *string `json:"whitelist_policy_id,omitempty" db:"whitelist_policy_id"`
	WhitelistPolicyVersion *string `json:"whitelist_policy_version,omitempty" db:"whitelist_policy_version"`
	WhitelistReasonCode    *string `json:"whitelist_reason_code,omitempty" db:"whitelist_reason_code"`
	WhitelistExplanation   *string `json:"whitelist_explanation,omitempty" db:"whitelist_explanation"`

	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

// ─────────────────────────────────────────────────────────────────────────────
// UNRESOLVED INTENT RECORD — reverse scan output (PDF review section 10).
//
// Records every canonical intent for which no acceptable settlement observation
// was found within the expected attachment window.  This is the only mechanism
// that can prove "every dollar intended to be paid was accounted for."
// ─────────────────────────────────────────────────────────────────────────────

type UnresolvedIntentRecord struct {
	UnresolvedID      uuid.UUID       `json:"unresolved_id" db:"unresolved_id"`
	TenantID          uuid.UUID       `json:"tenant_id" db:"tenant_id"`
	AttachmentJobID   uuid.UUID       `json:"attachment_job_id" db:"attachment_job_id"`
	IntentID          uuid.UUID       `json:"intent_id" db:"intent_id"`
	BatchID           *string         `json:"batch_id,omitempty" db:"batch_id"`
	ExpectedWindowEnd *time.Time      `json:"expected_window_end,omitempty" db:"expected_window_end"`
	ReasonCode        string          `json:"reason_code" db:"reason_code"`
	Amount            decimal.Decimal `json:"amount" db:"amount"`
	CurrencyCode      string          `json:"currency_code" db:"currency_code"`
	CreatedAt         time.Time       `json:"created_at" db:"created_at"`
}

// ─────────────────────────────────────────────────────────────────────────────
// AMBIGUOUS INTENT RECORD — intents with only ambiguous attachment candidates.
// ─────────────────────────────────────────────────────────────────────────────

type AmbiguousIntentRecord struct {
	AmbiguousID       uuid.UUID       `json:"ambiguous_id" db:"ambiguous_id"`
	TenantID          uuid.UUID       `json:"tenant_id" db:"tenant_id"`
	AttachmentJobID   uuid.UUID       `json:"attachment_job_id" db:"attachment_job_id"`
	IntentID          uuid.UUID       `json:"intent_id" db:"intent_id"`
	BatchID           *string         `json:"batch_id,omitempty" db:"batch_id"`
	ExpectedWindowEnd *time.Time      `json:"expected_window_end,omitempty" db:"expected_window_end"`
	ReasonCode        string          `json:"reason_code" db:"reason_code"`
	Amount            decimal.Decimal `json:"amount" db:"amount"`
	CurrencyCode      string          `json:"currency_code" db:"currency_code"`
	CreatedAt         time.Time       `json:"created_at" db:"created_at"`
}

// ─────────────────────────────────────────────────────────────────────────────
// CONFLICTED INTENT RECORD — intents with only conflicted attachment candidates.
// ─────────────────────────────────────────────────────────────────────────────

type ConflictedIntentRecord struct {
	ConflictedID      uuid.UUID       `json:"conflicted_id" db:"conflicted_id"`
	TenantID          uuid.UUID       `json:"tenant_id" db:"tenant_id"`
	AttachmentJobID   uuid.UUID       `json:"attachment_job_id" db:"attachment_job_id"`
	IntentID          uuid.UUID       `json:"intent_id" db:"intent_id"`
	BatchID           *string         `json:"batch_id,omitempty" db:"batch_id"`
	ExpectedWindowEnd *time.Time      `json:"expected_window_end,omitempty" db:"expected_window_end"`
	ReasonCode        string          `json:"reason_code" db:"reason_code"`
	Amount            decimal.Decimal `json:"amount" db:"amount"`
	CurrencyCode      string          `json:"currency_code" db:"currency_code"`
	CreatedAt         time.Time       `json:"created_at" db:"created_at"`
}

// ─────────────────────────────────────────────────────────────────────────────
// ORPHAN SETTLEMENT RECORD — reverse scan output.
//
// Records every canonical settlement observation that was not matched during an
// attachment job.
// ─────────────────────────────────────────────────────────────────────────────

type OrphanSettlementRecord struct {
	OrphanID                uuid.UUID       `json:"orphan_id" db:"orphan_id"`
	TenantID                uuid.UUID       `json:"tenant_id" db:"tenant_id"`
	AttachmentJobID         uuid.UUID       `json:"attachment_job_id" db:"attachment_job_id"`
	SettlementObservationID uuid.UUID       `json:"settlement_observation_id" db:"settlement_observation_id"`
	BatchID                 *string         `json:"batch_id,omitempty" db:"batch_id"`
	UnresolvedReason        string          `json:"unresolved_reason" db:"unresolved_reason"`
	Amount                  decimal.Decimal `json:"amount" db:"amount"`
	CurrencyCode            string          `json:"currency_code" db:"currency_code"`
	CreatedAt               time.Time       `json:"created_at" db:"created_at"`
}

// ─────────────────────────────────────────────────────────────────────────────
// BATCH ATTACHMENT SUMMARY — derived batch-level attachment picture.
// ─────────────────────────────────────────────────────────────────────────────

type BatchAttachmentSummary struct {
	BatchAttachmentSummaryID uuid.UUID `json:"batch_attachment_summary_id" db:"batch_attachment_summary_id"`
	TenantID                 uuid.UUID `json:"tenant_id" db:"tenant_id"`
	BatchID                  *string   `json:"batch_id,omitempty" db:"batch_id"`
	SourceReference          string    `json:"source_reference" db:"source_reference"`
	AttachmentJobID          uuid.UUID `json:"attachment_job_id" db:"attachment_job_id"`

	// Counts
	TotalIntentCount        int `json:"total_intent_count" db:"total_intent_count"`
	TotalObservationCount   int `json:"total_observation_count" db:"total_observation_count"`
	MatchedIntentCount      int `json:"matched_intent_count" db:"matched_intent_count"`
	MatchedObservationCount int `json:"matched_observation_count" db:"matched_observation_count"`
	ExactMatchCount         int `json:"exact_match_count" db:"exact_match_count"`
	HighConfidenceCount     int `json:"high_confidence_count" db:"high_confidence_count"`
	AmbiguousCount          int `json:"ambiguous_count" db:"ambiguous_count"`
	UnresolvedCount         int `json:"unresolved_count" db:"unresolved_count"`
	ConflictedCount         int `json:"conflicted_count" db:"conflicted_count"`
	OrphanObservationCount  int `json:"orphan_observation_count" db:"orphan_observation_count"`

	// Amount aggregates
	TotalIntendedAmount      decimal.Decimal `json:"total_intended_amount" db:"total_intended_amount"`
	OriginalIntendedAmount   decimal.Decimal `json:"original_intended_amount" db:"original_intended_amount"`
	MatchedIntendedAmount    decimal.Decimal `json:"matched_intended_amount" db:"matched_intended_amount"`
	UnresolvedIntendedAmount decimal.Decimal `json:"unresolved_intended_amount" db:"unresolved_intended_amount"`
	AmbiguousAmount          decimal.Decimal `json:"ambiguous_amount" db:"ambiguous_amount"`
	ConflictedAmount         decimal.Decimal `json:"conflicted_amount" db:"conflicted_amount"`
	TotalObservedAmount      decimal.Decimal `json:"total_observed_amount" db:"total_observed_amount"`
	OriginalSettledAmount    decimal.Decimal `json:"original_settled_amount" db:"original_settled_amount"`
	MatchedObservedAmount    decimal.Decimal `json:"matched_observed_amount" db:"matched_observed_amount"`
	OrphanObservedAmount     decimal.Decimal `json:"orphan_observed_amount" db:"orphan_observed_amount"`
	AmbiguousObservedAmount  decimal.Decimal `json:"ambiguous_observed_amount" db:"ambiguous_observed_amount"`
	ConflictedObservedAmount decimal.Decimal `json:"conflicted_observed_amount" db:"conflicted_observed_amount"`
	UnresolvedObservedAmount decimal.Decimal `json:"unresolved_observed_amount" db:"unresolved_observed_amount"`
	TotalFeeAmount           decimal.Decimal `json:"total_fee_amount" db:"total_fee_amount"`
	TotalDeductionAmount     decimal.Decimal `json:"total_deduction_amount" db:"total_deduction_amount"`
	TotalVariance            decimal.Decimal `json:"total_variance" db:"total_variance"`
	MatchedPairVariance      decimal.Decimal `json:"matched_pair_variance" db:"matched_pair_variance"`
	NetBatchDelta            decimal.Decimal `json:"net_batch_delta" db:"net_batch_delta"`
	NetUnexplainedVariance   decimal.Decimal `json:"net_unexplained_variance" db:"net_unexplained_variance"`

	// Coverage (0–1 ratios)
	IntentCountCoverage             float64 `json:"intent_count_coverage" db:"intent_count_coverage"`
	IntentValueCoverage             float64 `json:"intent_value_coverage" db:"intent_value_coverage"`
	ObservedCountAllocationCoverage float64 `json:"observed_count_allocation_coverage" db:"observed_count_allocation_coverage"`
	ObservedValueAllocationCoverage float64 `json:"observed_value_allocation_coverage" db:"observed_value_allocation_coverage"`

	// Derived status
	BatchAttachmentStatus    string    `json:"batch_attachment_status" db:"batch_attachment_status"`
	AggregateScore           float64   `json:"avg_matched_attachment_quality" db:"avg_matched_attachment_quality"`
	AggregateMatchConfidence float64   `json:"avg_matched_attachment_confidence" db:"avg_matched_attachment_confidence"`
	AmbiguityScore           float64   `json:"avg_matched_attachment_ambiguity" db:"avg_matched_attachment_ambiguity"`
	CreatedAt                time.Time `json:"created_at" db:"created_at"`
	UpdatedAt                time.Time `json:"updated_at" db:"updated_at"`
}

// ─────────────────────────────────────────────────────────────────────────────
// ATTACHMENT RULE PROFILE — tenant-scoped matching configuration.
// Keeps the engine deterministic while allowing per-tenant tuning.
// ─────────────────────────────────────────────────────────────────────────────

type AttachmentRuleProfile struct {
	ProfileID                  string          `json:"profile_id" db:"profile_id"`
	TenantID                   uuid.UUID       `json:"tenant_id" db:"tenant_id"`
	Version                    string          `json:"version" db:"version"`
	ExactRefPriorityJSON       json.RawMessage `json:"exact_ref_priority_json" db:"exact_ref_priority_json"`
	CarrierPriorityJSON        json.RawMessage `json:"carrier_priority_json" db:"carrier_priority_json"`
	TimeWindowPolicyJSON       json.RawMessage `json:"time_window_policy_json" db:"time_window_policy_json"`
	AmountTolerancePolicyJSON  json.RawMessage `json:"amount_tolerance_policy_json" db:"amount_tolerance_policy_json"`
	BatchBoundaryPolicyJSON    json.RawMessage `json:"batch_boundary_policy_json" db:"batch_boundary_policy_json"`
	ManualReviewThresholdsJSON json.RawMessage `json:"manual_review_thresholds_json" db:"manual_review_thresholds_json"`
	AmbiguityMarginThreshold   float64         `json:"ambiguity_margin_threshold" db:"ambiguity_margin_threshold"`
	RequiresBankRefForExact    bool            `json:"requires_bank_ref_for_exact_flag" db:"requires_bank_ref_for_exact_flag"`
	Status                     string          `json:"status" db:"status"`
	CreatedAt                  time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt                  time.Time       `json:"updated_at" db:"updated_at"`
}

// ─────────────────────────────────────────────────────────────────────────────
// ATTACHMENT OUTBOX EVENT — durable downstream handoff for 5C outputs.
// ─────────────────────────────────────────────────────────────────────────────

type AttachmentOutboxEvent struct {
	OutboxEventID   uuid.UUID       `json:"outbox_event_id" db:"outbox_event_id"`
	TenantID        uuid.UUID       `json:"tenant_id" db:"tenant_id"`
	TraceID         *uuid.UUID      `json:"trace_id" db:"trace_id"`
	AttachmentJobID uuid.UUID       `json:"attachment_job_id" db:"attachment_job_id"`
	EntityFamily    string          `json:"entity_family" db:"entity_family"`
	EntityID        uuid.UUID       `json:"entity_id" db:"entity_id"`
	EventType       string          `json:"event_type" db:"event_type"`
	PayloadJSON     json.RawMessage `json:"payload_json" db:"payload_json"`
	Status          string          `json:"status" db:"status"`
	Attempts        int             `json:"attempts" db:"attempts"`
	NextRetryAt     *time.Time      `json:"next_retry_at,omitempty" db:"next_retry_at"`
	CreatedAt       time.Time       `json:"created_at" db:"created_at"`
	PublishedAt     *time.Time      `json:"published_at,omitempty" db:"published_at"`
}

// ─────────────────────────────────────────────────────────────────────────────
// REQUEST / RESPONSE TYPES — HTTP boundary for Service 5C endpoints.
// ─────────────────────────────────────────────────────────────────────────────

// AttachmentRequest triggers an attachment job for a settlement batch or single observation.
type AttachmentRequest struct {
	TenantID           string  `json:"tenant_id" binding:"required"`
	SettlementBatchRef *string `json:"settlement_batch_ref,omitempty"`
	IntentID           *string `json:"intent_id,omitempty"`
	IngestRunID        *string `json:"ingest_run_id,omitempty"`
	JobScopeType       string  `json:"job_scope_type"` // SETTLEMENT_BATCH | SINGLE_INTENT | INGEST_RUN
}

// AttachmentResponse is returned after a job completes (sync) or is queued (async).
type AttachmentResponse struct {
	AttachmentJobID     string `json:"attachment_job_id"`
	Status              string `json:"status"`
	ExactMatchCount     int    `json:"exact_match_count"`
	HighConfidenceCount int    `json:"high_confidence_count"`
	AmbiguousCount      int    `json:"ambiguous_count"`
	UnresolvedCount     int    `json:"unresolved_count"`
	ConflictedCount     int    `json:"conflicted_count"`
	Message             string `json:"message"`
}

// AttachmentDecisionResponse is returned when fetching the decision for one intent.
type AttachmentDecisionResponse struct {
	Decision     *AttachmentDecision     `json:"decision"`
	Variance     *VarianceRecord         `json:"variance,omitempty"`
	BatchSummary *BatchAttachmentSummary `json:"batch_summary,omitempty"`
}
