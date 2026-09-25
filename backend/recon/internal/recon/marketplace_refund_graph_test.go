package recon

import (
	"context"
	"testing"
)

func TestRefundWithoutReverse_SurfacesOpsException(t *testing.T) {
	refunds := []RefundFact{
		{RefundID: "rfnd_1", PaymentID: "pay_1", AmountMinor: 1500, ProviderStatus: "processed", SellerID: "acc_seller"},
		{RefundID: "rfnd_ok", PaymentID: "pay_2", AmountMinor: 500, ProviderStatus: "processed", SellerID: "acc_ok"},
	}
	edges := []MarketplaceTransferEdge{
		// forward only — no reverse for pay_1/acc_seller
		{TransferID: "trf_1", PaymentID: "pay_1", SellerID: "acc_seller", AmountMinor: 1500},
		// reverse present for pay_2
		{TransferID: "trf_2", ReverseTransferID: "rtrf_2", PaymentID: "pay_2", SellerID: "acc_ok", AmountMinor: 500},
	}
	got := DetectRefundsWithoutReverse(refunds, edges)
	if len(got) != 1 {
		t.Fatalf("want 1 signal, got %+v", got)
	}
	if got[0].RefundID != "rfnd_1" || got[0].Reason != ReasonRefundWithoutReverse {
		t.Fatalf("signal: %+v", got[0])
	}
	if got[0].SellerID != "acc_seller" || got[0].AmountMinor != 1500 {
		t.Fatalf("fields: %+v", got[0])
	}
}

func TestRefundWithoutReverse_EmptySellerExcluded(t *testing.T) {
	refunds := []RefundFact{
		{RefundID: "rfnd_ns", PaymentID: "pay_x", AmountMinor: 900, ProviderStatus: "processed", SellerID: ""},
		{RefundID: "rfnd_ws", PaymentID: "pay_x", AmountMinor: 900, ProviderStatus: "processed", SellerID: "  "},
	}
	got := DetectRefundsWithoutReverse(refunds, nil)
	if len(got) != 0 {
		t.Fatalf("no seller_id must not signal: %+v", got)
	}
}

func TestRefundWithoutReverse_FailedStatusExcluded(t *testing.T) {
	refunds := []RefundFact{
		{RefundID: "rfnd_fail", PaymentID: "pay_1", AmountMinor: 100, ProviderStatus: "failed", SellerID: "S1"},
	}
	got := DetectRefundsWithoutReverse(refunds, nil)
	if len(got) != 0 {
		t.Fatalf("failed must not count toward graph exception: %+v", got)
	}
}

func TestRefundWithoutReverse_MatchedByRefundIDOnEdge(t *testing.T) {
	refunds := []RefundFact{
		{RefundID: "rfnd_linked", PaymentID: "pay_other", AmountMinor: 200, ProviderStatus: "processed", SellerID: "S1"},
	}
	edges := []MarketplaceTransferEdge{
		{TransferID: "trf_x", ReverseTransferID: "rtrf_x", PaymentID: "pay_diff", RefundID: "rfnd_linked", SellerID: "S1"},
	}
	got := DetectRefundsWithoutReverse(refunds, edges)
	if len(got) != 0 {
		t.Fatalf("refund_id reverse link must clear signal: %+v", got)
	}
}

func TestVelocityFlag_WithoutAutoBlock(t *testing.T) {
	patterns := []MarketplaceSellerPattern{
		{SellerID: "S1", RefundCount: 5, RefundSumMinor: 20000},
		{SellerID: "S2", RefundCount: 1, RefundSumMinor: 100},
	}
	// Hold disabled: flag + audit only
	flags := EvaluateMarketplaceVelocity(patterns, MarketplaceVelocityConfig{
		CountThreshold: 3, SumThreshold: 0, HoldEnabled: false,
	})
	if len(flags) != 1 || flags[0].SellerID != "S1" {
		t.Fatalf("want S1 flag: %+v", flags)
	}
	if !flags[0].OpsFlag || flags[0].AuditNote == "" {
		t.Fatalf("ops flag + audit required: %+v", flags[0])
	}
	if flags[0].HoldRecommended {
		t.Fatal("hold must not be recommended when HoldEnabled=false")
	}
	if flags[0].AutoBlockPayout {
		t.Fatal("agents must never auto-block payouts")
	}

	// Hold enabled: recommend hold, still never auto-block
	flags = EvaluateMarketplaceVelocity(patterns, MarketplaceVelocityConfig{
		CountThreshold: 3, HoldEnabled: true,
	})
	if len(flags) != 1 || !flags[0].HoldRecommended {
		t.Fatalf("want hold recommended: %+v", flags)
	}
	if flags[0].AutoBlockPayout {
		t.Fatal("HoldEnabled must not set AutoBlockPayout")
	}
}

