package recon

import (
	"strings"
	"time"

	"zord-outcome-engine/internal/poll/providers/razorpay"
)

type TaxBreakdown struct {
	PaymentID          string `json:"payment_id"`
	GrossMinor         int64  `json:"gross_minor"`
	FeeMinor           int64  `json:"fee_minor"`
	TaxMinor           int64  `json:"tax_minor"`
	NetMinor           int64  `json:"net_minor"`
	BankCreditedMinor  int64  `json:"bank_credited_minor"`
	Explained          bool   `json:"explained"`
	Reason             string `json:"reason"`
	Currency           string `json:"currency"`
}

func TaxBreakdownFor(pay PaymentFact, lines []SettlementLine, fr FinancialResult) TaxBreakdown {
	payLines, _ := splitLines(lines)
	var fee, tax int64
	for _, l := range payLines {
		fee += l.FeeMinor
		tax += l.TaxMinor
	}
	net := settlementNet(payLines)
	if net == 0 && pay.AmountMinor > 0 && fee+tax > 0 {
		net = pay.AmountMinor - fee - tax
	}
	out := TaxBreakdown{
		PaymentID:  pay.PaymentID,
		GrossMinor: pay.AmountMinor,
		FeeMinor:   fee,
		TaxMinor:   tax,
		NetMinor:   net,
		Currency:   nzCur(pay.Currency),
	}
	if fr.BankCreditProven {
		out.BankCreditedMinor = fr.ObservedAmount
	}
	if fee > 0 && tax == 0 && (fr.Result == ResultMatched || net+fee == pay.AmountMinor) {
		out.Explained, out.Reason = true, "fee_explained"
	} else if tax > 0 && fee == 0 && (fr.Result == ResultMatched || net+tax == pay.AmountMinor) {
		out.Explained, out.Reason = true, "tax_explained"
	} else if fee+tax > 0 && net+fee+tax == pay.AmountMinor {
		out.Explained, out.Reason = true, "fee_tax_explained"
	} else if fr.Reason == "tax_line_mismatch" {
		out.Explained, out.Reason = false, "tax_line_mismatch"
	} else if fr.Result == ResultVariance || fr.Reason == "partial_settlement" || fr.Reason == "amount_mismatch" {
		out.Explained, out.Reason = false, fr.Reason
	} else if fr.Result == ResultMatched {
		out.Explained, out.Reason = true, fr.Reason
	} else {
		out.Reason = fr.Reason
	}
	return out
}

func nzCur(s string) string {
	if s == "" {
		return "INR"
	}
	return s
}

type ScheduleDay struct {
	Date                string `json:"date"`
	ExpectedCreditMinor int64  `json:"expected_credit_minor"`
	ExpectedDebitMinor  int64  `json:"expected_debit_minor"`
	Count               int    `json:"count"`
}

type CashSchedule struct {
	AsOf                 time.Time     `json:"as_of"`
	HorizonDays          int           `json:"horizon_days"`
	Kind                 string        `json:"kind"`
	Days                 []ScheduleDay `json:"days"`
	UnknownTimingMinor   int64         `json:"unknown_timing_minor"`
	AlreadyReceivedMinor int64         `json:"already_received_minor"`
	Limitations          []string      `json:"limitations"`
}

// CashScheduleOpts extends BuildCashSchedule with merchant DueAt credits,
// refund debits, and an injectable banking calendar. Kind stays
// schedule_projection — this path never sets MATCHED and never treats
// projections as BankCreditProven.
type CashScheduleOpts struct {
	Results  []FinancialResult
	Lines    []SettlementLine
	Payouts  []PayoutFact
	Refunds  []RefundFact
	Books    []MerchantBookFact
	Now      time.Time
	Days     int
	Calendar *BankingCalendar
	// BankLines are existing bank observations (ListBankTxns), already
	// filtered by tenant_id + connector_id. When a matching bank debit has
	// landed, the expected refund debit is dropped; when a matching bank
	// credit has landed, the invoice DueAt credit is skipped. Nil/empty keeps
	// prior behaviour. See cash_schedule_bank.go for the match key.
	BankLines []BankTxn
}

