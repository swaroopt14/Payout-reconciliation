package recon

import (
	"context"
	"strings"
	"time"
)

type FinanceListRow struct {
	PaymentID        string    `json:"payment_id"`
	PayoutID         string    `json:"payout_id,omitempty"`
	Entity           string    `json:"entity,omitempty"`
	Settlement       *bool     `json:"settlement"`
	Bank             *bool     `json:"bank"`
	Result           string    `json:"result"`
	VarianceAmount   int64     `json:"variance_amount"`
	Reason           string    `json:"reason,omitempty"`
	Status           string    `json:"status,omitempty"`
	UTR              *string   `json:"utr"`
	AmountMinor      int64     `json:"amount_minor"`
	Currency         string    `json:"currency,omitempty"`
	Mode             string    `json:"mode,omitempty"`
	Purpose          string    `json:"purpose,omitempty"`
	CreatedAt        int64     `json:"created_at,omitempty"`
	ErrorCode        string    `json:"error_code,omitempty"`
	ErrorDescription string    `json:"error_description,omitempty"`
	Direction        string    `json:"direction,omitempty"`
	Rail             string    `json:"rail,omitempty"`
	TwoWay           *ReconLeg `json:"two_way,omitempty"`
	ThreeWay         *ReconLeg `json:"three_way,omitempty"`
}

type FinanceListResponse struct {
	Records    int              `json:"records"`
	Matched    int              `json:"matched"`
	Exceptions int              `json:"exceptions"`
	Results    []FinanceListRow `json:"results"`
}

type FinanceEvaluation struct {
	DatasetRecords          int     `json:"dataset_records"`
	ReconciliationRate      float64 `json:"reconciliation_rate"`
	ExceptionDetectionRate  float64 `json:"exception_detection_rate"`
	ExceptionResolutionRate float64 `json:"exception_resolution_rate"`
	FalseResolutionRate     float64 `json:"false_resolution_rate"`
	FinancialAccuracy       float64 `json:"financial_accuracy"`
	EvidenceGrounding       float64 `json:"evidence_grounding"`
}

func (s *FinancialService) ListFinanceResults(ctx context.Context, tenantID, connectorID, resultFilter string) (FinanceListResponse, error) {
	out := FinanceListResponse{Results: []FinanceListRow{}}
	payouts, err := s.Store.ListCanonicalPayouts(ctx, tenantID, connectorID)
	if err != nil {
		return out, err
	}
	pays, err := s.Store.ListCanonicalPayments(ctx, tenantID, connectorID)
	if err != nil {
		return out, err
	}
	results, err := s.Store.ListReconciliationResults(ctx, tenantID, connectorID)
	if err != nil {
		return out, err
	}
	byKey := map[string]FinancialResult{}
	for _, fr := range results {
		byKey[fr.EntityType+"|"+fr.EntityID] = fr
	}
	want := strings.ToUpper(strings.TrimSpace(resultFilter))
	if want == "ALL" {
		want = ""
	}

	for _, po := range payouts {
		row := payoutListRow(po, byKey[EntityPayout+"|"+po.PayoutID])
		if want != "" && strings.ToUpper(row.Result) != want {
			continue
		}
		out.Results = append(out.Results, row)
	}
	for _, pay := range pays {
		row := paymentListRow(pay, byKey[EntityPayment+"|"+pay.PaymentID])
		if want != "" && strings.ToUpper(row.Result) != want {
			continue
		}
		out.Results = append(out.Results, row)
	}

	out.Records = len(out.Results)
	for _, row := range out.Results {
		if strings.EqualFold(row.Result, ResultMatched) {
			out.Matched++
			continue
		}
		if row.Reason == "not_run" || row.Result == "" {
			continue
		}
		out.Exceptions++
	}
	return out, nil
}

func (s *FinancialService) Evaluation(ctx context.Context, tenantID, connectorID string) (FinanceEvaluation, error) {
	out := FinanceEvaluation{}
	results, err := s.Store.ListReconciliationResults(ctx, tenantID, connectorID)
	if err != nil {
		return out, err
	}
	exceptions, err := s.Store.ListReconciliationExceptions(ctx, tenantID, connectorID)
	if err != nil {
		return out, err
	}
	investigations, err := s.Store.ListInvestigations(ctx, tenantID, connectorID)
	if err != nil {
		return out, err
	}

	scored := 0
	matched := 0
	grounded := 0
	for _, fr := range results {
		if fr.Reason == "not_run" || fr.Result == "" {
			continue
		}
		scored++
		if fr.Result == ResultMatched {
			matched++
		}
		if hasEvidence(fr.EvidenceRefs) {
			grounded++
		}
	}
	out.DatasetRecords = scored
	if scored > 0 {
		rate := float64(matched) / float64(scored)
		out.ReconciliationRate = rate
		out.FinancialAccuracy = rate
		out.EvidenceGrounding = float64(grounded) / float64(scored)
		out.ExceptionDetectionRate = float64(len(exceptions)) / float64(scored)
	}
	completed := 0
	for _, rec := range investigations {
		if strings.EqualFold(rec.Status, "completed") {
			completed++
		}
	}
	if len(investigations) > 0 {
		out.ExceptionResolutionRate = float64(completed) / float64(len(investigations))
	}
	return out, nil
}

