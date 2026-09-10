package recon

import (
	"sort"
	"strings"
	"time"
)

// Fixed 14-step ARL ladder. Missing steps stay in the array with captured=false.
// captured_at is only the time we ingested (webhook observed_at, file settled_at).
// provider_at is the PSP clock when we have it — never used as a stand-in for capture time.
const (
	TimelineMerchantInstruction = "MERCHANT_PAYOUT_INSTRUCTION"
	TimelineRazorpayCreated     = "RAZORPAY_PAYOUT_CREATED"
	TimelineValidation          = "VALIDATION_ELIGIBILITY"
	TimelineGovernance          = "APPROVAL_GOVERNANCE"
	TimelineRouting             = "ROUTING_RAIL_SELECTION"
	TimelineSubmitted           = "PAYOUT_SUBMITTED_TO_RAIL"
	TimelineProviderResponse    = "PROVIDER_BANK_RESPONSE"
	TimelineStatusUpdates       = "PAYOUT_STATUS_UPDATES"
	TimelineUTR                 = "UTR_BANK_REFERENCE"
	TimelineSettlement          = "SETTLEMENT_ACCOUNTING_RECORD"
	TimelineBankCash            = "BANK_SIDE_CASH_MOVEMENT"
	TimelineReconRun            = "ZORD_RECONCILIATION"
	TimelineReconDecision       = "RECONCILIATION_DECISION"
	TimelineEvidence            = "EVIDENCE_AUDIT_TRAIL"
)

type TimelineCapture struct {
	CapturedAt     time.Time `json:"captured_at"`
	Source         string    `json:"source"`
	SourceEventID  string    `json:"source_event_id,omitempty"`
	SourceHash     string    `json:"source_hash,omitempty"`
	ProviderStatus string    `json:"provider_status,omitempty"`
	Reference      string    `json:"utr,omitempty"`
}

type TimelineStep struct {
	Seq            int               `json:"seq"`
	Kind           string            `json:"kind"`
	Label          string            `json:"label"`
	Captured       bool              `json:"captured"`
	CapturedAt     *time.Time        `json:"captured_at"`
	Source         string            `json:"source,omitempty"`
	SourceEventID  string            `json:"source_event_id,omitempty"`
	SourceHash     string            `json:"source_hash,omitempty"`
	ProviderStatus string            `json:"provider_status,omitempty"`
	ProviderAt     *time.Time        `json:"provider_at,omitempty"`
	Detail         map[string]any    `json:"detail,omitempty"`
	Events         []TimelineCapture `json:"events,omitempty"`
	Note           string            `json:"note,omitempty"`
}

type TimelineRecon struct {
	Result           string   `json:"result"`
	Reason           string   `json:"reason"`
	ExpectedAmount   int64    `json:"expected_amount"`
	ObservedAmount   int64    `json:"observed_amount"`
	VarianceAmount   int64    `json:"variance_amount"`
	Confidence       float64  `json:"confidence"`
	BankCreditProven bool     `json:"bank_credit_proven"`
	Direction        string   `json:"direction,omitempty"`
	Rail             string   `json:"rail,omitempty"`
	TwoWay           ReconLeg `json:"two_way,omitempty"`
	ThreeWay         ReconLeg `json:"three_way,omitempty"`
}

type EntityTimeline struct {
	EntityType     string         `json:"entity_type"`
	EntityID       string         `json:"entity_id"`
	ProviderStatus string         `json:"provider_status"`
	ReconRun       bool           `json:"recon_run"`
	Reconciliation *TimelineRecon `json:"reconciliation"`
	Steps          []TimelineStep `json:"steps"`
}

type timelineFacts struct {
	EntityType        string
	EntityID          string
	ProviderStatus    string
	AmountMinor       int64
	Currency          string
	UTR               string
	Mode              string
	ProviderCreatedAt time.Time
	FirstObservedAt   time.Time
	Events            []ObservationFact
	Lines             []SettlementLine
	Banks             []BankTxn
	Result            *FinancialResult
}

func emptyStep(seq int, kind, label, note string) TimelineStep {
	return TimelineStep{Seq: seq, Kind: kind, Label: label, Captured: false, Note: note}
}

func captureFrom(ev ObservationFact) TimelineCapture {
	return TimelineCapture{
		CapturedAt:     ev.ObservedAt.UTC(),
		Source:         timelineSource(ev.Source),
		SourceEventID:  ev.SourceEventID,
		SourceHash:     ev.SourceHash,
		ProviderStatus: statusOf(ev),
		Reference:      strings.TrimSpace(ev.RawReference),
	}
}

