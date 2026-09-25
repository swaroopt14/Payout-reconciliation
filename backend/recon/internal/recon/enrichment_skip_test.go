package recon

import (
	"context"
	"testing"
)

func skipFixture(status string) RefundFact {
	return RefundFact{RefundID: "rfnd_s", PaymentID: "pay_s", AmountMinor: 500, Currency: "INR",
		ProviderStatus: "processed", SellerID: "acc_seller", EnrichmentStatus: status}
}

// D52 / D47 / D48: a skipped-enrichment refund has an unknown marketplace
// shape — it must not feed seller patterns or refund_without_reverse_transfer,
// while the identical non-skipped refund still does.
func TestSkippedEnrichment_ExcludedFromSellerPatternsAndRefundGraph(t *testing.T) {
	for _, st := range []string{EnrichmentSkippedNoCreds, EnrichmentSkippedUnavailable} {
		r := skipFixture(st)
		if CountsTowardSellerPattern(r) {
			t.Fatalf("%s must not count", st)
		}
		if got := AggregateMarketplaceSellerPatterns([]RefundFact{r}); len(got) != 0 {
			t.Fatalf("%s formed a seller pattern: %+v", st, got)
		}
		if got := DetectRefundsWithoutReverse([]RefundFact{r}, nil); len(got) != 0 {
			t.Fatalf("%s flagged refund_without_reverse_transfer: %+v", st, got)
		}
	}
	for _, st := range []string{"", EnrichmentEnriched} {
		r := skipFixture(st)
		if got := AggregateMarketplaceSellerPatterns([]RefundFact{r}); len(got) != 1 {
			t.Fatalf("control %q must form a pattern: %+v", st, got)
		}
		if got := DetectRefundsWithoutReverse([]RefundFact{r}, nil); len(got) != 1 {
			t.Fatalf("control %q must be flagged: %+v", st, got)
		}
	}
}

func TestSkippedEnrichment_RefundGraphExceptionsAndDataGapCount(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryFinancialStore()
	skipped := skipFixture(EnrichmentSkippedNoCreds)
	if _, err := store.UpsertRefund(ctx, "t1", "c1", skipped); err != nil {
		t.Fatal(err)
	}
	svc := NewFinancialService(store)
	ex, err := svc.RefundGraphExceptions(ctx, "t1", "c1")
	if err != nil || len(ex) != 0 {
		t.Fatalf("skipped refund must not raise refund_without_reverse_transfer: %+v err=%v", ex, err)
	}
	gap, err := store.CountSkippedEnrichment(ctx, "t1", "c1")
	if err != nil || gap.Total != 1 || gap.ByReason[EnrichmentSkippedNoCreds] != 1 {
		t.Fatalf("data-gap count: %+v err=%v", gap, err)
	}
	if other, _ := store.CountSkippedEnrichment(ctx, "t2", ""); other.Total != 0 {
		t.Fatalf("tenant isolation: %+v", other)
	}

	control := skipFixture(EnrichmentEnriched)
	control.RefundID, control.PaymentID = "rfnd_c", "pay_c"
	if _, err := store.UpsertRefund(ctx, "t1", "c1", control); err != nil {
		t.Fatal(err)
	}
	ex, err = svc.RefundGraphExceptions(ctx, "t1", "c1")
	if err != nil || len(ex) != 1 || ex[0].EntityID == "" {
		t.Fatalf("enriched refund without reverse must be flagged once: %+v err=%v", ex, err)
	}
}
