package recon

import (
	"testing"
	"time"
)

func scheduleDebitTotal(s CashSchedule) int64 {
	total := s.UnknownTimingMinor
	for _, d := range s.Days {
		total += d.ExpectedDebitMinor
	}
	return total
}

// TestBooks_ReversedPayoutNotNetOutflow guards D15/L5: a reversed (or failed,
// cancelled, rejected) payout is never an expected debit. Only the pending
// payout counts. The failed refund and the duplicate settlement refund line
// must not add or double count (house rule).
func TestBooks_ReversedPayoutNotNetOutflow(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC) // Thursday
	sch := BuildCashScheduleOpts(CashScheduleOpts{
		Payouts: []PayoutFact{
			{PayoutID: "pout_rev", ProviderStatus: "reversed", AmountMinor: 100, ProviderCreatedAt: now},
			{PayoutID: "pout_fail", ProviderStatus: "failed", AmountMinor: 200, ProviderCreatedAt: now},
			{PayoutID: "pout_canc", ProviderStatus: "cancelled", AmountMinor: 300, ProviderCreatedAt: now},
			{PayoutID: "pout_rej", ProviderStatus: "rejected", AmountMinor: 400, ProviderCreatedAt: now},
			{PayoutID: "pout_pend", ProviderStatus: "pending", AmountMinor: 5000, ProviderCreatedAt: now},
		},
		Lines: []SettlementLine{
			// same refund line delivered twice → counts once
			{ID: "sl_rf1", PaymentID: "pay_1", LineType: "refund", DebitMinor: 700, SettledAt: now},
			{ID: "sl_rf1", PaymentID: "pay_1", LineType: "refund", DebitMinor: 700, SettledAt: now},
		},
		Refunds: []RefundFact{
			{RefundID: "rfnd_failed", PaymentID: "pay_2", AmountMinor: 900, ProviderStatus: "failed", ObservedAt: now},
		},
		Now: now, Days: 7,
	})
	if got, want := scheduleDebitTotal(sch), int64(5000+700); got != want {
		t.Fatalf("expected debit total = %d, want %d (pending payout + one refund line); days=%+v", got, want, sch.Days)
	}
	if sch.Days[0].ExpectedDebitMinor != 5700 {
		t.Fatalf("day0 debit = %d, want 5700", sch.Days[0].ExpectedDebitMinor)
	}
}

// TestBooks_PendingPayoutStillExpectedDebit guards D16: pending/processing
// payouts stay expected debits until a bank debit lands.
func TestBooks_PendingPayoutStillExpectedDebit(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	for _, st := range []string{"pending", "scheduled", "queued", "processing", "processed"} {
		sch := BuildCashScheduleOpts(CashScheduleOpts{
			Payouts: []PayoutFact{{PayoutID: "pout_1", ProviderStatus: st, AmountMinor: 25000, ProviderCreatedAt: now}},
			Now:     now, Days: 7,
		})
		if got := scheduleDebitTotal(sch); got != 25000 {
			t.Fatalf("status %s: expected debit = %d, want 25000", st, got)
		}
	}
	// Once the bank debit is proven, it is no longer projected.
	sch := BuildCashScheduleOpts(CashScheduleOpts{
		Payouts: []PayoutFact{{PayoutID: "pout_1", ProviderStatus: "processing", AmountMinor: 25000, ProviderCreatedAt: now}},
		Results: []FinancialResult{{EntityType: EntityPayout, EntityID: "pout_1", BankCreditProven: true}},
		Now:     now, Days: 7,
	})
	if got := scheduleDebitTotal(sch); got != 0 {
		t.Fatalf("bank-proven payout still projected: %d", got)
	}
}
