package recon

import (
	"context"
	"testing"
)

func TestRefundSellerID_EmptyStillReconcilesAndDoesNotJoinCluster(t *testing.T) {
	cases := []RefundFact{
		{RefundID: "rfnd_empty", PaymentID: "pay_s0", AmountMinor: 2000, ProviderStatus: "processed"},
		{RefundID: "rfnd_blank", PaymentID: "pay_s0", AmountMinor: 2000, ProviderStatus: "processed", SellerID: ""},
		{RefundID: "rfnd_ws", PaymentID: "pay_s0", AmountMinor: 2000, ProviderStatus: "processed", SellerID: "   "},
	}
	for _, rf := range cases {
		if rf.JoinsSellerCluster() {
			t.Fatalf("empty/whitespace seller_id must not join cluster: %+v", rf)
		}
		got := ReconcilePayment(FinancialInput{
			Payment: PaymentFact{PaymentID: "pay_s0", CanonicalStatus: PaymentFailed, AmountMinor: 2000},
			Refunds: []RefundFact{rf},
		})
		if got.Result != ResultMatched || got.Reason != "failed_refund_no_bank_movement" {
			t.Fatalf("seller_id=%q result=%s reason=%s", rf.SellerID, got.Result, got.Reason)
		}
	}
}

func TestRefundSellerID_PresentJoinsClusterPredicateOnly(t *testing.T) {
	rf := RefundFact{
		RefundID: "rfnd_s1", PaymentID: "pay_s1", AmountMinor: 1500,
		ProviderStatus: "processed", SellerID: "acct_seller_1",
	}
	if !rf.JoinsSellerCluster() {
		t.Fatal("non-empty seller_id must join cluster predicate")
	}
	// Fact shape only: reconcile path unchanged; presence of seller_id must not alter verdict.
	got := ReconcilePayment(FinancialInput{
		Payment: PaymentFact{PaymentID: "pay_s1", CanonicalStatus: PaymentFailed, AmountMinor: 1500},
		Refunds: []RefundFact{rf},
	})
	if got.Result != ResultMatched || got.Reason != "failed_refund_no_bank_movement" {
		t.Fatalf("result=%s reason=%s", got.Result, got.Reason)
	}
}

func TestRefundSellerID_RoundTripMemoryStore(t *testing.T) {
	store := NewMemoryFinancialStore()
	tenant, connector := "t1", "c1"
	saved, err := store.UpsertRefund(context.Background(), tenant, connector, RefundFact{
		RefundID: "rfnd_rt", PaymentID: "pay_rt", AmountMinor: 999,
		Currency: "INR", ProviderStatus: "processed", Source: "webhook",
		SellerID: "  seller_abc  ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if saved.SellerID != "seller_abc" {
		t.Fatalf("trim on write: %q", saved.SellerID)
	}
	got, err := store.ListRefunds(context.Background(), tenant, connector, "pay_rt")
	if err != nil || len(got) != 1 {
		t.Fatalf("list=%+v err=%v", got, err)
	}
	if got[0].SellerID != "seller_abc" || !got[0].JoinsSellerCluster() {
		t.Fatalf("round-trip seller_id=%q", got[0].SellerID)
	}
}

func TestRefundSellerID_DuplicateRefundLineUpsert(t *testing.T) {
	store := NewMemoryFinancialStore()
	tenant, connector := "t1", "c1"
	_, err := store.UpsertRefund(context.Background(), tenant, connector, RefundFact{
		RefundID: "rfnd_dup", PaymentID: "pay_dup", AmountMinor: 1000, ProviderStatus: "created",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.UpsertRefund(context.Background(), tenant, connector, RefundFact{
		RefundID: "rfnd_dup", PaymentID: "pay_dup", AmountMinor: 1000, ProviderStatus: "processed",
		SellerID: "seller_dup",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.ListRefunds(context.Background(), tenant, connector, "pay_dup")
	if err != nil || len(got) != 1 {
		t.Fatalf("duplicate must upsert one row: %+v err=%v", got, err)
	}
	if got[0].ProviderStatus != "processed" || got[0].SellerID != "seller_dup" {
		t.Fatalf("%+v", got[0])
	}
}

func TestRefundSellerID_FailedRefundStatusStillMatches(t *testing.T) {
	// Failed/cancelled refund observation must not invent bank cash; failed payment still matches.
	got := ReconcilePayment(FinancialInput{
		Payment: PaymentFact{PaymentID: "pay_ff", CanonicalStatus: PaymentFailed, AmountMinor: 2000},
		Refunds: []RefundFact{{
			RefundID: "rfnd_fail", PaymentID: "pay_ff", AmountMinor: 2000,
			ProviderStatus: "failed", SellerID: "",
		}},
	})
	if got.Result != ResultMatched || got.BankCreditProven {
		t.Fatalf("%+v", got)
	}
	if got.Reason != "failed_refund_no_bank_movement" {
		t.Fatalf("reason=%s", got.Reason)
	}
}

func TestRefundSellerID_ObservationPlusSettlementLineNotDoubleCounted(t *testing.T) {
	// Finance rule: refund observation + settlement refund line for the same payment is one outflow signal.
	got := ReconcilePayment(FinancialInput{
		Payment: PaymentFact{PaymentID: "pay_dc", CanonicalStatus: PaymentFailed, AmountMinor: 2000},
		Lines: []SettlementLine{{
			ID: "sl_ref", PaymentID: "pay_dc", LineType: "refund", AmountMinor: 2000, DebitMinor: 2000,
		}},
		Refunds: []RefundFact{{
			RefundID: "rfnd_dc", PaymentID: "pay_dc", AmountMinor: 2000,
			ProviderStatus: "processed", SellerID: "seller_dc",
		}},
	})
	if got.Result != ResultMatched || got.Reason != "failed_refund_no_bank_movement" {
		t.Fatalf("result=%s reason=%s", got.Result, got.Reason)
	}
	if got.Exception != nil {
		t.Fatalf("must not exception on explained refund: %+v", got.Exception)
	}
	if got.BankCreditProven {
		t.Fatal("must not treat refund as bank credit")
	}
}
