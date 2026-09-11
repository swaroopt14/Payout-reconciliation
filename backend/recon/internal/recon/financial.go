package recon

import (
	"encoding/json"
	"strings"
	"time"
)

const FinancialRuleVersion = "financial_recon_v1"

const (
	ResultMatched    = "MATCHED"
	ResultAmbiguous  = "AMBIGUOUS"
	ResultUnresolved = "UNRESOLVED"
	ResultConflicted = "CONFLICTED"
	ResultVariance   = "VARIANCE"
	ResultOrphan     = "ORPHAN"
)

const (
	EntityPayment = "payment"
	EntityBank    = "bank"
	EntityPayout  = "payout"
)

const DefaultStuckAfter = 72 * time.Hour
const DefaultPayoutSLA = 15 * time.Minute

type EvidenceRefs struct {
	CanonicalPaymentID       string   `json:"canonical_payment_id,omitempty"`
	ObservationEventIDs      []string `json:"observation_event_ids,omitempty"`
	SettlementLineID         string   `json:"settlement_line_id,omitempty"`
	SettlementBankDecisionID string   `json:"settlement_bank_decision_id,omitempty"`
	BankObservationID        string   `json:"bank_observation_id,omitempty"`
	PayloadHashes            []string `json:"payload_hashes,omitempty"`
	PaymentAmountMinor       int64    `json:"payment_amount_minor,omitempty"`
	SettlementNetMinor       int64    `json:"settlement_net_minor,omitempty"`
	BankCreditMinor          int64    `json:"bank_credit_minor,omitempty"`
}

type PaymentFact struct {
	ID                string
	PaymentID         string
	CanonicalStatus   string
	ProviderStatus    string
	Captured          bool
	AmountMinor       int64
	Currency          string
	Method            string
	ProviderCreatedAt time.Time
	FirstObservedAt   time.Time
	Sources           []string
	FeeMinor          int64
	TaxMinor          int64
}

type ObservationFact struct {
	Source          string
	ProviderStatus  string
	CanonicalStatus string
	SourceEventID   string
	SourceHash      string
	RawReference    string
	ObservedAt      time.Time
}

type RefundFact struct {
	ID             string `json:"id"`
	RefundID       string `json:"refund_id"`
	PaymentID      string `json:"payment_id"`
	AmountMinor    int64  `json:"amount_minor"`
	Currency       string `json:"currency"`
	ProviderStatus string `json:"provider_status"`
	Source         string `json:"source"`
}

type PayoutFact struct {
	ID                string
	PayoutID          string
	ProviderStatus    string
	AmountMinor       int64
	Currency          string
	UTR               string
	Mode              string
	Purpose           string
	StatusReason      string
	ProviderCreatedAt time.Time
	FirstObservedAt   time.Time
}

type PayoutInput struct {
	Payout     PayoutFact
	Events     []ObservationFact
	Banks      []BankTxn
	Now        time.Time
	StuckAfter time.Duration
}

type FinancialInput struct {
	Payment    PaymentFact
	Events     []ObservationFact
	Lines      []SettlementLine
	Decisions  []SettlementBankDecision
	Banks      []BankTxn
	Refunds    []RefundFact
	Now        time.Time
	StuckAfter time.Duration
}

type FinancialResult struct {
	ID               string
	RunID            string
	EntityType       string
	EntityID         string
	Status           string
	Result           string
	ExpectedAmount   int64
	ObservedAmount   int64
	VarianceAmount   int64
	Confidence       float64
	Reason           string
	CandidateIDs     []string
	EvidenceRefs     EvidenceRefs
	BankCreditProven bool
	Direction        string
	Rail             string
	TwoWay           ReconLeg
	ThreeWay         ReconLeg
	CreatedAt        time.Time
	Exception        *ReconciliationException
}

func ReconcilePayment(in FinancialInput) FinancialResult {
	out := reconcilePayment(in)
	if out.Rail == "" {
		out.Rail = NormalizeRail(in.Payment.Method)
	}
	AnnotateCashFlow(&out)
	return out
}

