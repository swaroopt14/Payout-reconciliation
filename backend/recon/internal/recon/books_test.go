package recon

import (
	"testing"
	"time"
)

func TestTaxBreakdownFeeExplained(t *testing.T) {
	pay := PaymentFact{PaymentID: "pay_1", AmountMinor: 10000, Currency: "INR"}
	lines := []SettlementLine{{
		ID: "sl1", PaymentID: "pay_1", LineType: "payment", AmountMinor: 10000, CreditMinor: 9728, FeeMinor: 272,
	}}
	tb := TaxBreakdownFor(pay, lines, FinancialResult{Result: ResultMatched, Reason: "captured_settlement_exact_bank", BankCreditProven: true, ObservedAmount: 9728})
	if !tb.Explained || tb.FeeMinor != 272 || tb.NetMinor != 9728 {
		t.Fatalf("%+v", tb)
	}
}

func TestCashScheduleIncludesOpenPayoutDebit(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	sch := BuildCashSchedule(nil, nil, []PayoutFact{{
		PayoutID: "pout_open", ProviderStatus: "queued", AmountMinor: 25000, ProviderCreatedAt: now,
	}}, now, 7)
	if sch.Days[0].ExpectedDebitMinor != 25000 {
		t.Fatalf("debit=%d days=%+v", sch.Days[0].ExpectedDebitMinor, sch.Days[0])
	}
}

func TestCashScheduleUnknownWhenNoSettledAt(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	sch := BuildCashSchedule([]FinancialResult{{
		EntityType: EntityPayment, EntityID: "pay_a", ExpectedAmount: 1000, BankCreditProven: false,
	}}, []SettlementLine{{PaymentID: "pay_a", LineType: "payment", CreditMinor: 1000}}, nil, now, 7)
	if sch.Kind != "schedule_projection" {
		t.Fatalf("kind=%s", sch.Kind)
	}
	if sch.UnknownTimingMinor != 1000 {
		t.Fatalf("unknown=%d", sch.UnknownTimingMinor)
	}
	if len(sch.Days) != 7 {
		t.Fatalf("days=%d", len(sch.Days))
	}
}

func TestLedgerDoesNotInventBankCash(t *testing.T) {
	led := LedgerForPayment(
		PaymentFact{ID: "cp", PaymentID: "pay_x", AmountMinor: 10000},
		[]SettlementLine{{ID: "sl", PaymentID: "pay_x", LineType: "payment", CreditMinor: 9728, FeeMinor: 272}},
		FinancialResult{BankCreditProven: false},
		nil,
	)
	for _, l := range led.Lines {
		if l.Account == "cash" {
			t.Fatal("must not post cash without bank_credit_proven")
		}
	}
}

func TestFailedUsesRefundObservation(t *testing.T) {
	got := ReconcilePayment(FinancialInput{
		Payment: PaymentFact{PaymentID: "pay_rf", CanonicalStatus: PaymentFailed, AmountMinor: 2000},
		Refunds: []RefundFact{{RefundID: "rfnd_1", PaymentID: "pay_rf", AmountMinor: 2000, ProviderStatus: "processed"}},
	})
	if got.Result != ResultMatched || got.Reason != "failed_refund_no_bank_movement" {
		t.Fatalf("%s %s", got.Result, got.Reason)
	}
}
