package recon

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func schedTotals(s CashSchedule) (credit, debit int64) {
	for _, d := range s.Days {
		credit += d.ExpectedCreditMinor
		debit += d.ExpectedDebitMinor
	}
	return credit, debit
}

var schedNow = time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC) // Thu

func TestCashSchedule_RefundDroppedWhenBankDebitLands(t *testing.T) {
	obs := time.Date(2026, 9, 3, 9, 0, 0, 0, time.UTC)
	refunds := []RefundFact{{
		RefundID: "rfnd_bank1", PaymentID: "pay_b1", AmountMinor: 1500, Currency: "INR",
		ProviderStatus: "processed", ObservedAt: obs,
	}}
	base := BuildCashScheduleOpts(CashScheduleOpts{Refunds: refunds, Now: schedNow, Days: 7})
	if _, d := schedTotals(base); d != 1500 {
		t.Fatalf("control debit=%d", d)
	}
	// Amount+currency fallback within window.
	landed := BuildCashScheduleOpts(CashScheduleOpts{
		Refunds: refunds, Now: schedNow, Days: 7,
		BankLines: []BankTxn{{ID: "b1", DebitMinor: 1500, Currency: "INR", CreditDebit: "DEBIT",
			ValueDate: time.Date(2026, 9, 4, 0, 0, 0, 0, time.UTC)}},
	})
	if _, d := schedTotals(landed); d != 0 {
		t.Fatalf("refund must drop once bank debit lands, debit=%d", d)
	}
	if landed.Kind != "schedule_projection" || landed.AlreadyReceivedMinor != 0 {
		t.Fatalf("bank debit must not become received cash: %+v", landed)
	}
	// Currency mismatch: not dropped.
	usd := BuildCashScheduleOpts(CashScheduleOpts{
		Refunds: refunds, Now: schedNow, Days: 7,
		BankLines: []BankTxn{{ID: "b1", DebitMinor: 1500, Currency: "USD", CreditDebit: "DEBIT"}},
	})
	if _, d := schedTotals(usd); d != 1500 {
		t.Fatalf("currency mismatch must not drop, debit=%d", d)
	}
	// Bank debit before the refund window: not dropped.
	early := BuildCashScheduleOpts(CashScheduleOpts{
		Refunds: refunds, Now: schedNow, Days: 7,
		BankLines: []BankTxn{{ID: "b1", DebitMinor: 1500, CreditDebit: "DEBIT",
			ValueDate: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)}},
	})
	if _, d := schedTotals(early); d != 1500 {
		t.Fatalf("out-of-window debit must not drop, debit=%d", d)
	}
	// Bank debit already cited as payout evidence is not reused.
	cited := BuildCashScheduleOpts(CashScheduleOpts{
		Refunds: refunds, Now: schedNow, Days: 7,
		Results: []FinancialResult{{EntityType: EntityPayout, EntityID: "pout_1", BankCreditProven: true,
			EvidenceRefs: EvidenceRefs{BankObservationID: "b1"}}},
		BankLines: []BankTxn{{ID: "b1", DebitMinor: 1500, CreditDebit: "DEBIT"}},
	})
	if _, d := schedTotals(cited); d != 1500 {
		t.Fatalf("payout-cited bank debit must not drop refund, debit=%d", d)
	}
	// One bank debit, two equal refunds on different payments: exactly one drops.
	two := BuildCashScheduleOpts(CashScheduleOpts{
		Refunds: append(refunds, RefundFact{RefundID: "rfnd_bank2", PaymentID: "pay_b2", AmountMinor: 1500,
			ProviderStatus: "processed", ObservedAt: obs}),
		Now: schedNow, Days: 7,
		BankLines: []BankTxn{{ID: "b1", DebitMinor: 1500, CreditDebit: "DEBIT"}},
	})
	if _, d := schedTotals(two); d != 1500 {
		t.Fatalf("one bank line must drop exactly one refund, debit=%d", d)
	}
	// Reference match (refund id in description) wins over plain amount match.
	ref := BuildCashScheduleOpts(CashScheduleOpts{
		Refunds: []RefundFact{
			{RefundID: "rfnd_aaaaaa", PaymentID: "pay_x1", AmountMinor: 700, ProviderStatus: "processed"},
			{RefundID: "rfnd_bbbbbb", PaymentID: "pay_x2", AmountMinor: 700, ProviderStatus: "processed", ObservedAt: obs},
		},
		Now: schedNow, Days: 7,
		BankLines: []BankTxn{{ID: "b9", DebitMinor: 700, CreditDebit: "DEBIT", Description: "REFUND rfnd_bbbbbb"}},
	})
	// rfnd_bbbbbb (timed) dropped by reference; rfnd_aaaaaa (untimed) stays unknown timing.
	if _, d := schedTotals(ref); d != 0 || ref.UnknownTimingMinor != 700 {
		t.Fatalf("reference match wrong: debit=%d unknown=%d", d, ref.UnknownTimingMinor)
	}
}