func TestVelocityFlag_DisabledThresholdsNoFlag(t *testing.T) {
	patterns := []MarketplaceSellerPattern{{SellerID: "S1", RefundCount: 99, RefundSumMinor: 99999}}
	if got := EvaluateMarketplaceVelocity(patterns, MarketplaceVelocityConfig{}); len(got) != 0 {
		t.Fatalf("zero thresholds: %+v", got)
	}
}

func TestRefundGraph_ConnectorScoped(t *testing.T) {
	store := NewMemoryFinancialStore()
	ctx := context.Background()
	tenant := "tenant-g"

	_, _ = store.UpsertRefund(ctx, tenant, "c1", RefundFact{
		RefundID: "rfnd_c1", PaymentID: "pay_c1", AmountMinor: 1000, ProviderStatus: "processed", SellerID: "S1",
	})
	_, _ = store.UpsertRefund(ctx, tenant, "c2", RefundFact{
		RefundID: "rfnd_c2", PaymentID: "pay_c2", AmountMinor: 2000, ProviderStatus: "processed", SellerID: "S1",
	})
	// Reverse only on c2
	_, _ = store.UpsertTransferEdge(ctx, tenant, "c2", MarketplaceTransferEdge{
		TransferID: "trf_c2", ReverseTransferID: "rtrf_c2", PaymentID: "pay_c2", SellerID: "S1", AmountMinor: 2000,
	})
	// Forward-only on c1
	_, _ = store.UpsertTransferEdge(ctx, tenant, "c1", MarketplaceTransferEdge{
		TransferID: "trf_c1", PaymentID: "pay_c1", SellerID: "S1", AmountMinor: 1000,
	})

	svc := NewFinancialService(store)
	c1, err := svc.MarketplaceRefundGraphExceptions(ctx, tenant, "c1")
	if err != nil {
		t.Fatal(err)
	}
	c2, err := svc.MarketplaceRefundGraphExceptions(ctx, tenant, "c2")
	if err != nil {
		t.Fatal(err)
	}
	if len(c1.Signals) != 1 || c1.Signals[0].RefundID != "rfnd_c1" {
		t.Fatalf("c1 should signal missing reverse: %+v", c1)
	}
	if len(c2.Signals) != 0 {
		t.Fatalf("c2 has reverse — must not leak/signal: %+v", c2)
	}
	if !c1.NotCash || !c1.Advisory {
		t.Fatalf("finance: graph exceptions are not cash: %+v", c1)
	}

	// Velocity also connector-scoped
	v1, err := svc.MarketplaceVelocityFlags(ctx, tenant, "c1", MarketplaceVelocityConfig{CountThreshold: 1})
	if err != nil {
		t.Fatal(err)
	}
	v2, err := svc.MarketplaceVelocityFlags(ctx, tenant, "c2", MarketplaceVelocityConfig{CountThreshold: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(v1.Flags) != 1 || v1.Flags[0].RefundSumMinor != 1000 {
		t.Fatalf("c1 velocity: %+v", v1)
	}
	if len(v2.Flags) != 1 || v2.Flags[0].RefundSumMinor != 2000 {
		t.Fatalf("c2 velocity must not mix connectors: %+v", v2)
	}
	for _, f := range append(v1.Flags, v2.Flags...) {
		if f.AutoBlockPayout {
			t.Fatal("velocity path must never auto-block")
		}
	}
}

func TestRefundGraph_TenantScoped(t *testing.T) {
	store := NewMemoryFinancialStore()
	ctx := context.Background()
	_, _ = store.UpsertRefund(ctx, "tenant-A", "conn-1", RefundFact{
		RefundID: "rfnd_a", PaymentID: "pay_a", AmountMinor: 111, ProviderStatus: "processed", SellerID: "S1",
	})
	_, _ = store.UpsertRefund(ctx, "tenant-B", "conn-1", RefundFact{
		RefundID: "rfnd_b", PaymentID: "pay_b", AmountMinor: 222, ProviderStatus: "processed", SellerID: "S1",
	})
	svc := NewFinancialService(store)
	a, err := svc.MarketplaceRefundGraphExceptions(ctx, "tenant-A", "conn-1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.MarketplaceRefundGraphExceptions(ctx, "tenant-B", "conn-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Signals) != 1 || a.Signals[0].AmountMinor != 111 {
		t.Fatalf("tenant-A: %+v", a)
	}
	if len(b.Signals) != 1 || b.Signals[0].AmountMinor != 222 {
		t.Fatalf("tenant-B: %+v", b)
	}
}
