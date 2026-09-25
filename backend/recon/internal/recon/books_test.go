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

func TestCashSchedule_RollsExpectedCreditToNextBankingDay(t *testing.T) {
	// Settled Friday 2026-09-04; +3d window → Monday 2026-09-07 (already banking).
	// Force natural hit onto Saturday by settling Wednesday: Wed+3d = Sat → roll to Mon.
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC) // Thursday
	settled := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC) // Wednesday
	// Wed + 3d = Sat 2026-09-05 → next banking Mon 2026-09-07
	sch := BuildCashScheduleOpts(CashScheduleOpts{
		Results: []FinancialResult{{
			EntityType: EntityPayment, EntityID: "pay_roll", ExpectedAmount: 10000, BankCreditProven: false,
		}},
		Lines: []SettlementLine{{
			PaymentID: "pay_roll", LineType: "payment", CreditMinor: 9700, SettledAt: settled,
		}},
		Now:  now,
		Days: 7,
	})
	if sch.Kind != "schedule_projection" {
		t.Fatalf("kind=%s", sch.Kind)
	}
	var monCredit int64
	for _, d := range sch.Days {
		if d.Date == "2026-09-05" || d.Date == "2026-09-06" {
			if d.ExpectedCreditMinor != 0 {
				t.Fatalf("weekend must have zero credit: %+v", d)
			}
		}
		if d.Date == "2026-09-07" {
			monCredit = d.ExpectedCreditMinor
		}
	}
	if monCredit != 9700 {
		t.Fatalf("monday credit=%d days=%+v", monCredit, sch.Days)
	}
}

func TestCashSchedule_HolidayRollsExpectedCredit(t *testing.T) {
	now := time.Date(2026, 1, 23, 12, 0, 0, 0, time.UTC) // Friday
	// Natural hit Monday 2026-01-26 (Republic Day) → roll to Tue 2026-01-27
	settled := time.Date(2026, 1, 23, 9, 0, 0, 0, time.UTC) // Fri; +3d = Mon Jan 26 holiday
	cal := DefaultBankingCalendar()
	sch := BuildCashScheduleOpts(CashScheduleOpts{
		Results: []FinancialResult{{
			EntityType: EntityPayment, EntityID: "pay_hol", BankCreditProven: false,
		}},
		Lines: []SettlementLine{{
			PaymentID: "pay_hol", LineType: "payment", CreditMinor: 5000, SettledAt: settled,
		}},
		Now: now, Days: 7, Calendar: &cal,
	})
	var got int64
	for _, d := range sch.Days {
		if d.Date == "2026-01-26" && d.ExpectedCreditMinor != 0 {
			t.Fatalf("holiday must not hold credit: %+v", d)
		}
		if d.Date == "2026-01-27" {
			got = d.ExpectedCreditMinor
		}
	}
	if got != 5000 {
		t.Fatalf("rolled credit=%d days=%+v", got, sch.Days)
	}
}

func TestCashSchedule_RefundDebitRollsToBankingDay(t *testing.T) {
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC) // Friday
	// Refund observed Saturday → roll debit to Monday
	obs := time.Date(2026, 9, 5, 11, 0, 0, 0, time.UTC) // Saturday
	sch := BuildCashScheduleOpts(CashScheduleOpts{
		Refunds: []RefundFact{{
			RefundID: "rfnd_1", PaymentID: "pay_r", AmountMinor: 1500,
			ProviderStatus: "processed", ObservedAt: obs,
		}},
		Now: now, Days: 7,
	})
	var monDebit int64
	for _, d := range sch.Days {
		if d.Date == "2026-09-05" || d.Date == "2026-09-06" {
			if d.ExpectedDebitMinor != 0 {
				t.Fatalf("weekend debit should be empty: %+v", d)
			}
		}
		if d.Date == "2026-09-07" {
			monDebit = d.ExpectedDebitMinor
		}
	}
	if monDebit != 1500 {
		t.Fatalf("monday debit=%d days=%+v", monDebit, sch.Days)
	}
}