func reconcilePayment(in FinancialInput) FinancialResult {
	pay := in.Payment
	status := strings.ToLower(strings.TrimSpace(pay.CanonicalStatus))
	if status == "" {
		status = strings.ToLower(strings.TrimSpace(pay.ProviderStatus))
	}
	now := in.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	stuckAfter := in.StuckAfter
	if stuckAfter <= 0 {
		stuckAfter = DefaultStuckAfter
	}

	out := FinancialResult{
		EntityType:     EntityPayment,
		EntityID:       pay.PaymentID,
		Status:         status,
		ExpectedAmount: pay.AmountMinor,
		EvidenceRefs:   evidenceFrom(in),
	}

	paymentLines, refundLines := splitLines(in.Lines)
	hasPaymentSettlement := len(paymentLines) > 0
	hasRefund := len(refundLines) > 0 || len(in.Refunds) > 0
	hasAnySettlement := len(in.Lines) > 0
	moved := HasBankMovement(in.Banks)

	exact := firstDecision(in.Decisions, BankMatchExact)
	high := firstDecision(in.Decisions, BankMatchHighConfidence)
	conflicted := firstDecision(in.Decisions, BankMatchConflicted)
	if conflicted == nil {
		conflicted = firstDecision(in.Decisions, BankMatchVariance)
	}
	ambiguous := firstDecision(in.Decisions, BankMatchAmbiguous)

	out.EvidenceRefs.PaymentAmountMinor = pay.AmountMinor
	out.EvidenceRefs.SettlementNetMinor = settlementNet(in.Lines)
	if len(paymentLines) > 0 {
		out.EvidenceRefs.SettlementLineID = lineID(paymentLines[0])
	} else if len(refundLines) > 0 {
		out.EvidenceRefs.SettlementLineID = lineID(refundLines[0])
	}

	switch {
	case isFailedLike(status):
		return reconcileFailed(out, in, hasAnySettlement, hasRefund, moved)
	case isCapturedOrLater(status) || pay.Captured:
		return reconcileCaptured(out, in, hasPaymentSettlement, exact, high, conflicted, ambiguous)
	case isOpen(status):
		age := now.Sub(pay.ProviderCreatedAt)
		if pay.ProviderCreatedAt.IsZero() {
			age = now.Sub(pay.FirstObservedAt)
		}
		if age >= stuckAfter && !hasAnySettlement && !moved {
			return withException(out, ResultUnresolved, "open_status_no_downstream", 0.4)
		}
		out.Result = ResultUnresolved
		out.Reason = "open_status"
		out.Confidence = 0.3
		return out
	default:
		out.Result = ResultUnresolved
		out.Reason = "insufficient_evidence"
		out.Confidence = 0.2
		return out
	}
}

func reconcileFailed(out FinancialResult, in FinancialInput, hasSettlement, hasRefund, moved bool) FinancialResult {
	if moved && !hasRefund {
		amt := BankMovementMinor(in.Banks)
		out.ObservedAmount = amt
		out.VarianceAmount = amt
		return withException(out, ResultUnresolved, "failed_with_bank_movement", 0.9)
	}
	if hasRefund && !moved {
		out.Result = ResultMatched
		out.Reason = "failed_refund_no_bank_movement"
		out.Confidence = 0.85
		out.BankCreditProven = false
		return out
	}
	if !hasSettlement && !moved {
		out.Result = ResultMatched
		out.Reason = "failed_no_money_movement"
		out.Confidence = 0.95
		out.BankCreditProven = false
		return out
	}
	out.Result = ResultUnresolved
	out.Reason = "failed_unexplained"
	out.Confidence = 0.5
	return withException(out, ResultUnresolved, out.Reason, out.Confidence)
}