func payoutListRow(po PayoutFact, fr FinancialResult) FinanceListRow {
	if fr.Rail == "" {
		fr.Rail = NormalizeRail(po.Mode)
	}
	if fr.EntityType == "" {
		fr.EntityType = EntityPayout
	}
	AnnotateCashFlow(&fr)
	row := FinanceListRow{
		PaymentID:   po.PayoutID,
		PayoutID:    po.PayoutID,
		Entity:      "payout",
		Status:      po.ProviderStatus,
		AmountMinor: po.AmountMinor,
		Currency:    nzCur(po.Currency),
		Mode:        po.Mode,
		Purpose:     po.Purpose,
		CreatedAt:   unixSeconds(firstTime(po.ProviderCreatedAt, po.FirstObservedAt)),
		UTR:         nullableString(po.UTR),
		Direction:   DirectionOutbound,
		Rail:        fr.Rail,
	}
	applyResult(&row, fr, po.StatusReason)
	if fr.Reason != "not_run" && fr.Result != "" {
		settled := fr.EvidenceRefs.SettlementLineID != ""
		bank := fr.BankCreditProven
		row.Settlement = &settled
		row.Bank = &bank
	}
	return row
}

func paymentListRow(pay PaymentFact, fr FinancialResult) FinanceListRow {
	if fr.Rail == "" {
		fr.Rail = NormalizeRail(pay.Method)
	}
	if fr.EntityType == "" {
		fr.EntityType = EntityPayment
	}
	AnnotateCashFlow(&fr)
	row := FinanceListRow{
		PaymentID:   pay.PaymentID,
		Entity:      "payment",
		Status:      firstNonEmpty(pay.ProviderStatus, pay.CanonicalStatus),
		AmountMinor: pay.AmountMinor,
		Currency:    nzCur(pay.Currency),
		CreatedAt:   unixSeconds(firstTime(pay.ProviderCreatedAt, pay.FirstObservedAt)),
		Direction:   DirectionInbound,
		Rail:        fr.Rail,
	}
	applyResult(&row, fr, "")
	if fr.Reason != "not_run" && fr.Result != "" {
		settled := fr.EvidenceRefs.SettlementLineID != ""
		bank := fr.BankCreditProven
		row.Settlement = &settled
		row.Bank = &bank
	}
	return row
}

func applyResult(row *FinanceListRow, fr FinancialResult, statusReason string) {
	if fr.Result == "" || fr.Reason == "not_run" {
		row.Result = ""
		row.Reason = "not_run"
		return
	}
	row.Result = fr.Result
	row.Reason = fr.Reason
	row.VarianceAmount = fr.VarianceAmount
	if statusReason != "" {
		row.ErrorCode = statusReason
	} else if fr.Reason != "" && fr.Result != ResultMatched {
		row.ErrorCode = fr.Reason
	}
	if fr.TwoWay.Result != "" {
		leg := fr.TwoWay
		row.TwoWay = &leg
	}
	if fr.ThreeWay.Result != "" {
		leg := fr.ThreeWay
		row.ThreeWay = &leg
	}
}

func unixSeconds(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

func firstTime(a, b time.Time) time.Time {
	if !a.IsZero() {
		return a
	}
	return b
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func nullableString(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

func hasEvidence(refs EvidenceRefs) bool {
	return refs.CanonicalPaymentID != "" ||
		refs.SettlementLineID != "" ||
		refs.SettlementBankDecisionID != "" ||
		refs.BankObservationID != "" ||
		len(refs.ObservationEventIDs) > 0 ||
		len(refs.PayloadHashes) > 0
}

func ReconJSON(fr FinancialResult) any {
	if fr.Result == "" || fr.Reason == "not_run" {
		return nil
	}
	AnnotateCashFlow(&fr)
	out := map[string]any{
		"result":             fr.Result,
		"reason":             fr.Reason,
		"expected_amount":    fr.ExpectedAmount,
		"observed_amount":    fr.ObservedAmount,
		"variance_amount":    fr.VarianceAmount,
		"confidence":         fr.Confidence,
		"bank_credit_proven": fr.BankCreditProven,
	}
	if fr.Direction != "" {
		out["direction"] = fr.Direction
	}
	if fr.Rail != "" {
		out["rail"] = fr.Rail
	}
	if fr.TwoWay.Result != "" {
		out["two_way"] = fr.TwoWay
	}
	if fr.ThreeWay.Result != "" {
		out["three_way"] = fr.ThreeWay
	}
	return out
}
