package recon

import (
	"context"
	"encoding/json"
	"testing"
)

func reconJSONMap(t *testing.T, fr FinancialResult) map[string]any {
	t.Helper()
	b, err := json.Marshal(ReconJSON(fr))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestReconJSON_SellerIDAndReasonCode(t *testing.T) {
	fr := FinancialResult{EntityType: EntityPayment, EntityID: "pay_1", Result: ResultVariance, Reason: "settlement_amount_mismatch", SellerID: "acc_seller_1"}
	m := reconJSONMap(t, fr)
	if m["seller_id"] != "acc_seller_1" {
		t.Fatalf("seller_id=%v", m["seller_id"])
	}
	if m["reason_code"] != "settlement_amount_mismatch" {
		t.Fatalf("reason_code=%v", m["reason_code"])
	}
	if m["result"] != ResultVariance || m["reason"] != "settlement_amount_mismatch" {
		t.Fatalf("verdict must be unchanged: %v", m)
	}
}

func TestReconJSON_SellerIDEmptyWhenUnknown(t *testing.T) {
	store := NewMemoryFinancialStore()
	ctx := context.Background()
	store.Payments = []PaymentFact{
		{PaymentID: "pay_none", CanonicalStatus: PaymentCaptured, AmountMinor: 100, Currency: "INR"},
		{PaymentID: "pay_split", CanonicalStatus: PaymentCaptured, AmountMinor: 100, Currency: "INR"},
	}
	store.Results = []FinancialResult{
		{EntityType: EntityPayment, EntityID: "pay_none", Result: ResultMatched, Reason: "captured_settlement_exact_bank"},
		{EntityType: EntityPayment, EntityID: "pay_split", Result: ResultMatched, Reason: "captured_settlement_exact_bank"},
	}
	// Split payment: two stored sellers -> ambiguous -> empty (never pick one).
	_, _ = store.UpsertTransferEdge(ctx, "t", "c", MarketplaceTransferEdge{TransferID: "trf_1", PaymentID: "pay_split", SellerID: "acc_a", AmountMinor: 50})
	_, _ = store.UpsertTransferEdge(ctx, "t", "c", MarketplaceTransferEdge{TransferID: "trf_2", PaymentID: "pay_split", SellerID: "acc_b", AmountMinor: 50})
	// Another tenant's edge for pay_none must not leak in.
	_, _ = store.UpsertTransferEdge(ctx, "t2", "c", MarketplaceTransferEdge{TransferID: "trf_3", PaymentID: "pay_none", SellerID: "acc_other", AmountMinor: 100})
	svc := NewFinancialService(store)
	for _, pid := range []string{"pay_none", "pay_split"} {
		_, fr, found, err := svc.GetPayment(ctx, "t", "c", pid)
		if err != nil || !found {
			t.Fatalf("%s found=%v err=%v", pid, found, err)
		}
		m := reconJSONMap(t, fr)
		if _, ok := m["seller_id"]; ok {
			t.Fatalf("%s seller_id must be omitted when unknown: %v", pid, m)
		}
		if m["reason_code"] != "captured_settlement_exact_bank" {
			t.Fatalf("%s reason_code=%v", pid, m["reason_code"])
		}
	}
}

func TestFinanceListRow_SellerIDAndReasonCode(t *testing.T) {
	store := NewMemoryFinancialStore()
	ctx := context.Background()
	store.Payments = []PaymentFact{
		{PaymentID: "pay_edge", CanonicalStatus: PaymentCaptured, AmountMinor: 100, Currency: "INR"},
		{PaymentID: "pay_refund", CanonicalStatus: PaymentCaptured, AmountMinor: 100, Currency: "INR"},
		{PaymentID: "pay_unknown", CanonicalStatus: PaymentCaptured, AmountMinor: 100, Currency: "INR"},
	}
	store.Payouts = []PayoutFact{{PayoutID: "pout_1", ProviderStatus: "processed", AmountMinor: 100, Currency: "INR"}}
	store.Results = []FinancialResult{
		{EntityType: EntityPayment, EntityID: "pay_edge", Result: ResultVariance, Reason: "settlement_amount_mismatch", VarianceAmount: 10},
		{EntityType: EntityPayment, EntityID: "pay_refund", Result: ResultMatched, Reason: "captured_settlement_exact_bank"},
		{EntityType: EntityPayout, EntityID: "pout_1", Result: ResultMatched, Reason: "payout_bank_debit_exact"},
	}
	_, _ = store.UpsertTransferEdge(ctx, "t", "c", MarketplaceTransferEdge{TransferID: "trf_1", PaymentID: "pay_edge", SellerID: "acc_edge", AmountMinor: 100})
	_, _ = store.UpsertRefund(ctx, "t", "c", RefundFact{RefundID: "rfnd_1", PaymentID: "pay_refund", SellerID: "acc_refund", AmountMinor: 10, Currency: "INR"})
	resp, err := NewFinancialService(store).ListFinanceResults(ctx, "t", "c", "")
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]FinanceListRow{}
	for _, r := range resp.Results {
		rows[r.PaymentID] = r
	}
	check := func(id, seller, code, result string) {
		t.Helper()
		r := rows[id]
		if r.SellerID != seller || r.ReasonCode != code || r.Result != result {
			t.Fatalf("%s: seller=%q code=%q result=%q", id, r.SellerID, r.ReasonCode, r.Result)
		}
	}
	check("pay_edge", "acc_edge", "settlement_amount_mismatch", ResultVariance)
	check("pay_refund", "acc_refund", "captured_settlement_exact_bank", ResultMatched)
	check("pay_unknown", "", "", "")
	check("pout_1", "", "payout_bank_debit_exact", ResultMatched)

	b, _ := json.Marshal(rows["pay_edge"])
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if m["seller_id"] != "acc_edge" || m["reason_code"] != "settlement_amount_mismatch" {
		t.Fatalf("json=%s", b)
	}
	b, _ = json.Marshal(rows["pay_unknown"])
	m = map[string]any{}
	_ = json.Unmarshal(b, &m)
	if _, ok := m["seller_id"]; ok {
		t.Fatalf("unknown seller must be omitted: %s", b)
	}
}
