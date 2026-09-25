package recon

import (
	"context"
	"testing"
)

func kpisFor(t *testing.T, store *MemoryFinancialStore, tenant, connector string) PayoutKPIs {
	t.Helper()
	k, err := NewFinancialService(store).PayoutKPIs(context.Background(), tenant, connector)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestPayoutKPIs_ReversedCountsAsFailed(t *testing.T) {
	store := NewMemoryFinancialStore()
	store.Payouts = []PayoutFact{
		{PayoutID: "pout_p", ProviderStatus: "processed", AmountMinor: 10000, Currency: "INR"},
		{PayoutID: "pout_r", ProviderStatus: "reversed", AmountMinor: 2500, Currency: "INR"},
		{PayoutID: "pout_f", ProviderStatus: "failed", AmountMinor: 700, Currency: "INR"},
		{PayoutID: "pout_q", ProviderStatus: "queued", AmountMinor: 300, Currency: "INR"},
	}
	k := kpisFor(t, store, "t", "c")
	if k.ProcessedCount != 1 || k.ProcessedAmountMinor != 10000 {
		t.Fatalf("processed=%+v", k)
	}
	if k.FailedCount != 2 || k.FailedAmountMinor != 3200 {
		t.Fatalf("reversed must count as failed: %+v", k)
	}
	if k.ReviewCount != 1 || k.ReviewAmountMinor != 300 {
		t.Fatalf("review=%+v", k)
	}
	if k.ScoredCount != 4 || k.TotalAmountMinor != 13500 {
		t.Fatalf("totals=%+v", k)
	}
}

func TestPayoutKPIs_INROnly(t *testing.T) {
	store := NewMemoryFinancialStore()
	store.Payouts = []PayoutFact{
		{PayoutID: "pout_inr", ProviderStatus: "processed", AmountMinor: 5000, Currency: "INR"},
		{PayoutID: "pout_usd", ProviderStatus: "processed", AmountMinor: 9999, Currency: "USD"},
		{PayoutID: "pout_blank", ProviderStatus: "failed", AmountMinor: 111, Currency: ""},
	}
	k := kpisFor(t, store, "t", "c")
	if k.Currency != "INR" {
		t.Fatalf("currency=%q", k.Currency)
	}
	if k.ScoredCount != 1 || k.TotalAmountMinor != 5000 || k.ProcessedAmountMinor != 5000 || k.FailedCount != 0 {
		t.Fatalf("non-INR must be excluded: %+v", k)
	}
}

func TestPayoutKPIs_ExcludesUnlinkedFileIntent(t *testing.T) {
	store := NewMemoryFinancialStore()
	store.Payouts = []PayoutFact{
		{PayoutID: "pout_file_unlinked", ProviderStatus: "pending", AmountMinor: 4000, Currency: "INR", Origin: PayoutOriginFileIntent},
		{PayoutID: "pout_file_linked", ProviderStatus: "processed", AmountMinor: 6000, Currency: "INR", Origin: PayoutOriginFileIntent},
	}
	store.PayoutEvents["pout_file_unlinked"] = []ObservationFact{{Source: "file_intent", ProviderStatus: "pending"}}
	store.PayoutEvents["pout_file_linked"] = []ObservationFact{{Source: "webhook", ProviderStatus: "processed", SourceEventID: "evt_1"}}
	k := kpisFor(t, store, "t", "c")
	if k.ScoredCount != 1 || k.TotalAmountMinor != 6000 || k.ReviewCount != 0 || k.ProcessedCount != 1 {
		t.Fatalf("unlinked file intent must not count: %+v", k)
	}
}

func TestPayoutKPIs_ProviderPayoutWithoutIntentCounts(t *testing.T) {
	store := NewMemoryFinancialStore()
	// Provider-observed payout with no linked intent and no observation rows loaded.
	store.Payouts = []PayoutFact{
		{PayoutID: "pout_no_intent", ProviderStatus: "processing", AmountMinor: 1234, Currency: "INR"},
		{PayoutID: "pout_provider", ProviderStatus: "processed", AmountMinor: 100, Currency: "INR", Origin: PayoutOriginProvider},
	}
	k := kpisFor(t, store, "t", "c")
	if k.ScoredCount != 2 || k.ReviewCount != 1 || k.ReviewAmountMinor != 1234 || k.ProcessedAmountMinor != 100 {
		t.Fatalf("provider payout without intent must count: %+v", k)
	}
}

func TestPayoutKPIs_TenantConnectorScoped(t *testing.T) {
	store := NewMemoryFinancialStore()
	store.PutScopedPayout("t1", "c1", PayoutFact{PayoutID: "pout_a", ProviderStatus: "processed", AmountMinor: 1000, Currency: "INR"})
	store.PutScopedPayout("t1", "c2", PayoutFact{PayoutID: "pout_b", ProviderStatus: "processed", AmountMinor: 2000, Currency: "INR"})
	store.PutScopedPayout("t2", "c1", PayoutFact{PayoutID: "pout_c", ProviderStatus: "failed", AmountMinor: 4000, Currency: "INR"})
	k := kpisFor(t, store, "t1", "c1")
	if k.ScoredCount != 1 || k.TotalAmountMinor != 1000 || k.FailedCount != 0 {
		t.Fatalf("t1/c1 leaked: %+v", k)
	}
	k = kpisFor(t, store, "t2", "c1")
	if k.ScoredCount != 1 || k.FailedAmountMinor != 4000 || k.ProcessedCount != 0 {
		t.Fatalf("t2/c1 leaked: %+v", k)
	}
	k = kpisFor(t, store, "t3", "c1")
	if k.ScoredCount != 0 || k.TotalAmountMinor != 0 {
		t.Fatalf("unknown scope must be empty: %+v", k)
	}
}
