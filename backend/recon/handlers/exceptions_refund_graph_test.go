package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"zord-outcome-engine/internal/recon"

	"github.com/gin-gonic/gin"
)

func refundGraphExceptionsRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	store := recon.NewMemoryFinancialStore()
	for _, r := range []recon.RefundFact{
		{RefundID: "rfnd_sig", PaymentID: "pay_1", AmountMinor: 1500, ProviderStatus: "processed", SellerID: "acc_seller"},
		{RefundID: "rfnd_cancelled", PaymentID: "pay_1", AmountMinor: 800, ProviderStatus: "cancelled", SellerID: "acc_seller"},
	} {
		if _, err := store.UpsertRefund(ctx, "t", "c", r); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.UpsertRefund(ctx, "t", "other", recon.RefundFact{
		RefundID: "rfnd_other", PaymentID: "pay_2", AmountMinor: 100, ProviderStatus: "processed", SellerID: "acc_x",
	}); err != nil {
		t.Fatal(err)
	}
	h := &FinancialHandler{Service: recon.NewFinancialService(store), Store: store}
	r := gin.New()
	r.GET("/v1/reconciliation/exceptions", h.ListExceptions)
	r.GET("/v1/reconciliation/exceptions/:id", h.GetException)
	r.GET("/v1/reconciliation/marketplace/refund-graph-exceptions", h.ListMarketplaceRefundGraphExceptions)
	return r
}

func TestListExceptions_SurfacesRefundWithoutReverse(t *testing.T) {
	r := refundGraphExceptionsRouter(t)
	code, body := getJSON(t, r, "/v1/reconciliation/exceptions?tenant_id=t&connector_id=c&reason=refund_without_reverse_transfer")
	if code != http.StatusOK {
		t.Fatalf("code=%d body=%v", code, body)
	}
	list, _ := body["exceptions"].([]any)
	if len(list) != 1 || body["total"].(float64) != 1 {
		t.Fatalf("want 1 refund exception (cancelled excluded, other connector excluded), got %v", body)
	}
	ex := list[0].(map[string]any)
	if ex["entity_type"] != "refund" || ex["entity_id"] != "rfnd_sig" ||
		ex["reconciliation_result"] != recon.ResultUnresolved || ex["variance_amount"].(float64) != 0 ||
		ex["connector_id"] != "c" {
		t.Fatalf("exception: %v", ex)
	}

	id := ex["id"].(string)
	code, one := getJSON(t, r, "/v1/reconciliation/exceptions/"+id+"?tenant_id=t&connector_id=c")
	if code != http.StatusOK {
		t.Fatalf("get by id code=%d body=%v", code, one)
	}
	if d := one["data"].(map[string]any); d["entity_id"] != "rfnd_sig" {
		t.Fatalf("get: %v", one)
	}
	code, _ = getJSON(t, r, "/v1/reconciliation/exceptions/"+id+"?tenant_id=t&connector_id=other")
	if code != http.StatusNotFound {
		t.Fatalf("other connector must 404, got %d", code)
	}

	code, other := getJSON(t, r, "/v1/reconciliation/exceptions?tenant_id=t&connector_id=other")
	if code != http.StatusOK || other["total"].(float64) != 1 {
		t.Fatalf("other connector listing: %v", other)
	}
	if e := other["exceptions"].([]any)[0].(map[string]any); e["entity_id"] != "rfnd_other" {
		t.Fatalf("connector leak: %v", e)
	}

	// Legacy endpoint still works from the same source.
	req := httptest.NewRequest(http.MethodGet, "/v1/reconciliation/marketplace/refund-graph-exceptions?tenant_id=t&connector_id=c", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var legacy recon.MarketplaceRefundGraphResult
	if err := json.Unmarshal(w.Body.Bytes(), &legacy); err != nil || w.Code != 200 {
		t.Fatalf("legacy code=%d err=%v", w.Code, err)
	}
	if len(legacy.Signals) != 1 || legacy.Signals[0].RefundID != "rfnd_sig" {
		t.Fatalf("legacy: %+v", legacy)
	}
}