func TestCashSchedule_ProjectionNeverMatchedOrProven(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	sch := BuildCashScheduleOpts(CashScheduleOpts{
		Results: []FinancialResult{{
			EntityType: EntityPayment, EntityID: "pay_p", Result: ResultUnresolved, BankCreditProven: false,
		}},
		Lines: []SettlementLine{{
			PaymentID: "pay_p", LineType: "payment", CreditMinor: 1000,
			SettledAt: time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC),
		}},
		Now: now, Days: 7,
	})
	if sch.Kind != "schedule_projection" {
		t.Fatalf("kind=%s", sch.Kind)
	}
	// Schedule itself has no Result/MATCHED field; guard that we did not flip BankCreditProven accounting.
	if sch.AlreadyReceivedMinor != 0 {
		t.Fatalf("projection must not invent already_received: %d", sch.AlreadyReceivedMinor)
	}
	var totalCredit int64
	for _, d := range sch.Days {
		totalCredit += d.ExpectedCreditMinor
	}
	if totalCredit == 0 {
		t.Fatal("expected a projected credit")
	}
}

func TestCashSchedule_NoDoubleCountDueAtAndInFlightSettlement(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	settled := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC) // Mon; +3d = Thu Sep 4
	due := time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC)
	sch := BuildCashScheduleOpts(CashScheduleOpts{
		Results: []FinancialResult{{
			EntityType: EntityPayment, EntityID: "pay_same", BankCreditProven: false,
		}},
		Lines: []SettlementLine{{
			PaymentID: "pay_same", LineType: "payment", CreditMinor: 8000, SettledAt: settled,
		}},
		Books: []MerchantBookFact{{
			ID: "inv1", InvoiceID: "INV-1", PaymentID: "pay_same", AmountMinor: 8000, DueAt: due,
		}},
		Now: now, Days: 7,
	})
	var totalCredit int64
	for _, d := range sch.Days {
		totalCredit += d.ExpectedCreditMinor
	}
	if totalCredit != 8000 {
		t.Fatalf("double-counted credit total=%d days=%+v", totalCredit, sch.Days)
	}
}

func TestCashSchedule_DueAtUsedWhenNoSettlement(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	// Due Saturday → roll to Monday
	due := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	sch := BuildCashScheduleOpts(CashScheduleOpts{
		Books: []MerchantBookFact{{
			ID: "inv2", InvoiceID: "INV-2", PaymentID: "pay_due", AmountMinor: 4200, DueAt: due,
		}},
		Now: now, Days: 7,
	})
	var mon int64
	for _, d := range sch.Days {
		if d.Date == "2026-09-07" {
			mon = d.ExpectedCreditMinor
		}
	}
	if mon != 4200 {
		t.Fatalf("dueAt rolled credit=%d days=%+v", mon, sch.Days)
	}
}

func TestCashSchedule_RefundObservationAndSettlementLineNotDoubleCounted(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	when := time.Date(2026, 9, 3, 9, 0, 0, 0, time.UTC)
	sch := BuildCashScheduleOpts(CashScheduleOpts{
		Lines: []SettlementLine{{
			PaymentID: "pay_dc", LineType: "refund", AmountMinor: 2000, DebitMinor: 2000, SettledAt: when,
		}},
		Refunds: []RefundFact{{
			RefundID: "rfnd_dc", PaymentID: "pay_dc", AmountMinor: 2000,
			ProviderStatus: "processed", ObservedAt: when,
		}},
		Now: now, Days: 7,
	})
	var totalDebit int64
	for _, d := range sch.Days {
		totalDebit += d.ExpectedDebitMinor
	}
	if totalDebit != 2000 {
		t.Fatalf("refund double-count debit=%d days=%+v", totalDebit, sch.Days)
	}
}

func TestCashSchedule_FailedRefundNotProjected(t *testing.T) {
	now := time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)
	sch := BuildCashScheduleOpts(CashScheduleOpts{
		Refunds: []RefundFact{{
			RefundID: "rfnd_fail", PaymentID: "pay_f", AmountMinor: 999,
			ProviderStatus: "failed", ObservedAt: now,
		}},
		Now: now, Days: 7,
	})
	for _, d := range sch.Days {
		if d.ExpectedDebitMinor != 0 {
			t.Fatalf("failed refund must not project debit: %+v", d)
		}
	}
}
