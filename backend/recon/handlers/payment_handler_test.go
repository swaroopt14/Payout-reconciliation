package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"zord-outcome-engine/internal/paymenttruth"
	"zord-outcome-engine/internal/poll"
	"zord-outcome-engine/internal/poll/providers/razorpay"

	"github.com/gin-gonic/gin"
)

func TestGetInternalPayment(t *testing.T) {
	t.Setenv("RELAY_AUTH_TOKEN", "secret-token")
	gin.SetMode(gin.TestMode)
	store := poll.NewMemoryStore()
	p := paymenttruth.NewProcessor(store)
	item := razorpay.NeutralPayment{
		PaymentID: "pay_ABC", OrderID: "order_1", AmountMinor: 50000, Currency: "INR",
		Status: "captured", Captured: true, PayloadHash: "sha256:cap", CreatedAt: time.Now().UTC(),
	}
	if _, err := p.ProcessNeutral(t.Context(), "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222", "razorpay", "test", "webhook", "evt_1", "33333333-3333-3333-3333-333333333333", item, false); err != nil {
		t.Fatal(err)
	}
	h := &PaymentHandler{Store: store}
	r := gin.New()
	r.GET("/internal/payments/:payment_id", h.Get)
	req := httptest.NewRequest(http.MethodGet, "/internal/payments/pay_ABC?tenant_id=11111111-1111-1111-1111-111111111111&connector_id=22222222-2222-2222-2222-222222222222", nil)
	req.Header.Set("X-Relay-Token", "secret-token")
	req.Header.Set("X-Relay-Tenant-ID", "11111111-1111-1111-1111-111111111111")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp["canonical_status"] != "captured" || resp["provider_status"] != "captured" {
		t.Fatalf("resp=%v", resp)
	}
	if _, ok := resp["email"]; ok {
		t.Fatal("email must not be in GET body")
	}
}

func TestGetInternalPaymentTenantMismatch(t *testing.T) {
	t.Setenv("RELAY_AUTH_TOKEN", "secret-token")
	gin.SetMode(gin.TestMode)
	h := &PaymentHandler{Store: poll.NewMemoryStore()}
	r := gin.New()
	r.GET("/internal/payments/:payment_id", h.Get)
	req := httptest.NewRequest(http.MethodGet, "/internal/payments/pay_ABC?tenant_id=11111111-1111-1111-1111-111111111111&connector_id=c", nil)
	req.Header.Set("X-Relay-Token", "secret-token")
	req.Header.Set("X-Relay-Tenant-ID", "22222222-2222-2222-2222-222222222222")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
}

func TestGetInternalPaymentUnauthorized(t *testing.T) {
	t.Setenv("RELAY_AUTH_TOKEN", "secret-token")
	gin.SetMode(gin.TestMode)
	h := &PaymentHandler{Store: poll.NewMemoryStore()}
	r := gin.New()
	r.GET("/internal/payments/:payment_id", h.Get)
	req := httptest.NewRequest(http.MethodGet, "/internal/payments/pay_ABC?tenant_id=t&connector_id=c", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("code=%d", w.Code)
	}
}