func BuildCashSchedule(results []FinancialResult, lines []SettlementLine, payouts []PayoutFact, now time.Time, days int) CashSchedule {
	return BuildCashScheduleOpts(CashScheduleOpts{
		Results: results, Lines: lines, Payouts: payouts, Now: now, Days: days,
	})
}

func BuildCashScheduleOpts(opts CashScheduleOpts) CashSchedule {
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	days := opts.Days
	if days <= 0 {
		days = 7
	}
	cal := DefaultBankingCalendar()
	if opts.Calendar != nil {
		cal = *opts.Calendar
	}
	asOf := cal.CivilDate(now)
	out := CashSchedule{
		AsOf:        asOf,
		HorizonDays: days,
		Kind:        "schedule_projection",
		Limitations: []string{
			"Not a statistical forecast. Buckets observed settlement dates plus the 3-day bank window and payout SLA.",
			"Expected credits and refund/payout debits roll to the next banking day (weekend + reference holidays).",
			"Projection only: never MATCHED and never BankCreditProven.",
		},
	}
	if len(opts.BankLines) > 0 {
		out.Limitations = append(out.Limitations,
			"Refund debits and invoice DueAt credits already landed at bank (reference, else amount+currency in window) are not projected.")
	}
	windowEnd := asOf.AddDate(0, 0, days)
	byDate := map[string]*ScheduleDay{}
	for i := 0; i < days; i++ {
		d := asOf.AddDate(0, 0, i).Format("2006-01-02")
		byDate[d] = &ScheduleDay{Date: d}
		out.Days = append(out.Days, ScheduleDay{Date: d})
	}

	bucket := func(natural time.Time, credit, debit int64) {
		if credit == 0 && debit == 0 {
			return
		}
		day := cal.NextBankingDay(natural)
		if day.Before(asOf) {
			day = cal.NextBankingDay(asOf)
		}
		key := day.Format("2006-01-02")
		slot, ok := byDate[key]
		if !ok {
			out.UnknownTimingMinor += credit + debit
			return
		}
		slot.ExpectedCreditMinor += credit
		slot.ExpectedDebitMinor += debit
		slot.Count++
	}

	settledAt := map[string]time.Time{}
	netByPay := map[string]int64{}
	for _, l := range opts.Lines {
		pid := l.PaymentID
		if pid == "" {
			pid = l.EntityID
		}
		if l.LineType == "" || l.LineType == "payment" {
			netByPay[pid] += lineNet(l)
			if !l.SettledAt.IsZero() {
				settledAt[pid] = l.SettledAt
			}
		}
	}

	// Payments already proven at bank: cash received, not a projection.
	provenPay := map[string]bool{}
	for _, r := range opts.Results {
		if r.EntityType != EntityPayment {
			continue
		}
		if r.BankCreditProven {
			out.AlreadyReceivedMinor += r.ObservedAmount
			provenPay[r.EntityID] = true
		}
	}

	// In-flight settlement expected credits (prefer these over invoice DueAt).
	creditedFromSettlement := map[string]bool{}
	for _, r := range opts.Results {
		if r.EntityType != EntityPayment {
			continue
		}
		if provenPay[r.EntityID] {
			continue
		}
		net := netByPay[r.EntityID]
		if net == 0 {
			continue
		}
		when, ok := settledAt[r.EntityID]
		if !ok || when.IsZero() {
			out.UnknownTimingMinor += net
			continue
		}
		// expected bank credit: settled_at + 0..3d window, then roll to banking day
		hit := when.Add(defaultDateWindow)
		bucket(hit, net, 0)
		creditedFromSettlement[r.EntityID] = true
	}

	// Invoice DueAt expected credits — skip when same payment already has
	// in-flight settlement credit or bank-proven cash (no double-count).
	// Then skip DueAt credits already bank-proven by a landed bank credit.
	type dueItem struct {
		ev    ExpectedCashEvent
		claim scheduleBankClaim
	}
	var dues []dueItem
	for i, ev := range ObligationsFromMerchant(opts.Books) {
		if ev.Direction != DirectionInbound || ev.AmountMinor == 0 {
			continue
		}
		if ev.EntityID != "" && (creditedFromSettlement[ev.EntityID] || provenPay[ev.EntityID]) {
			continue
		}
		f := opts.Books[i] // ObligationsFromMerchant is 1:1 and order-preserving
		c := scheduleBankClaim{
			amount: ev.AmountMinor, currency: ev.Currency,
			refs:   []string{f.InvoiceID, f.OrderID, f.PaymentID},
			latest: windowEnd,
		}
		if !ev.DueAt.IsZero() {
			// early payment allowed up to one horizon + bank window before due
			c.earliest = ev.DueAt.AddDate(0, 0, -days).Add(-defaultDateWindow)
		}
		dues = append(dues, dueItem{ev: ev, claim: c})
	}
	var dueClaims []scheduleBankClaim
	for _, d := range dues {
		dueClaims = append(dueClaims, d.claim)
	}
	dueLanded := newScheduleBankPool(opts.BankLines, opts.Results, false).claimAll(dueClaims)
	for di, d := range dues {
		if dueLanded[di] {
			continue
		}
		ev := d.ev
		when := ev.DueAt
		if when.IsZero() {
			out.UnknownTimingMinor += ev.AmountMinor
			continue
		}
		bucket(when, ev.AmountMinor, 0)
	}

	provenOut := map[string]bool{}
	for _, r := range opts.Results {
		if r.EntityType == EntityPayout && r.BankCreditProven {
			provenOut[r.EntityID] = true
		}
	}
	for _, po := range opts.Payouts {
		st := razorpay.NormalizePayoutStatus(po.ProviderStatus)
		if razorpay.IsPayoutFailedLike(st) {
			continue
		}
		if provenOut[po.PayoutID] {
			continue
		}
		when := po.ProviderCreatedAt
		if when.IsZero() {
			when = po.FirstObservedAt
		}
		if when.IsZero() {
			when = now
		}
		bucket(when, 0, po.AmountMinor)
	}

	// Refund debits: settlement refund lines first; RefundFact only when that
	// payment+amount pair is not already covered (observation + line = one outflow).
	// Duplicate settlement refund lines (same line ID) count once. Failed /
	// cancelled RefundFacts are excluded before bank matching, so they never
	// project and never consume a bank debit. Candidates whose bank debit has
	// landed are dropped (each bank line drops at most one refund).
	type refundKey struct {
		pay string
		amt int64
	}
	type refundItem struct {
		amt   int64
		when  time.Time
		claim scheduleBankClaim
	}
	var refunds []refundItem
	addRefund := func(amt int64, when time.Time, cur string, refs ...string) {
		c := scheduleBankClaim{amount: amt, currency: cur, refs: refs, latest: windowEnd}
		if !when.IsZero() {
			c.earliest = when.Add(-defaultDateWindow)
		}
		refunds = append(refunds, refundItem{amt: amt, when: when, claim: c})
	}
	coveredRefund := map[refundKey]bool{}
	seenLine := map[string]bool{}
	for _, l := range opts.Lines {
		if !strings.EqualFold(l.LineType, "refund") {
			continue
		}
		if l.ID != "" {
			if seenLine[l.ID] {
				continue
			}
			seenLine[l.ID] = true
		}
		pid := l.PaymentID
		if pid == "" {
			pid = l.EntityID
		}
		amt := l.DebitMinor
		if amt == 0 {
			amt = l.AmountMinor
		}
		if amt < 0 {
			amt = -amt
		}
		if amt == 0 {
			continue
		}
		addRefund(amt, l.SettledAt, l.Currency, l.EntityID, pid)
		coveredRefund[refundKey{pid, amt}] = true
	}
	for _, rf := range opts.Refunds {
		if !refundCountsAsExpectedDebit(rf) {
			continue
		}
		if rf.AmountMinor <= 0 {
			continue
		}
		if coveredRefund[refundKey{rf.PaymentID, rf.AmountMinor}] {
			continue
		}
		addRefund(rf.AmountMinor, rf.ObservedAt, rf.Currency, rf.RefundID, rf.PaymentID)
		coveredRefund[refundKey{rf.PaymentID, rf.AmountMinor}] = true
	}
	var refundClaims []scheduleBankClaim
	for _, r := range refunds {
		refundClaims = append(refundClaims, r.claim)
	}
	refundLanded := newScheduleBankPool(opts.BankLines, opts.Results, true).claimAll(refundClaims)
	for ri, r := range refunds {
		if refundLanded[ri] {
			continue
		}
		if r.when.IsZero() {
			out.UnknownTimingMinor += r.amt
			continue
		}
		bucket(r.when, 0, r.amt)
	}

	for i := range out.Days {
		if s, ok := byDate[out.Days[i].Date]; ok {
			out.Days[i] = *s
		}
	}
	return out
}