func applyCapture(step *TimelineStep, ev ObservationFact) {
	c := captureFrom(ev)
	step.Captured = true
	step.CapturedAt = timePtr(c.CapturedAt)
	step.Source = c.Source
	step.SourceEventID = c.SourceEventID
	step.SourceHash = c.SourceHash
	step.ProviderStatus = c.ProviderStatus
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

func statusOf(ev ObservationFact) string {
	if s := strings.ToLower(strings.TrimSpace(ev.ProviderStatus)); s != "" {
		return s
	}
	return strings.ToLower(strings.TrimSpace(ev.CanonicalStatus))
}

func timelineSource(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	switch s {
	case "webhook", "webhooks":
		return "webhook"
	case "api", "razorpay_api", "api_backfill", "poll":
		return "api_backfill"
	case "bank_csv", "bank":
		return "bank_csv"
	case "settlement", "settlement_file":
		return "settlement_file"
	case "reconciliation", "recon":
		return "reconciliation"
	default:
		return s
	}
}

func isOpenProviderStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "queued", "processing", "pending", "scheduled":
		return true
	default:
		return false
	}
}

func BuildEntityTimeline(f timelineFacts) EntityTimeline {
	if f.Result != nil {
		if f.Result.Rail == "" {
			f.Result.Rail = NormalizeRail(f.Mode)
		}
		if f.Result.EntityType == "" {
			f.Result.EntityType = f.EntityType
		}
		AnnotateCashFlow(f.Result)
	}
	out := EntityTimeline{
		EntityType:     f.EntityType,
		EntityID:       f.EntityID,
		ProviderStatus: f.ProviderStatus,
		ReconRun:       f.Result != nil && f.Result.Reason != "not_run",
	}

	events := append([]ObservationFact{}, f.Events...)
	sortObservations(events)

	var first ObservationFact
	hasFirst := len(events) > 0
	if hasFirst {
		first = events[0]
	}

	// 1. Merchant instruction — recon does not store the source file row.
	out.Steps = append(out.Steps, emptyStep(1, TimelineMerchantInstruction, "Merchant payout instruction",
		"Not captured in recon. Intent/file ingest lives on edge + intents."))

	// 2. Razorpay payout/payment created — first observation we stored.
	s2 := emptyStep(2, TimelineRazorpayCreated, "Razorpay payout created",
		"No provider observation captured yet.")
	if hasFirst {
		applyCapture(&s2, first)
		s2.Note = ""
		s2.Detail = map[string]any{"amount_minor": f.AmountMinor, "currency": f.Currency}
		if !f.ProviderCreatedAt.IsZero() {
			s2.ProviderAt = timePtr(f.ProviderCreatedAt)
		}
	} else if !f.FirstObservedAt.IsZero() {
		s2.Captured = true
		s2.CapturedAt = timePtr(f.FirstObservedAt)
		s2.Source = "observation"
		s2.ProviderStatus = f.ProviderStatus
		s2.Note = "Canonical first_observed_at; raw webhook row not loaded."
		if !f.ProviderCreatedAt.IsZero() {
			s2.ProviderAt = timePtr(f.ProviderCreatedAt)
		}
	}
	out.Steps = append(out.Steps, s2)

	out.Steps = append(out.Steps, emptyStep(3, TimelineValidation, "Validation / eligibility",
		"Not captured in recon. Intents validation is not on this observation log."))
	out.Steps = append(out.Steps, emptyStep(4, TimelineGovernance, "Approval / governance",
		"Not captured in recon. Governance decisions live on the intent engine."))
	s5 := emptyStep(5, TimelineRouting, "Routing / rail selection",
		"No routing-decision event stored. Rail on the payout is not a captured decision time.")
	if strings.TrimSpace(f.Mode) != "" && hasFirst {
		s5.Detail = map[string]any{"mode": f.Mode}
		s5.Note = "Mode observed on the payout record; no separate routing timestamp."
	}
	out.Steps = append(out.Steps, s5)

	// 6. Submitted to rail — first queued/processing/pending/scheduled webhook.
	s6 := emptyStep(6, TimelineSubmitted, "Payout submitted to execution rail",
		"No queued/processing webhook captured.")
	if ev, ok := firstMatching(events, isOpenProviderStatus); ok {
		applyCapture(&s6, ev)
		s6.Note = ""
	}
	out.Steps = append(out.Steps, s6)

	// 7. Provider / bank response — first captured observation (same clock we stored).
	s7 := emptyStep(7, TimelineProviderResponse, "Provider / bank response",
		"No provider response observation captured.")
	if hasFirst {
		applyCapture(&s7, first)
		s7.Note = ""
	}
	out.Steps = append(out.Steps, s7)

	// 8. Status updates — every observation, each with its own captured_at.
	s8 := emptyStep(8, TimelineStatusUpdates, "Payout status updates",
		"No status webhooks captured.")
	if len(events) > 0 {
		s8.Events = make([]TimelineCapture, 0, len(events))
		for _, ev := range events {
			if ev.ObservedAt.IsZero() {
				continue
			}
			s8.Events = append(s8.Events, captureFrom(ev))
		}
		if len(s8.Events) > 0 {
			last := s8.Events[len(s8.Events)-1]
			s8.Captured = true
			s8.Note = ""
			s8.CapturedAt = timePtr(last.CapturedAt)
			s8.Source = last.Source
			s8.SourceEventID = last.SourceEventID
			s8.ProviderStatus = last.ProviderStatus
		}
	}
	out.Steps = append(out.Steps, s8)

	// 9. UTR — first observation that carried a reference, else canonical UTR without a webhook time.
	s9 := emptyStep(9, TimelineUTR, "UTR / bank reference", "No UTR captured on an observation.")
	if ev, ok := firstUTR(events); ok {
		applyCapture(&s9, ev)
		s9.Detail = map[string]any{"utr": strings.TrimSpace(ev.RawReference)}
		s9.Note = ""
	} else if u := strings.TrimSpace(f.UTR); u != "" {
		s9.Captured = true
		s9.Detail = map[string]any{"utr": u}
		s9.ProviderStatus = f.ProviderStatus
		if hasFirst {
			applyCapture(&s9, first)
			s9.Detail = map[string]any{"utr": u}
			s9.Note = "UTR on canonical payout; timestamp is first observation, not a dedicated UTR webhook."
		} else {
			s9.Note = "UTR present on canonical payout; no observation timestamp."
		}
	}
	out.Steps = append(out.Steps, s9)

	// 10. Settlement
	s10 := emptyStep(10, TimelineSettlement, "Settlement / accounting record",
		"No settlement line captured for this payout.")
	if line, ok := settlementForEntity(f.Lines, f.EntityID, f.UTR); ok {
		s10.Captured = true
		s10.Source = "settlement_file"
		s10.Note = ""
		if !line.SettledAt.IsZero() {
			s10.CapturedAt = timePtr(line.SettledAt)
		}
		s10.Detail = map[string]any{
			"settlement_line_id": line.ID,
			"settlement_id":      line.SettlementID,
			"net_minor":          line.CreditMinor,
			"fee_minor":          line.FeeMinor,
			"tax_minor":          line.TaxMinor,
			"utr":                line.UTR,
		}
		if s10.CapturedAt == nil {
			s10.Note = "Settlement line present; settled_at not stored."
		}
	}
	out.Steps = append(out.Steps, s10)

	// 11. Bank cash
	s11 := emptyStep(11, TimelineBankCash, "Bank-side cash movement",
		"No bank observation captured.")
	if bank, ok := bankForEntity(f.Banks, f.UTR, f.Result); ok {
		s11.Captured = true
		s11.Source = "bank_csv"
		s11.Note = ""
		if !bank.ValueDate.IsZero() {
			s11.CapturedAt = timePtr(bank.ValueDate)
		}
		s11.Detail = map[string]any{
			"bank_observation_id": bank.ID,
			"utr":                 bank.UTR,
			"credit_minor":        bank.CreditMinor,
			"debit_minor":         bank.DebitMinor,
			"credit_debit":        bank.CreditDebit,
		}
		if s11.CapturedAt == nil {
			s11.Note = "Bank row present; value_date not stored. Not a webhook time."
		} else {
			s11.Note = "captured_at is statement value_date, not a bank webhook."
		}
	} else if out.ReconRun && f.Result != nil && (f.Result.Reason == "payout_missing_bank" || f.Result.Reason == "settlement_without_bank" || f.Result.Reason == "captured_missing_settlement") {
		s11.Captured = true
		s11.Source = "reconciliation"
		s11.CapturedAt = timePtr(f.Result.CreatedAt)
		s11.Detail = map[string]any{"found": false, "search": "BANK_SEARCH"}
		s11.Note = "No bank row. Absence recorded at recon decision time."
	}
	out.Steps = append(out.Steps, s11)

	s12 := emptyStep(12, TimelineReconRun, "Zord reconciliation", "Reconciliation has not been run.")
	s13 := emptyStep(13, TimelineReconDecision, "Reconciliation decision", "No reconciliation decision stored.")
	s14 := emptyStep(14, TimelineEvidence, "Evidence + audit trail", "No evidence refs stored.")
	if out.ReconRun && f.Result != nil {
		at := timePtr(f.Result.CreatedAt)
		s12.Captured = true
		s12.CapturedAt = at
		s12.Source = "reconciliation"
		s12.Note = ""
		s12.Detail = map[string]any{"run_id": f.Result.RunID}

		s13.Captured = true
		s13.CapturedAt = at
		s13.Source = "reconciliation"
		s13.Note = ""
		s13.ProviderStatus = f.Result.Status
		s13.Detail = map[string]any{
			"result":             f.Result.Result,
			"reason":             f.Result.Reason,
			"variance_amount":    f.Result.VarianceAmount,
			"bank_credit_proven": f.Result.BankCreditProven,
			"direction":          f.Result.Direction,
			"rail":               f.Result.Rail,
			"two_way":            f.Result.TwoWay,
			"three_way":          f.Result.ThreeWay,
		}

		ids := EvidenceIDList(f.Result.EvidenceRefs)
		if len(ids) > 0 || f.Result.EvidenceRefs.SettlementLineID != "" || f.Result.EvidenceRefs.BankObservationID != "" {
			s14.Captured = true
			s14.CapturedAt = at
			s14.Source = "reconciliation"
			s14.Note = "Refs recorded with the recon decision. Pack seal time is on the evidence service."
			s14.Detail = map[string]any{
				"evidence_ids":  ids,
				"evidence_refs": f.Result.EvidenceRefs,
			}
		}
	}
	out.Steps = append(out.Steps, s12, s13, s14)
	if out.ReconRun && f.Result != nil {
		out.Reconciliation = &TimelineRecon{
			Result:           f.Result.Result,
			Reason:           f.Result.Reason,
			ExpectedAmount:   f.Result.ExpectedAmount,
			ObservedAmount:   f.Result.ObservedAmount,
			VarianceAmount:   f.Result.VarianceAmount,
			Confidence:       f.Result.Confidence,
			BankCreditProven: f.Result.BankCreditProven,
			Direction:        f.Result.Direction,
			Rail:             f.Result.Rail,
			TwoWay:           f.Result.TwoWay,
			ThreeWay:         f.Result.ThreeWay,
		}
	}
	return out
}

