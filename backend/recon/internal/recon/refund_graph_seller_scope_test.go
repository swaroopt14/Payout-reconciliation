package recon

import (
	"context"
	"testing"
)

// Seller A and seller B split one payment; B's transfer is reversed and (legacy
// bad stamp) carries A's refund_id. A's refund must still surface
// refund_without_reverse_transfer: a refund_id match only proves a reverse
// when the edge seller equals the refund seller.
func TestRefundGraph_SplitPaymentOtherSellerReversedStillFlags(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryFinancialStore()
	if _, err := s.UpsertRefund(ctx, rgTenant, rgConnA, RefundFact{
		RefundID: "rfnd_A", PaymentID: "pay_split", AmountMinor: 1200, Currency: "INR",
		ProviderStatus: "processed", SellerID: "acc_A",
	}); err != nil {
		t.Fatal(err)
	}
	for _, e := range []MarketplaceTransferEdge{
		{TransferID: "trf_A", PaymentID: "pay_split", SellerID: "acc_A", AmountMinor: 1200},
		{TransferID: "trf_B", PaymentID: "pay_split", SellerID: "acc_B", AmountMinor: 800,
			ReverseTransferID: "rvrsl_B", RefundID: "rfnd_A"},
	} {
		if _, err := s.UpsertTransferEdge(ctx, rgTenant, rgConnA, e); err != nil {
			t.Fatal(err)
		}
	}

	refunds, _ := s.ListRefunds(ctx, rgTenant, rgConnA, "")
	edges, _ := s.ListTransferEdges(ctx, rgTenant, rgConnA)
	sigs := DetectRefundsWithoutReverse(refunds, edges)
	if len(sigs) != 1 || sigs[0].RefundID != "rfnd_A" || sigs[0].SellerID != "acc_A" {
		t.Fatalf("want signal for seller A refund, got %+v", sigs)
	}
	if sigs[0].Reason != ReasonRefundWithoutReverse {
		t.Fatalf("reason=%q", sigs[0].Reason)
	}

	list, err := NewFinancialService(s).ListExceptions(ctx, rgTenant, rgConnA)
	if err != nil {
		t.Fatal(err)
	}
	rows := refundGraphRows(list)
	if len(rows) != 1 {
		t.Fatalf("want 1 refund_without_reverse_transfer exception, got %+v", list)
	}
	ex := rows[0]
	if ex.EntityID != "rfnd_A" || ex.Reason != ReasonRefundWithoutReverse {
		t.Fatalf("exception must be for seller A refund with reason %q: %+v", ReasonRefundWithoutReverse, ex)
	}
	if ex.ReconciliationResult != ResultUnresolved || ex.VarianceAmount != 0 || ex.ExpectedAmount != 1200 {
		t.Fatalf("must be UNRESOLVED, zero variance, int64 minor expected: %+v", ex)
	}
}

// Same-seller refund_id match on a reversed edge still counts as proof.
func TestRefundGraph_RefundIDMatchSameSellerIsProof(t *testing.T) {
	refunds := []RefundFact{{RefundID: "rfnd_B", PaymentID: "pay_x", AmountMinor: 500, ProviderStatus: "processed", SellerID: "acc_B"}}
	edges := []MarketplaceTransferEdge{{TransferID: "trf_B", PaymentID: "pay_other", SellerID: "acc_B", ReverseTransferID: "rvrsl_B", RefundID: "rfnd_B"}}
	if sigs := DetectRefundsWithoutReverse(refunds, edges); len(sigs) != 0 {
		t.Fatalf("same-seller refund_id match must suppress: %+v", sigs)
	}
}

func TestTransferEdgeUpsert_CustomerRefundIDWins(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryFinancialStore()
	if _, err := s.UpsertTransferEdge(ctx, "t1", "c1", MarketplaceTransferEdge{
		TransferID: "trf_1", PaymentID: "pay_1", SellerID: "acc_A", RefundID: "rfnd_stamp", AmountMinor: 900,
	}); err != nil {
		t.Fatal(err)
	}
	saved, err := s.UpsertTransferEdge(ctx, "t1", "c1", MarketplaceTransferEdge{
		TransferID: "trf_1", ReverseTransferID: "rvrsl_1", RefundID: "rfnd_customer", RefundIDFromReversal: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if saved.RefundIDFromReversal {
		t.Fatal("RefundIDFromReversal must not leak from store")
	}
	got, _ := s.ListTransferEdges(ctx, "t1", "c1")
	if len(got) != 1 || got[0].RefundID != "rfnd_customer" || got[0].ReverseTransferID != "rvrsl_1" {
		t.Fatalf("customer_refund_id must win: %+v", got)
	}
	if got[0].AmountMinor != 900 || got[0].SellerID != "acc_A" {
		t.Fatalf("forward fields preserved: %+v", got[0])
	}
}

func TestTransferEdgeUpsert_ReupsertKeepsExistingRefundID(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryFinancialStore()
	base := MarketplaceTransferEdge{TransferID: "trf_k", PaymentID: "pay_k", SellerID: "acc_A", RefundID: "rfnd_first", AmountMinor: 700}
	if _, err := s.UpsertTransferEdge(ctx, "t1", "c1", base); err != nil {
		t.Fatal(err)
	}
	for _, rid := range []string{"rfnd_other", "", "  "} {
		e := base
		e.RefundID = rid
		if _, err := s.UpsertTransferEdge(ctx, "t1", "c1", e); err != nil {
			t.Fatal(err)
		}
		got, _ := s.ListTransferEdges(ctx, "t1", "c1")
		if len(got) != 1 || got[0].RefundID != "rfnd_first" {
			t.Fatalf("re-upsert with refund_id=%q must keep existing: %+v", rid, got)
		}
	}
	// Empty existing refund_id still fills from a refund-observe stamp.
	if _, err := s.UpsertTransferEdge(ctx, "t1", "c1", MarketplaceTransferEdge{TransferID: "trf_empty", PaymentID: "pay_k", SellerID: "acc_A"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertTransferEdge(ctx, "t1", "c1", MarketplaceTransferEdge{TransferID: "trf_empty", RefundID: "rfnd_fill"}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.ListTransferEdges(ctx, "t1", "c1")
	for _, e := range got {
		if e.TransferID == "trf_empty" && e.RefundID != "rfnd_fill" {
			t.Fatalf("empty refund_id should fill: %+v", e)
		}
	}
}
