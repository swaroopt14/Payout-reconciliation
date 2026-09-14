package recon

import (
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
	Date                 string `json:"date"`
	ExpectedCreditMinor  int64  `json:"expected_credit_minor"`
	ExpectedDebitMinor   int64  `json:"expected_debit_minor"`
	Count                int    `json:"count"`
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

func BuildCashSchedule(results []FinancialResult, lines []SettlementLine, payouts []PayoutFact, now time.Time, days int) CashSchedule {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if days <= 0 {
		days = 7
	}
	asOf := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	out := CashSchedule{
		AsOf:        asOf,
		HorizonDays: days,
		Kind:        "schedule_projection",
		Limitations: []string{"Not a statistical forecast. Buckets observed settlement dates plus the 3-day bank window and payout SLA."},
	}
	byDate := map[string]*ScheduleDay{}
	for i := 0; i < days; i++ {
		d := asOf.AddDate(0, 0, i).Format("2006-01-02")
		byDate[d] = &ScheduleDay{Date: d}
		out.Days = append(out.Days, ScheduleDay{Date: d})
	}
	settledAt := map[string]time.Time{}
	netByPay := map[string]int64{}
	for _, l := range lines {
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
	for _, r := range results {
		if r.EntityType != EntityPayment {
			continue
		}
		if r.BankCreditProven {
			out.AlreadyReceivedMinor += r.ObservedAmount
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
		// expected bank credit: settled_at + 0..3d window, bucket the end of the window clipped to horizon
		hit := when.Add(defaultDateWindow)
		day := time.Date(hit.Year(), hit.Month(), hit.Day(), 0, 0, 0, 0, time.UTC)
		if day.Before(asOf) {
			day = asOf
		}
		key := day.Format("2006-01-02")
		slot, ok := byDate[key]
		if !ok {
			out.UnknownTimingMinor += net
			continue
		}
		slot.ExpectedCreditMinor += net
		slot.Count++
	}
	provenOut := map[string]bool{}
	for _, r := range results {
		if r.EntityType == EntityPayout && r.BankCreditProven {
			provenOut[r.EntityID] = true
		}
	}
	for _, po := range payouts {
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
		day := time.Date(when.Year(), when.Month(), when.Day(), 0, 0, 0, 0, time.UTC)
		if day.Before(asOf) {
			day = asOf
		}
		key := day.Format("2006-01-02")
		slot, ok := byDate[key]
		if !ok {
			out.UnknownTimingMinor += po.AmountMinor
			continue
		}
		slot.ExpectedDebitMinor += po.AmountMinor
		slot.Count++
	}
	for i := range out.Days {
		if s, ok := byDate[out.Days[i].Date]; ok {
			out.Days[i] = *s
		}
	}
	return out
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