func reconcileCaptured(out FinancialResult, in FinancialInput, hasPaymentSettlement bool, exact, high, conflicted, ambiguous *SettlementBankDecision) FinancialResult {
	if !hasPaymentSettlement {
		return withException(out, ResultUnresolved, "captured_missing_settlement", 0.8)
	}
	paymentLines, _ := splitLines(in.Lines)
	if len(paymentLines) > 1 {
		totalNet := settlementNet(in.Lines)
		out.ObservedAmount = totalNet
		if totalNet > in.Payment.AmountMinor {
			out.VarianceAmount = totalNet - in.Payment.AmountMinor
		}
		if exact != nil {
			attachDecision(&out, *exact)
		}
		return withException(out, ResultConflicted, "duplicate_settlement", 0.85)
	}
	if gap, partial := partialSettlementGap(in.Payment.AmountMinor, paymentLines); partial {
		out.ObservedAmount = settlementNet(in.Lines)
		out.VarianceAmount = gap
		if exact != nil {
			attachDecision(&out, *exact)
			out.BankCreditProven = true
			if v, ok := evidenceInt64(exact.Evidence, "bank_credit_minor"); ok {
				out.ObservedAmount = v
			}
		}
		return withException(out, ResultVariance, "partial_settlement", 0.88)
	}
	if gap, reason, ok := paymentSettlementFeeTaxGap(in.Payment, paymentLines); ok {
		out.ObservedAmount = settlementNet(in.Lines)
		out.VarianceAmount = gap
		if exact != nil {
			attachDecision(&out, *exact)
			out.BankCreditProven = true
			if v, ok := evidenceInt64(exact.Evidence, "bank_credit_minor"); ok {
				out.ObservedAmount = v
			}
		} else if high != nil {
			attachDecision(&out, *high)
		}
		return withException(out, ResultVariance, reason, 0.9)
	}
	if conflicted != nil {
		out.CandidateIDs = append([]string{}, conflicted.Candidates...)
		attachDecision(&out, *conflicted)
		bankAmt := observedFromDecision(*conflicted, in.Banks)
		out.ObservedAmount = bankAmt
		out.EvidenceRefs.BankCreditMinor = bankAmt
		if d, ok := evidenceInt64(conflicted.Evidence, "difference_minor"); ok {
			out.VarianceAmount = d
		} else {
			net := settlementNet(in.Lines)
			out.VarianceAmount = net - bankAmt
		}
		res := ResultVariance
		if conflicted.State == BankMatchConflicted && out.VarianceAmount == 0 {
			res = ResultConflicted
		}
		return withException(out, res, "amount_mismatch", conflicted.Confidence)
	}
	if ambiguous != nil {
		out.CandidateIDs = append([]string{}, ambiguous.Candidates...)
		attachDecision(&out, *ambiguous)
		out.Result = ResultAmbiguous
		out.Reason = "ambiguous_bank_candidates"
		out.Confidence = 0.5
		out.BankCreditProven = false
		return withException(out, ResultAmbiguous, out.Reason, out.Confidence)
	}
	if exact != nil {
		attachDecision(&out, *exact)
		out.Result = ResultMatched
		out.Reason = "captured_settlement_exact_bank"
		out.Confidence = 0.99
		out.BankCreditProven = true
		if v, ok := evidenceInt64(exact.Evidence, "bank_credit_minor"); ok {
			out.ObservedAmount = v
		} else {
			out.ObservedAmount = settlementNet(in.Lines)
		}
		out.EvidenceRefs.BankCreditMinor = out.ObservedAmount
		out.VarianceAmount = 0
		return out
	}
	if high != nil {
		attachDecision(&out, *high)
		out.Result = ResultMatched
		out.Reason = "captured_settlement_high_confidence_bank"
		out.Confidence = high.Confidence
		if out.Confidence == 0 {
			out.Confidence = 0.7
		}
		out.BankCreditProven = false
		out.ObservedAmount = settlementNet(in.Lines)
		return out
	}
	return withException(out, ResultUnresolved, "settlement_without_bank", 0.7)
}

func OrphanBankResult(b BankTxn) FinancialResult {
	out := orphanBankResult(b)
	AnnotateCashFlow(&out)
	return out
}

func orphanBankResult(b BankTxn) FinancialResult {
	out := FinancialResult{
		EntityType:     EntityBank,
		EntityID:       b.ID,
		Status:         "",
		Result:         ResultOrphan,
		ExpectedAmount: 0,
		ObservedAmount: b.CreditMinor,
		VarianceAmount: b.CreditMinor,
		Confidence:     0.9,
		Reason:         "orphan_bank_credit",
		EvidenceRefs:   EvidenceRefs{BankObservationID: b.ID, PayloadHashes: hashList(b.RowHash)},
	}
	return withException(out, ResultOrphan, out.Reason, out.Confidence)
}

func isCapturedOrLater(status string) bool {
	switch status {
	case PaymentCaptured, PaymentPartiallyRefunded, PaymentRefunded:
		return true
	default:
		return false
	}
}

func isFailedLike(status string) bool {
	switch status {
	case PaymentFailed, "cancelled", "canceled", "rejected":
		return true
	default:
		return false
	}
}

func isOpen(status string) bool {
	switch status {
	case PaymentCreated, PaymentAuthorized, "processing", "pending":
		return true
	default:
		return false
	}
}

func splitLines(lines []SettlementLine) (payment, refund []SettlementLine) {
	for _, l := range lines {
		switch strings.ToLower(l.LineType) {
		case "refund":
			refund = append(refund, l)
		case "payment", "":
			payment = append(payment, l)
		default:
			payment = append(payment, l)
		}
	}
	return payment, refund
}

func firstDecision(ds []SettlementBankDecision, state string) *SettlementBankDecision {
	for i := range ds {
		if ds[i].State == state {
			return &ds[i]
		}
	}
	return nil
}

func attachDecision(out *FinancialResult, d SettlementBankDecision) {
	out.EvidenceRefs.SettlementBankDecisionID = d.ID
	if d.BankObservationID != "" {
		out.EvidenceRefs.BankObservationID = d.BankObservationID
	}
	if len(d.Candidates) > 0 && out.CandidateIDs == nil {
		out.CandidateIDs = append([]string{}, d.Candidates...)
	}
}

func observedFromDecision(d SettlementBankDecision, banks []BankTxn) int64 {
	if v, ok := evidenceInt64(d.Evidence, "bank_credit_minor"); ok {
		return v
	}
	for _, b := range banks {
		if b.ID == d.BankObservationID {
			if b.CreditMinor != 0 {
				return b.CreditMinor
			}
			return b.DebitMinor
		}
	}
	return 0
}

