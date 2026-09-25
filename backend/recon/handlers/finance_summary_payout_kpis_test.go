package handlers

import (
	"testing"

	"zord-outcome-engine/internal/recon"
)

func TestFinanceSummary_IncludesPayoutKPIs(t *testing.T) {
	r, store := financeRouter(t)
	store.PutScopedPayout("t", "c", recon.PayoutFact{PayoutID: "pout_1", ProviderStatus: "processed", AmountMinor: 10000, Currency: "INR"})
	store.PutScopedPayout("t", "c", recon.PayoutFact{PayoutID: "pout_2", ProviderStatus: "reversed", AmountMinor: 500, Currency: "INR"})
	store.PutScopedPayout("t", "c", recon.PayoutFact{PayoutID: "pout_3", ProviderStatus: "pending", AmountMinor: 250, Currency: "INR"})
	store.PutScopedPayout("t", "c", recon.PayoutFact{PayoutID: "pout_usd", ProviderStatus: "processed", AmountMinor: 777, Currency: "USD"})
	store.PutScopedPayout("other", "c", recon.PayoutFact{PayoutID: "pout_x", ProviderStatus: "processed", AmountMinor: 999, Currency: "INR"})
	code, body := getJSON(t, r, "/v1/reconciliation/summary?tenant_id=t&connector_id=c")
	if code != 200 {
		t.Fatalf("code=%d body=%v", code, body)
	}
	k, ok := body["payout_kpis"].(map[string]any)
	if !ok {
		t.Fatalf("missing payout_kpis: %v", body)
	}
	want := map[string]any{
		"currency": "INR", "scored_count": 3.0,
		"processed_count": 1.0, "processed_amount_minor": 10000.0,
		"review_count": 1.0, "review_amount_minor": 250.0,
		"failed_count": 1.0, "failed_amount_minor": 500.0,
		"total_amount_minor": 10750.0,
	}
	for key, v := range want {
		if k[key] != v {
			t.Fatalf("%s=%v want %v (kpis=%v)", key, k[key], v, k)
		}
	}
	if len(k) != len(want) {
		t.Fatalf("unexpected payout_kpis keys: %v", k)
	}
}