// refundCountsAsExpectedDebit excludes failed/cancelled (no money movement).
// processed/pending remain expected outflows until a bank debit proves them —
// never treated as MATCHED or BankCreditProven here.
func refundCountsAsExpectedDebit(r RefundFact) bool {
	switch strings.ToLower(strings.TrimSpace(r.ProviderStatus)) {
	case "failed", "cancelled", "canceled":
		return false
	default:
		return true
	}
}

type LedgerLine struct {
	Side       string `json:"side"`
	Account    string `json:"account"`
	AmountMinor int64 `json:"amount_minor"`
	Source     string `json:"source"`
	EvidenceID string `json:"evidence_id,omitempty"`
}

type Ledger struct {
	EntityType  string       `json:"entity_type"`
	EntityID    string       `json:"entity_id"`
	Lines       []LedgerLine `json:"lines"`
	Balanced    bool         `json:"balanced"`
	Limitations []string     `json:"limitations"`
}

func LedgerForPayment(pay PaymentFact, lines []SettlementLine, fr FinancialResult, refunds []RefundFact) Ledger {
	out := Ledger{
		EntityType: EntityPayment,
		EntityID:   pay.PaymentID,
		Limitations: []string{"Not a statutory GL. Derived from Razorpay and bank observations."},
	}
	if pay.AmountMinor > 0 {
		out.Lines = append(out.Lines, LedgerLine{
			Side: "debit", Account: "receivable", AmountMinor: pay.AmountMinor,
			Source: "canonical_payment", EvidenceID: pay.ID,
		})
	}
	payLines, _ := splitLines(lines)
	var fee, tax int64
	var lineID string
	for _, l := range payLines {
		fee += l.FeeMinor
		tax += l.TaxMinor
		if lineID == "" {
			lineID = l.ID
		}
	}
	if fee > 0 {
		out.Lines = append(out.Lines, LedgerLine{Side: "credit", Account: "fee", AmountMinor: fee, Source: "settlement_line", EvidenceID: lineID})
	}
	if tax > 0 {
		out.Lines = append(out.Lines, LedgerLine{Side: "credit", Account: "tax", AmountMinor: tax, Source: "settlement_line", EvidenceID: lineID})
	}
	if fr.BankCreditProven && fr.ObservedAmount > 0 {
		out.Lines = append(out.Lines, LedgerLine{
			Side: "debit", Account: "cash", AmountMinor: fr.ObservedAmount,
			Source: "bank", EvidenceID: fr.EvidenceRefs.BankObservationID,
		})
		out.Lines = append(out.Lines, LedgerLine{
			Side: "credit", Account: "receivable", AmountMinor: fr.ObservedAmount,
			Source: "bank", EvidenceID: fr.EvidenceRefs.BankObservationID,
		})
	}
	for _, rf := range refunds {
		if rf.AmountMinor <= 0 {
			continue
		}
		out.Lines = append(out.Lines, LedgerLine{
			Side: "credit", Account: "refund", AmountMinor: rf.AmountMinor,
			Source: "refund_observation", EvidenceID: rf.RefundID,
		})
	}
	var debit, credit int64
	for _, l := range out.Lines {
		if l.Side == "debit" {
			debit += l.AmountMinor
		} else {
			credit += l.AmountMinor
		}
	}
	out.Balanced = debit == credit
	if !out.Balanced {
		gap := debit - credit
		if gap < 0 {
			gap = -gap
		}
		out.Lines = append(out.Lines, LedgerLine{
			Side: "debit", Account: "unresolved_exposure", AmountMinor: gap, Source: "derived",
		})
		if debit < credit {
			out.Lines[len(out.Lines)-1].Side = "debit"
		}
		// recompute
		debit, credit = 0, 0
		for _, l := range out.Lines {
			if l.Side == "debit" {
				debit += l.AmountMinor
			} else {
				credit += l.AmountMinor
			}
		}
		out.Balanced = debit == credit
	}
	return out
}
