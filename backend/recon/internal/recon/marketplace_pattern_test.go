package recon

import (
	"context"
	"testing"
)

func TestMarketplacePattern_IndependentSellerClusters(t *testing.T) {
	store := NewMemoryFinancialStore()
	tenant, connector := "tenant-a", "conn-1"
	ctx := context.Background()
	for _, rf := range []RefundFact{
		{RefundID: "rfnd_s1_a", PaymentID: "pay_1", AmountMinor: 1000, ProviderStatus: "processed", SellerID: "S1"},
		{RefundID: "rfnd_s1_b", PaymentID: "pay_2", AmountMinor: 2500, ProviderStatus: "processed", SellerID: "S1"},
		{RefundID: "rfnd_s2_a", PaymentID: "pay_3", AmountMinor: 500, ProviderStatus: "processed", SellerID: "S2"},
	} {
		if _, err := store.UpsertRefund(ctx, tenant, connector, rf); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewFinancialService(store)
	res, err := svc.MarketplaceSellerPatterns(ctx, tenant, connector)
	if err != nil {
		t.Fatal(err)
	}
	if res.TenantID != tenant || res.ConnectorID != connector {
		t.Fatalf("scope: %+v", res)
	}
	by := map[string]MarketplaceSellerPattern{}
	for _, s := range res.Sellers {
		by[s.SellerID] = s
	}
	if len(by) != 2 {
		t.Fatalf("want 2 sellers, got %+v", res.Sellers)
	}
	if by["S1"].RefundCount != 2 || by["S1"].RefundSumMinor != 3500 {
		t.Fatalf("S1 cluster: %+v", by["S1"])
	}
	if by["S2"].RefundCount != 1 || by["S2"].RefundSumMinor != 500 {
		t.Fatalf("S2 cluster: %+v", by["S2"])
	}
}

func TestMarketplacePattern_RateBreachAdvisoryOnly(t *testing.T) {
	patterns := []MarketplaceSellerPattern{
		{SellerID: "S1", RefundCount: 5, RefundSumMinor: 9000},
		{SellerID: "S2", RefundCount: 1, RefundSumMinor: 100},
	}
	advisories := EvaluateMarketplacePatternBreach(patterns, 3, 10000)
	if len(advisories) != 1 || advisories[0].SellerID != "S1" || advisories[0].Reason != "refund_count_threshold" {
		t.Fatalf("want S1 count advisory only: %+v", advisories)
	}
	// sum threshold only hits S1
	advisories = EvaluateMarketplacePatternBreach(patterns, 0, 8000)
	if len(advisories) != 1 || advisories[0].SellerID != "S1" || advisories[0].Reason != "refund_sum_threshold" {
		t.Fatalf("want S1 sum advisory: %+v", advisories)
	}
	// zero thresholds disable both dimensions — never auto-block signal
	if got := EvaluateMarketplacePatternBreach(patterns, 0, 0); len(got) != 0 {
		t.Fatalf("disabled thresholds must yield no advisory: %+v", got)
	}
	if got := EvaluateMarketplacePatternBreach(patterns, -1, -5); len(got) != 0 {
		t.Fatalf("negative thresholds disabled: %+v", got)
	}
}

func TestMarketplacePattern_EmptyWhitespaceSellerExcluded(t *testing.T) {
	refunds := []RefundFact{
		{RefundID: "rfnd_ok", AmountMinor: 100, ProviderStatus: "processed", SellerID: "S1"},
		{RefundID: "rfnd_empty", AmountMinor: 999, ProviderStatus: "processed", SellerID: ""},
		{RefundID: "rfnd_ws", AmountMinor: 999, ProviderStatus: "processed", SellerID: "   "},
		{RefundID: "rfnd_missing", AmountMinor: 999, ProviderStatus: "processed"},
	}
	got := AggregateMarketplaceSellerPatterns(refunds)
	if len(got) != 1 || got[0].SellerID != "S1" || got[0].RefundCount != 1 || got[0].RefundSumMinor != 100 {
		t.Fatalf("empty/whitespace must not join any cluster: %+v", got)
	}
	for _, rf := range refunds[1:] {
		if rf.JoinsSellerCluster() {
			t.Fatalf("must not join: %+v", rf)
		}
	}
}

func TestMarketplacePattern_TenantScoped(t *testing.T) {
	store := NewMemoryFinancialStore()
	ctx := context.Background()
	_, _ = store.UpsertRefund(ctx, "tenant-A", "conn-1", RefundFact{
		RefundID: "rfnd_a", AmountMinor: 1111, ProviderStatus: "processed", SellerID: "S1",
	})
	_, _ = store.UpsertRefund(ctx, "tenant-B", "conn-1", RefundFact{
		RefundID: "rfnd_b", AmountMinor: 2222, ProviderStatus: "processed", SellerID: "S1",
	})
	svc := NewFinancialService(store)
	a, err := svc.MarketplaceSellerPatterns(ctx, "tenant-A", "conn-1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.MarketplaceSellerPatterns(ctx, "tenant-B", "conn-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Sellers) != 1 || a.Sellers[0].RefundSumMinor != 1111 {
		t.Fatalf("tenant A leaked or wrong: %+v", a)
	}
	if len(b.Sellers) != 1 || b.Sellers[0].RefundSumMinor != 2222 {
		t.Fatalf("tenant B leaked or wrong: %+v", b)
	}
	// A data must never appear in B
	if a.Sellers[0].RefundSumMinor == b.Sellers[0].RefundSumMinor {
		t.Fatal("tenant isolation broken")
	}
}

func TestMarketplacePattern_FailedCancelledDoNotInflate(t *testing.T) {
	refunds := []RefundFact{
		{RefundID: "rfnd_ok", AmountMinor: 1000, ProviderStatus: "processed", SellerID: "S1"},
		{RefundID: "rfnd_fail", AmountMinor: 5000, ProviderStatus: "failed", SellerID: "S1"},
		{RefundID: "rfnd_cancel", AmountMinor: 7000, ProviderStatus: "cancelled", SellerID: "S1"},
		{RefundID: "rfnd_canceled", AmountMinor: 3000, ProviderStatus: "canceled", SellerID: "S1"},
	}
	got := AggregateMarketplaceSellerPatterns(refunds)
	if len(got) != 1 || got[0].RefundCount != 1 || got[0].RefundSumMinor != 1000 {
		t.Fatalf("failed/cancelled must not inflate pattern as cash out: %+v", got)
	}
	if CountsTowardSellerPattern(refunds[1]) || CountsTowardSellerPattern(refunds[2]) {
		t.Fatal("failed/cancelled must not count toward seller pattern")
	}
}

func TestMarketplacePattern_DuplicateRefundCountsOnce(t *testing.T) {
	store := NewMemoryFinancialStore()
	ctx := context.Background()
	tenant, connector := "t1", "c1"
	_, err := store.UpsertRefund(ctx, tenant, connector, RefundFact{
		RefundID: "rfnd_dup", PaymentID: "pay_d", AmountMinor: 1000, ProviderStatus: "created", SellerID: "S1",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.UpsertRefund(ctx, tenant, connector, RefundFact{
		RefundID: "rfnd_dup", PaymentID: "pay_d", AmountMinor: 1500, ProviderStatus: "processed", SellerID: "S1",
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := NewFinancialService(store).MarketplaceSellerPatterns(ctx, tenant, connector)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sellers) != 1 || res.Sellers[0].RefundCount != 1 || res.Sellers[0].RefundSumMinor != 1500 {
		t.Fatalf("duplicate refund_id must count once after upsert: %+v", res.Sellers)
	}
}

func TestMarketplacePattern_NullSellerStillReconciles(t *testing.T) {
	rf := RefundFact{RefundID: "rfnd_ns", PaymentID: "pay_ns", AmountMinor: 2000, ProviderStatus: "processed", SellerID: ""}
	if rf.JoinsSellerCluster() {
		t.Fatal("empty seller must not join cluster")
	}
	got := ReconcilePayment(FinancialInput{
		Payment: PaymentFact{PaymentID: "pay_ns", CanonicalStatus: PaymentFailed, AmountMinor: 2000},
		Refunds: []RefundFact{rf},
	})
	if got.Result != ResultMatched || got.Reason != "failed_refund_no_bank_movement" {
		t.Fatalf("empty seller_id must still reconcile: %+v", got)
	}
	// pattern side: excluded from cluster
	if pats := AggregateMarketplaceSellerPatterns([]RefundFact{rf}); len(pats) != 0 {
		t.Fatalf("must not appear in pattern: %+v", pats)
	}
}

func TestMarketplacePattern_AdvisoryDoesNotMutateCashOrVerdicts(t *testing.T) {
	patterns := []MarketplaceSellerPattern{{SellerID: "S1", RefundCount: 10, RefundSumMinor: 50000}}
	input := FinancialInput{
		Payment: PaymentFact{PaymentID: "pay_adv", CanonicalStatus: PaymentFailed, AmountMinor: 2000},
		Refunds: []RefundFact{{
			RefundID: "rfnd_adv", PaymentID: "pay_adv", AmountMinor: 2000,
			ProviderStatus: "processed", SellerID: "S1",
		}},
	}
	before := ReconcilePayment(input)
	_ = EvaluateMarketplacePatternBreach(patterns, 1, 1)
	_ = AggregateMarketplaceSellerPatterns(input.Refunds)
	after := ReconcilePayment(input)
	if before.Result != after.Result || before.Reason != after.Reason || before.BankCreditProven != after.BankCreditProven {
		t.Fatalf("pattern helpers must not mutate reconcile verdicts: before=%+v after=%+v", before, after)
	}
	if before.Exception != nil || after.Exception != nil {
		t.Fatalf("must not invent exceptions: before=%+v after=%+v", before.Exception, after.Exception)
	}
}