func sortObservations(events []ObservationFact) {
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].ObservedAt.IsZero() {
			return false
		}
		if events[j].ObservedAt.IsZero() {
			return true
		}
		return events[i].ObservedAt.Before(events[j].ObservedAt)
	})
}

func firstMatching(events []ObservationFact, pred func(string) bool) (ObservationFact, bool) {
	for _, ev := range events {
		if pred(statusOf(ev)) {
			return ev, true
		}
	}
	return ObservationFact{}, false
}

func firstUTR(events []ObservationFact) (ObservationFact, bool) {
	for _, ev := range events {
		if strings.TrimSpace(ev.RawReference) != "" {
			return ev, true
		}
	}
	return ObservationFact{}, false
}

func settlementForEntity(lines []SettlementLine, entityID, utr string) (SettlementLine, bool) {
	id := strings.TrimSpace(entityID)
	u := strings.TrimSpace(utr)
	for _, l := range lines {
		if id != "" && (l.PaymentID == id || l.EntityID == id) {
			return l, true
		}
	}
	if u == "" {
		return SettlementLine{}, false
	}
	for _, l := range lines {
		if strings.TrimSpace(l.UTR) == u {
			return l, true
		}
	}
	return SettlementLine{}, false
}

func bankForEntity(banks []BankTxn, utr string, result *FinancialResult) (BankTxn, bool) {
	if result != nil && result.EvidenceRefs.BankObservationID != "" {
		want := result.EvidenceRefs.BankObservationID
		for _, b := range banks {
			if b.ID == want || b.BankTxnID == want {
				return b, true
			}
		}
	}
	u := strings.TrimSpace(utr)
	if u == "" {
		return BankTxn{}, false
	}
	for _, b := range banks {
		if strings.TrimSpace(b.UTR) == u || strings.TrimSpace(b.UTRRaw) == u {
			return b, true
		}
	}
	return BankTxn{}, false
}
