package recon

import "time"

type ReconciliationException struct {
	ID                   string       `json:"id"`
	RunID                string       `json:"run_id"`
	TenantID             string       `json:"tenant_id"`
	ConnectorID          string       `json:"connector_id"`
	EntityType           string       `json:"entity_type"`
	EntityID             string       `json:"entity_id"`
	Status               string       `json:"status"`
	ReconciliationResult string       `json:"reconciliation_result"`
	Reason               string       `json:"reason"`
	ExpectedAmount       int64        `json:"expected_amount"`
	ObservedAmount       int64        `json:"observed_amount"`
	VarianceAmount       int64        `json:"variance_amount"`
	CandidateIDs         []string     `json:"candidate_ids"`
	Confidence           float64      `json:"confidence"`
	EvidenceIDs          []string     `json:"evidence_ids"`
	EvidenceRefs         EvidenceRefs `json:"evidence_refs"`
	CreatedAt            time.Time    `json:"created_at"`
	UpdatedAt            time.Time    `json:"updated_at"`
}

type ReasonExposure struct {
	Reason        string `json:"reason"`
	Count         int    `json:"count"`
	ExposureMinor int64  `json:"exposure_minor"`
}

type FinanceSummary struct {
	EntityCounts     map[string]int   `json:"entity_counts"`
	ResultCounts     map[string]int   `json:"result_counts"`
	ExposureMinor    int64            `json:"exposure_minor"`
	ExposureByReason []ReasonExposure `json:"exposure_by_reason"`
	Currency         string           `json:"currency"`
	ScoredCount      int              `json:"scored_count"`
	MatchedCount     int              `json:"matched_count"`
}

type ReconciliationRun struct {
	ID             string
	TenantID       string
	ConnectorID    string
	AccountID      string
	BatchID        string
	EntityIDs      []string
	Status         string
	PaymentCount   int
	MatchedCount   int
	ExceptionCount int
	Counts         map[string]int
	CreatedAt      time.Time
	CompletedAt    time.Time
}

type InvestigationRecord struct {
	ID              string    `json:"id"`
	TenantID        string    `json:"tenant_id"`
	ConnectorID     string    `json:"connector_id"`
	ExceptionID     string    `json:"exception_id"`
	EntityType      string    `json:"entity_type"`
	EntityID        string    `json:"entity_id"`
	Status          string    `json:"status"`
	RootCause       string    `json:"root_cause"`
	Recommendation  string    `json:"recommendation"`
	Confidence      float64   `json:"confidence"`
	FinancialImpact int64     `json:"financial_impact"`
	EvidenceIDs     []string  `json:"evidence_ids"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func NeedsInvestigation(r FinancialResult) bool {
	if r.Exception == nil {
		return false
	}
	switch r.Result {
	case ResultMatched:
		return false
	case ResultUnresolved, ResultConflicted, ResultVariance, ResultAmbiguous, ResultOrphan:
		return true
	default:
		return r.Exception != nil
	}
}

func DeterministicInvestigation(ex ReconciliationException) InvestigationRecord {
	rec := InvestigationRecord{
		ExceptionID:     ex.ID,
		EntityType:      ex.EntityType,
		EntityID:        ex.EntityID,
		Status:          "completed",
		FinancialImpact: ex.VarianceAmount,
		Confidence:      ex.Confidence,
		EvidenceIDs:     append([]string{}, ex.EvidenceIDs...),
	}
	switch ex.Reason {
	case "failed_with_bank_movement":
		rec.RootCause = "Payment failed at the PSP lifecycle level, but a corresponding bank movement was detected without a matching settlement/refund record."
		rec.Recommendation = "Investigate the unmatched bank movement and provider-side transaction outcome."
	case "captured_missing_settlement":
		rec.RootCause = "Payment is captured but no settlement line is linked by payment_id."
		rec.Recommendation = "Wait for settlement recon or backfill settlement lines for this payment_id."
	case "settlement_without_bank":
		rec.RootCause = "Settlement is observed but no bank CREDIT candidate is matched."
		rec.Recommendation = "Confirm the bank statement window and UTR against the settlement UTR."
	case "amount_mismatch":
		rec.RootCause = "A unique UTR matched a bank row whose amount differs from the settlement net."
		rec.Recommendation = "Do not force a match. Review fee/tax/adjustment and the bank amount."
	case "partial_settlement":
		rec.RootCause = "Settlement net is less than the captured payment amount and the gap is not fully explained by fee/tax on the settlement line."
		rec.Recommendation = "Review whether the payment was partially settled or a settlement line is missing."
	case "duplicate_settlement":
		rec.RootCause = "More than one payment-type settlement line exists for the same payment_id."
		rec.Recommendation = "Do not force a match. Review duplicate settlement lines and provider settlement files."
	case "ambiguous_bank_candidates", "shared_utr_or_bank_candidates":
		rec.RootCause = "More than one plausible bank candidate exists. No match was forced."
		rec.Recommendation = "Finance should pick from the candidate IDs using additional evidence."
	case "orphan_bank_credit":
		rec.RootCause = "A bank CREDIT has no related settlement or payment."
		rec.Recommendation = "Keep the bank row and search for a missing settlement or payment."
	case "open_status_no_downstream":
		rec.RootCause = "Payment remains in an open Razorpay status with no settlement or bank movement after the age window."
		rec.Recommendation = "Do not rename the Razorpay status. Check later events or backfill."
	case "payout_failed_with_bank_movement":
		rec.RootCause = "Payout failed, cancelled, or rejected at the Razorpay lifecycle level, but a bank movement was detected."
		rec.Recommendation = "ESCALATE: confirm whether the debit should be reversed. Do not rename the Razorpay status."
	case "payout_missing_bank":
		rec.RootCause = "Payout is processed but no matching bank DEBIT was found."
		rec.Recommendation = "MONITOR the bank statement window and UTR. Status stays processed."
	case "payout_open_past_sla":
		rec.RootCause = "Payout remains in an open Razorpay status past the configured SLA. Status is unchanged."
		rec.Recommendation = "MONITOR or ESCALATE payout operations. Do not rename the status to STUCK."
	case "payout_reversed_unexplained":
		rec.RootCause = "Payout is reversed and the corresponding bank story is incomplete."
		rec.Recommendation = "REQUEST_REVIEW of reversal and bank movement."
	default:
		rec.RootCause = ex.Reason
		rec.Recommendation = "Review evidence_refs. Do not change the Razorpay status."
	}
	return rec
}
