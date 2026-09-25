package recon

import (
	"testing"
	"time"
)

func TestCashSchedule_SingleAmountOnlyCandidateDropsLabelled(t *testing.T) {
	obs := time.Date(2026, 9, 3, 9, 0, 0, 0, time.UTC)
	s := BuildCashScheduleOpts(CashScheduleOpts{
		Refunds: []RefundFact{{RefundID: "rfnd_ao0001", PaymentID: "pay_ao0001", AmountMinor: 4200,
			Currency: "INR", ProviderStatus: "processed", ObservedAt: obs}},
		Now: schedNow, Days: 7,
		BankLines: []BankTxn{{ID: "bk_ao1", DebitMinor: 4200, Currency: "INR", CreditDebit: "DEBIT",
			ValueDate: time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC)}},
	})
	if _, d := schedTotals(s); d != 0 {
		t.Fatalf("single candidate must drop, debit=%d", d)
	}
	if len(s.Dropped) != 1 || s.Dropped[0].DropBasis != DropBasisAmountOnly ||
		s.Dropped[0].BankObservationID != "bk_ao1" || s.Dropped[0].AmountMinor != 4200 {
		t.Fatalf("drop must be labelled amount_only: %+v", s.Dropped)
	}
}

func TestCashSchedule_MultipleAmountCandidatesKept(t *testing.T) {
	obs := time.Date(2026, 9, 3, 9, 0, 0, 0, time.UTC)
	s := BuildCashScheduleOpts(CashScheduleOpts{
		Refunds: []RefundFact{{RefundID: "rfnd_mc0001", PaymentID: "pay_mc0001", AmountMinor: 4200,
			Currency: "INR", ProviderStatus: "processed", ObservedAt: obs}},
		Now: schedNow, Days: 7,
		BankLines: []BankTxn{
			{ID: "bk_mc1", DebitMinor: 4200, Currency: "INR", CreditDebit: "DEBIT", ValueDate: time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC)},
			{ID: "bk_mc2", DebitMinor: 4200, Currency: "INR", CreditDebit: "DEBIT", ValueDate: time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)},
		},
	})
	if _, d := schedTotals(s); d != 4200 {
		t.Fatalf("two same-amount candidates must keep the refund projected, debit=%d", d)
	}
	if len(s.Dropped) != 0 {
		t.Fatalf("nothing should drop: %+v", s.Dropped)
	}
}

func TestCashSchedule_ReferenceMatchDrops(t *testing.T) {
	obs := time.Date(2026, 9, 3, 9, 0, 0, 0, time.UTC)
	s := BuildCashScheduleOpts(CashScheduleOpts{
		Refunds: []RefundFact{{RefundID: "rfnd_rf0001", PaymentID: "pay_rf0001", AmountMinor: 4200,
			Currency: "INR", ProviderStatus: "processed", ObservedAt: obs}},
		Now: schedNow, Days: 7,
		BankLines: []BankTxn{
			{ID: "bk_rf1", DebitMinor: 4200, Currency: "INR", CreditDebit: "DEBIT", Description: "other"},
			{ID: "bk_rf2", DebitMinor: 4200, Currency: "INR", CreditDebit: "DEBIT", Description: "REFUND rfnd_rf0001"},
		},
	})
	if _, d := schedTotals(s); d != 0 {
		t.Fatalf("reference match must drop even with multiple amount candidates, debit=%d", d)
	}
	if len(s.Dropped) != 1 || s.Dropped[0].DropBasis != DropBasisReference || s.Dropped[0].BankObservationID != "bk_rf2" {
		t.Fatalf("drop must be labelled reference on bk_rf2: %+v", s.Dropped)
	}
}