func evidenceInt64(m map[string]any, key string) (int64, bool) {
	if m == nil {
		return 0, false
	}
	v, ok := m[key]
	if !ok || v == nil {
		return 0, false
	}
	switch n := v.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	case int32:
		return int64(n), true
	case float64:
		return int64(n), true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	default:
		return 0, false
	}
}

func settlementNet(lines []SettlementLine) int64 {
	var n int64
	for _, l := range lines {
		if strings.EqualFold(l.LineType, "payment") || l.LineType == "" {
			n += lineNet(l)
		}
	}
	return n
}

func paymentSettlementFeeTaxGap(pay PaymentFact, paymentLines []SettlementLine) (int64, string, bool) {
	var sFee, sTax int64
	for _, l := range paymentLines {
		sFee += l.FeeMinor
		sTax += l.TaxMinor
	}
	if pay.FeeMinor != 0 && pay.FeeMinor != sFee {
		gap := pay.FeeMinor - sFee
		if gap < 0 {
			gap = -gap
		}
		return gap, "fee_mismatch", true
	}
	if pay.TaxMinor != 0 && pay.TaxMinor != sTax {
		gap := pay.TaxMinor - sTax
		if gap < 0 {
			gap = -gap
		}
		return gap, "tax_mismatch", true
	}
	return 0, "", false
}

func feeTaxDeduction(lines []SettlementLine) int64 {
	var n int64
	for _, l := range lines {
		if strings.EqualFold(l.LineType, "payment") || l.LineType == "" {
			n += l.FeeMinor + l.TaxMinor
		}
	}
	return n
}

func partialSettlementGap(paymentAmount int64, paymentLines []SettlementLine) (int64, bool) {
	if paymentAmount <= 0 || len(paymentLines) == 0 {
		return 0, false
	}
	net := settlementNet(paymentLines)
	if net >= paymentAmount {
		return 0, false
	}
	explained := feeTaxDeduction(paymentLines)
	if net+explained >= paymentAmount {
		return 0, false
	}
	return paymentAmount - net, true
}

func lineID(l SettlementLine) string {
	if l.ID != "" {
		return l.ID
	}
	return l.EntityID
}

func evidenceFrom(in FinancialInput) EvidenceRefs {
	refs := EvidenceRefs{
		CanonicalPaymentID: in.Payment.ID,
	}
	for _, ev := range in.Events {
		if ev.SourceEventID != "" {
			refs.ObservationEventIDs = append(refs.ObservationEventIDs, ev.SourceEventID)
		}
		if ev.SourceHash != "" {
			refs.PayloadHashes = append(refs.PayloadHashes, ev.SourceHash)
		}
	}
	if in.Payment.ID != "" {
		// keep
	}
	return refs
}

func hashList(h string) []string {
	if h == "" {
		return nil
	}
	return []string{h}
}

func withException(out FinancialResult, result, reason string, conf float64) FinancialResult {
	out.Result = result
	out.Reason = reason
	out.Confidence = conf
	ids := EvidenceIDList(out.EvidenceRefs)
	out.Exception = &ReconciliationException{
		EntityType:           out.EntityType,
		EntityID:             out.EntityID,
		Status:               out.Status,
		ReconciliationResult: result,
		Reason:               reason,
		ExpectedAmount:       out.ExpectedAmount,
		ObservedAmount:       out.ObservedAmount,
		VarianceAmount:       out.VarianceAmount,
		CandidateIDs:         append([]string{}, out.CandidateIDs...),
		Confidence:           conf,
		EvidenceIDs:          ids,
		EvidenceRefs:         out.EvidenceRefs,
	}
	return out
}

func EvidenceIDList(r EvidenceRefs) []string {
	var ids []string
	if r.CanonicalPaymentID != "" {
		ids = append(ids, r.CanonicalPaymentID)
	}
	ids = append(ids, r.ObservationEventIDs...)
	if r.SettlementLineID != "" {
		ids = append(ids, r.SettlementLineID)
	}
	if r.SettlementBankDecisionID != "" {
		ids = append(ids, r.SettlementBankDecisionID)
	}
	if r.BankObservationID != "" {
		ids = append(ids, r.BankObservationID)
	}
	return ids
}

func MarkAmbiguous(out FinancialResult, candidates []string) FinancialResult {
	out.Result = ResultAmbiguous
	out.Reason = "shared_utr_or_bank_candidates"
	out.CandidateIDs = candidates
	out.BankCreditProven = false
	out.Confidence = 0.45
	return withException(out, ResultAmbiguous, out.Reason, out.Confidence)
}
