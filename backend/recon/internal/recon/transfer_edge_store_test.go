package recon

import (
	"context"
	"testing"
	"time"
)

func TestUpsertTransferEdgeRoundTrip(t *testing.T) {
	store := NewMemoryFinancialStore()
	ctx := context.Background()
	at := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	saved, err := store.UpsertTransferEdge(ctx, "tenant-a", "conn-1", MarketplaceTransferEdge{
		TransferID:  "trf_1",
		PaymentID:   "pay_1",
		RefundID:    "rfnd_1",
		SellerID:    "acc_seller",
		AmountMinor: 1500,
		Currency:    "INR",
		TransferAt:  at,
	})
	if err != nil {
		t.Fatal(err)
	}
	if saved.ID == "" || saved.TenantID != "tenant-a" || saved.ConnectorID != "conn-1" {
		t.Fatalf("saved=%+v", saved)
	}
	got, err := store.ListTransferEdges(ctx, "tenant-a", "conn-1")
	if err != nil || len(got) != 1 {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	e := got[0]
	if e.TransferID != "trf_1" || e.PaymentID != "pay_1" || e.RefundID != "rfnd_1" {
		t.Fatalf("%+v", e)
	}
	if e.SellerID != "acc_seller" || e.AmountMinor != 1500 || e.Currency != "INR" {
		t.Fatalf("%+v", e)
	}
	if !e.TransferAt.Equal(at) {
		t.Fatalf("transfer_at=%v", e.TransferAt)
	}
}

func TestUpsertTransferEdgeReverseUpdatesSameRow(t *testing.T) {
	store := NewMemoryFinancialStore()
	ctx := context.Background()
	first, err := store.UpsertTransferEdge(ctx, "t1", "c1", MarketplaceTransferEdge{
		TransferID:  "trf_rev",
		PaymentID:   "pay_rev",
		SellerID:    "acc_s",
		AmountMinor: 2000,
	})
	if err != nil {
		t.Fatal(err)
	}
	revAt := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	second, err := store.UpsertTransferEdge(ctx, "t1", "c1", MarketplaceTransferEdge{
		TransferID:        "trf_rev",
		ReverseTransferID: "rvrsl_1",
		ReverseAt:         revAt,
		// omit payment/seller/amount — must coalesce from existing row
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID {
		t.Fatalf("expected same row id %s got %s", first.ID, second.ID)
	}
	got, _ := store.ListTransferEdges(ctx, "t1", "c1")
	if len(got) != 1 {
		t.Fatalf("want 1 row, got %+v", got)
	}
	e := got[0]
	if e.ReverseTransferID != "rvrsl_1" || !e.ReverseAt.Equal(revAt) {
		t.Fatalf("reverse not set: %+v", e)
	}
	if e.PaymentID != "pay_rev" || e.SellerID != "acc_s" || e.AmountMinor != 2000 {
		t.Fatalf("forward fields must be preserved: %+v", e)
	}
	if !e.HasReverse() {
		t.Fatal("HasReverse")
	}
}

func TestUpsertTransferEdgeEmptySellerIDStored(t *testing.T) {
	store := NewMemoryFinancialStore()
	ctx := context.Background()
	_, err := store.UpsertTransferEdge(ctx, "t1", "c1", MarketplaceTransferEdge{
		TransferID:  "trf_empty",
		PaymentID:   "pay_empty",
		SellerID:    "", // never invent
		AmountMinor: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := store.ListTransferEdges(ctx, "t1", "c1")
	if len(got) != 1 || got[0].SellerID != "" {
		t.Fatalf("empty seller_id must round-trip without inventing: %+v", got)
	}
}

func TestUpsertTransferEdgeTenantConnectorIsolation(t *testing.T) {
	store := NewMemoryFinancialStore()
	ctx := context.Background()
	_, _ = store.UpsertTransferEdge(ctx, "tenant-A", "conn-1", MarketplaceTransferEdge{
		TransferID: "trf_shared", PaymentID: "pay_a", SellerID: "S1", AmountMinor: 111,
	})
	_, _ = store.UpsertTransferEdge(ctx, "tenant-B", "conn-1", MarketplaceTransferEdge{
		TransferID: "trf_shared", PaymentID: "pay_b", SellerID: "S1", AmountMinor: 222,
	})
	_, _ = store.UpsertTransferEdge(ctx, "tenant-A", "conn-2", MarketplaceTransferEdge{
		TransferID: "trf_shared", PaymentID: "pay_a2", SellerID: "S2", AmountMinor: 333,
	})

	a1, _ := store.ListTransferEdges(ctx, "tenant-A", "conn-1")
	b1, _ := store.ListTransferEdges(ctx, "tenant-B", "conn-1")
	a2, _ := store.ListTransferEdges(ctx, "tenant-A", "conn-2")
	if len(a1) != 1 || a1[0].PaymentID != "pay_a" || a1[0].AmountMinor != 111 {
		t.Fatalf("a1=%+v", a1)
	}
	if len(b1) != 1 || b1[0].PaymentID != "pay_b" || b1[0].AmountMinor != 222 {
		t.Fatalf("b1=%+v", b1)
	}
	if len(a2) != 1 || a2[0].PaymentID != "pay_a2" || a2[0].AmountMinor != 333 {
		t.Fatalf("a2=%+v", a2)
	}
}