func TestCashSchedule_ExpectedNotSettled(t *testing.T) {
	settled := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	results := []FinancialResult{{
		EntityType: EntityPayment, EntityID: "pay_exp", Result: ResultUnresolved, BankCreditProven: false,
	}}
	before := append([]FinancialResult{}, results...)
	sch := BuildCashScheduleOpts(CashScheduleOpts{
		Results: results,
		Lines: []SettlementLine{{PaymentID: "pay_exp", LineType: "payment", CreditMinor: 5000,
			Currency: "INR", SettledAt: settled}},
		// An unrelated same-amount bank credit must not "settle" the projection.
		BankLines: []BankTxn{{ID: "bx", CreditMinor: 5000, Currency: "INR", CreditDebit: "CREDIT",
			ValueDate: time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)}},
		Now: schedNow, Days: 7,
	})
	if sch.Kind != "schedule_projection" {
		t.Fatalf("kind=%s", sch.Kind)
	}
	if sch.AlreadyReceivedMinor != 0 {
		t.Fatalf("expected credit must not become bank cash: %d", sch.AlreadyReceivedMinor)
	}
	if c, _ := schedTotals(sch); c != 5000 {
		t.Fatalf("expected credit must stay projected, credit=%d", c)
	}
	if !reflect.DeepEqual(results, before) || results[0].Result == ResultMatched || results[0].BankCreditProven {
		t.Fatalf("projection must not mutate verdicts: %+v", results)
	}
	joined := strings.Join(sch.Limitations, " ")
	if !strings.Contains(joined, "never MATCHED") || !strings.Contains(joined, "never BankCreditProven") {
		t.Fatalf("limitations must disclaim settlement: %v", sch.Limitations)
	}
}

func TestCashSchedule_DueAtSkippedWhenBankProven(t *testing.T) {
	due := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	books := []MerchantBookFact{{ID: "inv_b", InvoiceID: "INV-2002", PaymentID: "pay_inv", AmountMinor: 4200, Currency: "INR", DueAt: due}}
	base := BuildCashScheduleOpts(CashScheduleOpts{Books: books, Now: schedNow, Days: 7})
	if c, _ := schedTotals(base); c != 4200 {
		t.Fatalf("control credit=%d", c)
	}
	byRef := BuildCashScheduleOpts(CashScheduleOpts{
		Books: books, Now: schedNow, Days: 7,
		BankLines: []BankTxn{{ID: "bc1", CreditMinor: 4200, Currency: "INR", CreditDebit: "CREDIT",
			Description: "NEFT CR INV-2002 ACME", ValueDate: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)}},
	})
	if c, _ := schedTotals(byRef); c != 0 {
		t.Fatalf("invoice reference in bank credit must skip DueAt, credit=%d", c)
	}
	byAmt := BuildCashScheduleOpts(CashScheduleOpts{
		Books: books, Now: schedNow, Days: 7,
		BankLines: []BankTxn{{ID: "bc2", CreditMinor: 4200, CreditDebit: "CREDIT",
			ValueDate: time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)}},
	})
	if c, _ := schedTotals(byAmt); c != 0 {
		t.Fatalf("in-window amount+currency credit must skip DueAt, credit=%d", c)
	}
	if byAmt.AlreadyReceivedMinor != 0 {
		t.Fatalf("skip must not invent already_received: %d", byAmt.AlreadyReceivedMinor)
	}
	// Out-of-window amount-only credit does not skip.
	old := BuildCashScheduleOpts(CashScheduleOpts{
		Books: books, Now: schedNow, Days: 7,
		BankLines: []BankTxn{{ID: "bc3", CreditMinor: 4200, CreditDebit: "CREDIT",
			ValueDate: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)}},
	})
	if c, _ := schedTotals(old); c != 4200 {
		t.Fatalf("stale credit must not skip, credit=%d", c)
	}
	// Bank credit already cited by a settlement result is not reused.
	cited := BuildCashScheduleOpts(CashScheduleOpts{
		Books: books, Now: schedNow, Days: 7,
		Results: []FinancialResult{{EntityType: EntityPayment, EntityID: "pay_other", BankCreditProven: true,
			ObservedAmount: 4200, EvidenceRefs: EvidenceRefs{BankObservationID: "bc2"}}},
		BankLines: []BankTxn{{ID: "bc2", CreditMinor: 4200, CreditDebit: "CREDIT",
			ValueDate: time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)}},
	})
	if c, _ := schedTotals(cited); c != 4200 {
		t.Fatalf("cited bank credit must not skip another invoice, credit=%d", c)
	}
	// Bank debit never proves an inbound invoice.
	debit := BuildCashScheduleOpts(CashScheduleOpts{
		Books: books, Now: schedNow, Days: 7,
		BankLines: []BankTxn{{ID: "bd", DebitMinor: 4200, CreditDebit: "DEBIT"}},
	})
	if c, _ := schedTotals(debit); c != 4200 {
		t.Fatalf("debit must not skip invoice credit, credit=%d", c)
	}
}

