package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"zord-outcome-engine/internal/payouttruth"
	"zord-outcome-engine/internal/poll"
	"zord-outcome-engine/internal/poll/providers/razorpay"

	"github.com/gin-gonic/gin"
)

func TestGetInternalPayout(t *testing.T) {
	t.Setenv("RELAY_AUTH_TOKEN", "secret-token")
	gin.SetMode(gin.TestMode)
	store := poll.NewMemoryStore()
	p := payouttruth.NewProcessor(store)
	item := razorpay.NeutralPayout{
		PayoutID: "pout_ABC", AmountMinor: 25000, Currency: "INR",
		Status: "processed", UTR: "UTR9", Mode: "IMPS", PayloadHash: "sha256:pout",
		CreatedAt: time.Now().UTC(),
	}
	obs, err := payouttruth.MapNeutral(
		"11111111-1111-1111-1111-111111111111",
		"22222222-2222-2222-2222-222222222222",
		"razorpay", "webhook", "evt_1", item, time.Now().UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Process(t.Context(), obs); err != nil {
		t.Fatal(err)
	}
	h := &PayoutHandler{Store: store}
	r := gin.New()
	r.GET("/internal/payouts/:payout_id", h.Get)
	req := httptest.NewRequest(http.MethodGet, "/internal/payouts/pout_ABC?tenant_id=11111111-1111-1111-1111-111111111111&connector_id=22222222-2222-2222-2222-222222222222", nil)
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
	if resp["status"] != "processed" || resp["payout_id"] != "pout_ABC" {
		t.Fatalf("%v", resp)
	}
	if resp["status"] == "STUCK" || resp["status"] == "SETTLED" {
		t.Fatal("must keep Razorpay status")
	}
}
