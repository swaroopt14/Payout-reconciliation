package recon

import (
	"testing"
	"time"
)

func capturedExactInput() FinancialInput {
	return FinancialInput{
		Payment: PaymentFact{
			ID: "cp1", PaymentID: "pay_001", CanonicalStatus: PaymentCaptured, Captured: true,
			AmountMinor: 10000, Currency: "INR",
		},
		Lines: []SettlementLine{{
			ID: "sl1", PaymentID: "pay_001", LineType: "payment", AmountMinor: 10000, CreditMinor: 9728, FeeMinor: 272, Currency: "INR",
		}},
		Decisions: []SettlementBankDecision{{
			ID: "d1", SettlementLineID: "sl1", BankObservationID: "b1", State: BankMatchExact, Confidence: 0.99,
			Evidence: map[string]any{"bank_credit_minor": int64(9728)},
		}},
		Banks: []BankTxn{{ID: "b1", UTR: "UTR123", CreditMinor: 9728, CreditDebit: "CREDIT", Currency: "INR"}},
	}
}

func TestP2_MerchantAmountMismatchIsVariance(t *testing.T) {
	in := capturedExactInput()
	in.Merchant = &MerchantBookFact{ID: "inv1", InvoiceID: "INV-1", PaymentID: "pay_001", AmountMinor: 11000, Currency: "INR"}
	got := ReconcilePayment(in)
	if got.Result != ResultVariance || got.Reason != "merchant_amount_mismatch" {
		t.Fatalf("got %s/%s", got.Result, got.Reason)
	}
	if got.ExpectedAmount != 11000 || got.ObservedAmount != 10000 || got.VarianceAmount != 1000 {
		t.Fatalf("amounts expected=%d observed=%d var=%d", got.ExpectedAmount, got.ObservedAmount, got.VarianceAmount)
	}
	if got.TwoWay.Result != ResultVariance {
		t.Fatalf("2-way must not be MATCHED when books disagree: %+v", got.TwoWay)
	}
}

func TestP2_MerchantAgreeHonestTwoWay(t *testing.T) {
	in := capturedExactInput()
	in.Merchant = &MerchantBookFact{ID: "inv1", InvoiceID: "INV-1", PaymentID: "pay_001", AmountMinor: 10000, Currency: "INR"}
	got := ReconcilePayment(in)
	if got.Result != ResultMatched {
		t.Fatalf("result=%s reason=%s", got.Result, got.Reason)
	}
	if !got.MerchantAgreed || got.TwoWay.Reason != "merchant_books_psp" {
		t.Fatalf("honest 2-way=%+v agreed=%v", got.TwoWay, got.MerchantAgreed)
	}
}

func TestP2_AbsentMerchantKeepsLegacyTwoWayLabel(t *testing.T) {
	got := ReconcilePayment(capturedExactInput())
	if got.TwoWay.Reason != "merchant_psp_settled" {
		t.Fatalf("legacy two-way=%s", got.TwoWay.Reason)
	}
}

func TestP2_OpenChargebackVariance(t *testing.T) {
	in := capturedExactInput()
	in.Disputes = []DisputeFact{{ID: "disp1", PaymentID: "pay_001", AmountMinor: 10000, Status: "open"}}
	got := ReconcilePayment(in)
	if got.Result != ResultVariance || got.Reason != "open_chargeback" {
		t.Fatalf("got %s/%s", got.Result, got.Reason)
	}
}

func TestP2_ReconcileFromSourcesProjectsAndMatches(t *testing.T) {
	in := capturedExactInput()
	obs := ObservationsFromInput(in)
	got := ReconcileFromSources(obs, FinancialInput{Decisions: in.Decisions})
	if got.Result != ResultMatched {
		t.Fatalf("result=%s reason=%s", got.Result, got.Reason)
	}
}

func TestP2_RestatementKeepsLatestFact(t *testing.T) {
	obs := []SourceObservation{
		{Kind: SourceKindSettlement, ID: "old", EntityID: "pay_001", AmountMinor: 1, FactVersion: 1},
		{Kind: SourceKindSettlement, ID: "new", EntityID: "pay_001", AmountMinor: 2, CreditMinor: 2, FactVersion: 2},
	}
	got := PickLatestObservations(obs)
	if len(got) != 1 || got[0].ID != "new" || got[0].AmountMinor != 2 {
		t.Fatalf("%+v", got)
	}
}

func TestP2_SelectByUTRShared(t *testing.T) {
	sel := SelectByUTR([]MatchCandidate{
		{ID: "a", UTR: "utr1", AmountMinor: 10},
		{ID: "b", UTR: "UTR1", AmountMinor: 11},
	}, "UTR1")
	if len(sel.IDs) != 2 || sel.Unique != nil {
		t.Fatalf("%+v", sel)
	}
	one := SelectByUTR([]MatchCandidate{{ID: "a", UTR: "xyz", AmountMinor: 5}}, "xyz")
	if one.Unique == nil || one.Unique.ID != "a" {
		t.Fatalf("%+v", one)
	}
}

func TestP2_BankContinuityGapAndDuplicate(t *testing.T) {
	dup := AssertBankContinuity([]BankContinuityFact{
		{ID: "1", IdentityHash: "h", HasBalance: true, BalanceMinor: 100},
		{ID: "2", IdentityHash: "h", HasBalance: true, CreditMinor: 10, BalanceMinor: 110},
	})
	if dup.OK || len(dup.Duplicates) != 1 {
		t.Fatalf("dup %+v", dup)
	}
	gap := AssertBankContinuity([]BankContinuityFact{
		{ID: "1", IdentityHash: "a", HasBalance: true, BalanceMinor: 100},
		{ID: "2", IdentityHash: "b", HasBalance: true, CreditMinor: 10, BalanceMinor: 105},
	})
	if gap.OK || len(gap.Gaps) != 1 {
		t.Fatalf("gap %+v", gap)
	}
	ok := AssertBankContinuity([]BankContinuityFact{
		{ID: "1", IdentityHash: "a", HasBalance: true, BalanceMinor: 100},
		{ID: "2", IdentityHash: "b", HasBalance: true, CreditMinor: 10, BalanceMinor: 110},
	})
	if !ok.OK {
		t.Fatalf("%+v", ok)
	}
}

func TestP2_ObligationsFromMerchant(t *testing.T) {
	due := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	ev := ObligationsFromMerchant([]MerchantBookFact{
		{ID: "i1", InvoiceID: "INV", PaymentID: "pay_1", AmountMinor: 50, Currency: "INR", DueAt: due},
		{ID: "p1", PayoutID: "pout_1", AmountMinor: 20, Currency: "INR", DueAt: due},
	})
	if len(ev) != 2 || ev[0].Kind != ObligationInvoiceDue || ev[1].Kind != ObligationPayoutDue {
		t.Fatalf("%+v", ev)
	}
	if ev[0].Direction != DirectionInbound || ev[1].Direction != DirectionOutbound {
		t.Fatalf("dirs %+v", ev)
	}
}

func TestP2_FourthSourceDoesNotRequireNewInputField(t *testing.T) {
	specs := CollectLegSpecs()
	if len(specs[1].Kinds) < 4 {
		t.Fatalf("3-way kinds=%v", specs[1].Kinds)
	}
	obs := ObservationsFromInput(capturedExactInput())
	obs = append(obs, SourceObservation{Kind: "tax_line", ID: "tax1", AmountMinor: 18})
	in := ProjectFinancialInput(obs)
	if in.Payment.PaymentID != "pay_001" {
		t.Fatal("unknown kind must not drop payment")
	}
}