func TestCashSchedule_FailedRefundNotDroppedAgainstBankDebit(t *testing.T) {
	sch := BuildCashScheduleOpts(CashScheduleOpts{
		Refunds: []RefundFact{
			{RefundID: "rfnd_failx", PaymentID: "pay_f1", AmountMinor: 999, ProviderStatus: "failed", ObservedAt: schedNow},
			{RefundID: "rfnd_okxxx", PaymentID: "pay_f2", AmountMinor: 999, ProviderStatus: "processed", ObservedAt: schedNow},
		},
		BankLines: []BankTxn{{ID: "bf", DebitMinor: 999, CreditDebit: "DEBIT"}},
		Now:       schedNow, Days: 7,
	})
	// Failed refund never projected; the single bank debit drops the processed one.
	if _, d := schedTotals(sch); d != 0 || sch.UnknownTimingMinor != 0 {
		t.Fatalf("failed refund leaked: debit=%d unknown=%d", d, sch.UnknownTimingMinor)
	}
	onlyFailed := BuildCashScheduleOpts(CashScheduleOpts{
		Refunds:   []RefundFact{{RefundID: "rfnd_failx", PaymentID: "pay_f1", AmountMinor: 999, ProviderStatus: "failed", ObservedAt: schedNow}},
		BankLines: []BankTxn{{ID: "bf", DebitMinor: 999, CreditDebit: "DEBIT"}},
		Now:       schedNow, Days: 7,
	})
	if _, d := schedTotals(onlyFailed); d != 0 {
		t.Fatalf("failed refund must not appear as outflow: %d", d)
	}
}

func TestCashSchedule_DuplicateRefundLineNoDoubleCountOrDoubleDrop(t *testing.T) {
	when := time.Date(2026, 9, 3, 9, 0, 0, 0, time.UTC)
	dup := SettlementLine{ID: "sl_r1", EntityID: "rfnd_dup01", PaymentID: "pay_dup", LineType: "refund",
		AmountMinor: 2000, DebitMinor: 2000, SettledAt: when}
	lines := []SettlementLine{dup, dup}
	obsFact := []RefundFact{{RefundID: "rfnd_dup01", PaymentID: "pay_dup", AmountMinor: 2000, ProviderStatus: "processed", ObservedAt: when}}

	noBank := BuildCashScheduleOpts(CashScheduleOpts{Lines: lines, Refunds: obsFact, Now: schedNow, Days: 7})
	if _, d := schedTotals(noBank); d != 2000 {
		t.Fatalf("duplicate refund line double-counted: %d", d)
	}
	oneBank := BuildCashScheduleOpts(CashScheduleOpts{
		Lines: lines, Refunds: obsFact, Now: schedNow, Days: 7,
		BankLines: []BankTxn{{ID: "bd1", DebitMinor: 2000, CreditDebit: "DEBIT"}},
	})
	if _, d := schedTotals(oneBank); d != 0 {
		t.Fatalf("landed duplicate refund should drop to 0: %d", d)
	}
	// Two genuinely distinct refund lines, one bank debit: only one drops.
	other := dup
	other.ID, other.EntityID = "sl_r2", "rfnd_dup02"
	distinct := BuildCashScheduleOpts(CashScheduleOpts{
		Lines: []SettlementLine{dup, other}, Now: schedNow, Days: 7,
		BankLines: []BankTxn{{ID: "bd1", DebitMinor: 2000, CreditDebit: "DEBIT"}},
	})
	if _, d := schedTotals(distinct); d != 2000 {
		t.Fatalf("one bank line must not double-drop: %d", d)
	}
}

func TestCashSchedule_NilAndEmptyBankLinesUnchanged(t *testing.T) {
	opts := CashScheduleOpts{
		Lines:   []SettlementLine{{PaymentID: "pay_n", LineType: "refund", DebitMinor: 300, SettledAt: schedNow}},
		Refunds: []RefundFact{{RefundID: "rfnd_n", PaymentID: "pay_m", AmountMinor: 400, ProviderStatus: "processed", ObservedAt: schedNow}},
		Books:   []MerchantBookFact{{ID: "inv_n", PaymentID: "pay_q", AmountMinor: 900, DueAt: schedNow}},
		Now:     schedNow, Days: 7,
	}
	a := BuildCashScheduleOpts(opts)
	opts.BankLines = []BankTxn{}
	b := BuildCashScheduleOpts(opts)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("nil vs empty bank lines differ:\n%+v\n%+v", a, b)
	}
}
