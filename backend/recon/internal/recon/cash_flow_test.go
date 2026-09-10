package recon

import "testing"

func TestTwoWayThreeWay_CapturedExactBank(t *testing.T) {
	got := ReconcilePayment(FinancialInput{
		Payment: PaymentFact{
			PaymentID: "pay_001", CanonicalStatus: PaymentCaptured, Captured: true,
			AmountMinor: 10000, Method: "upi",
		},
		Lines: []SettlementLine{{
			ID: "sl1", PaymentID: "pay_001", LineType: "payment", AmountMinor: 10000, CreditMinor: 9728, FeeMinor: 272, Currency: "INR",
		}},
		Decisions: []SettlementBankDecision{{
			ID: "d1", SettlementLineID: "sl1", BankObservationID: "b1", State: BankMatchExact, Confidence: 0.99,
			Evidence: map[string]any{"bank_credit_minor": int64(9728)},
		}},
		Banks: []BankTxn{{ID: "b1", UTR: "UTR123", CreditMinor: 9728, CreditDebit: "CREDIT", Currency: "INR"}},
	})
	if got.Result != ResultMatched {
		t.Fatalf("top-level result=%s reason=%s", got.Result, got.Reason)
	}
	if got.Direction != DirectionInbound || got.Rail != RailUPI {
		t.Fatalf("direction=%s rail=%s", got.Direction, got.Rail)
	}
	if got.TwoWay.Result != ResultMatched || got.TwoWay.Reason != "merchant_psp_settled" {
		t.Fatalf("two_way=%+v", got.TwoWay)
	}
	if got.ThreeWay.Result != ResultMatched || got.ThreeWay.Reason != "merchant_psp_bank" {
		t.Fatalf("three_way=%+v", got.ThreeWay)
	}
}

func TestTwoWayThreeWay_SettlementWithoutBank(t *testing.T) {
	got := ReconcilePayment(FinancialInput{
		Payment: PaymentFact{
			PaymentID: "pay_sb", CanonicalStatus: PaymentCaptured, Captured: true,
			AmountMinor: 10000, Method: "card",
		},
		Lines: []SettlementLine{{
			ID: "sl1", PaymentID: "pay_sb", LineType: "payment", AmountMinor: 10000, CreditMinor: 9728, FeeMinor: 272, Currency: "INR",
		}},
	})
	if got.Result != ResultUnresolved || got.Reason != "settlement_without_bank" {
		t.Fatalf("top-level must stay UNRESOLVED settlement_without_bank, got %s %s", got.Result, got.Reason)
	}
	if got.Exception == nil {
		t.Fatal("exception required")
	}
	if got.TwoWay.Result != ResultMatched {
		t.Fatalf("2-way PSP books agree: %+v", got.TwoWay)
	}
	if got.ThreeWay.Result != ResultUnresolved || got.ThreeWay.Reason != "settlement_without_bank" {
		t.Fatalf("3-way needs bank: %+v", got.ThreeWay)
	}
	if got.Rail != RailCard {
		t.Fatalf("rail=%s", got.Rail)
	}
}

func TestTwoWayThreeWay_HighConfidenceBankNotProven(t *testing.T) {
	got := ReconcilePayment(FinancialInput{
		Payment: PaymentFact{
			PaymentID: "pay_hc", CanonicalStatus: PaymentCaptured, Captured: true, AmountMinor: 10000,
		},
		Lines: []SettlementLine{{
			ID: "sl1", PaymentID: "pay_hc", LineType: "payment", AmountMinor: 10000, CreditMinor: 9728, FeeMinor: 272, Currency: "INR",
		}},
		Decisions: []SettlementBankDecision{{
			ID: "d1", SettlementLineID: "sl1", BankObservationID: "b1", State: BankMatchHighConfidence, Confidence: 0.72,
		}},
	})
	if got.Result != ResultMatched {
		t.Fatalf("top-level stays MATCHED, got %s", got.Result)
	}
	if got.BankCreditProven {
		t.Fatal("high-confidence must not claim bank_credit_proven")
	}
	if got.TwoWay.Result != ResultMatched {
		t.Fatalf("two_way=%+v", got.TwoWay)
	}
	if got.ThreeWay.Result != ResultUnresolved || got.ThreeWay.Reason != "bank_not_proven" {
		t.Fatalf("three_way=%+v", got.ThreeWay)
	}
}

func TestTwoWayThreeWay_PayoutMissingBank(t *testing.T) {
	got := ReconcilePayout(PayoutInput{
		Payout: PayoutFact{PayoutID: "pout_m", ProviderStatus: "processed", AmountMinor: 99, Mode: "IMPS"},
	})
	if got.Result != ResultUnresolved || got.Reason != "payout_missing_bank" {
		t.Fatalf("%s %s", got.Result, got.Reason)
	}
	if got.Direction != DirectionOutbound || got.Rail != RailIMPS {
		t.Fatalf("direction=%s rail=%s", got.Direction, got.Rail)
	}
	if got.TwoWay.Result != ResultMatched || got.TwoWay.Reason != "merchant_psp_payout" {
		t.Fatalf("two_way=%+v", got.TwoWay)
	}
	if got.ThreeWay.Result != ResultUnresolved || got.ThreeWay.Reason != "payout_missing_bank" {
		t.Fatalf("three_way=%+v", got.ThreeWay)
	}
}

func TestTwoWayThreeWay_ProcessedExactDebit(t *testing.T) {
	got := ReconcilePayout(PayoutInput{
		Payout: PayoutFact{
			PayoutID: "pout_001", ProviderStatus: "processed", AmountMinor: 25000, Currency: "INR", UTR: "UTRPO1", Mode: "UPI",
		},
		Banks: []BankTxn{{ID: "bdebit1", UTR: "UTRPO1", DebitMinor: 25000, CreditDebit: "DEBIT", Currency: "INR"}},
	})
	if got.Result != ResultMatched {
		t.Fatalf("result=%s", got.Result)
	}
	if got.TwoWay.Result != ResultMatched || got.ThreeWay.Result != ResultMatched {
		t.Fatalf("two=%+v three=%+v", got.TwoWay, got.ThreeWay)
	}
	if got.ThreeWay.Reason != "merchant_psp_bank" {
		t.Fatalf("three reason=%s", got.ThreeWay.Reason)
	}
}

func TestReconJSONIncludesLegs(t *testing.T) {
	fr := FinancialResult{
		EntityType: EntityPayment, Result: ResultUnresolved, Reason: "settlement_without_bank",
		Confidence: 0.7, Rail: RailCard,
	}
	raw, ok := ReconJSON(fr).(map[string]any)
	if !ok {
		t.Fatal("expected map")
	}
	if raw["result"] != ResultUnresolved {
		t.Fatalf("result=%v", raw["result"])
	}
	two, _ := raw["two_way"].(ReconLeg)
	three, _ := raw["three_way"].(ReconLeg)
	if two.Result != ResultMatched || three.Result != ResultUnresolved {
		t.Fatalf("legs two=%+v three=%+v", two, three)
	}
}
