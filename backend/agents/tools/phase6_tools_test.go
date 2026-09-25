package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMarketplaceRefundGraphExceptionsOptional(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/reconciliation/marketplace/refund-graph-exceptions" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if r.URL.Query().Get("tenant_id") != "t1" || r.URL.Query().Get("connector_id") != "c1" {
			t.Fatalf("query=%s", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tenant_id": "t1", "connector_id": "c1", "not_cash": true, "advisory": true,
			"signals": []any{
				map[string]any{
					"refund_id": "rf_1", "payment_id": "pay_1", "seller_id": "sel_1",
					"amount_minor": float64(500), "reason": "refund_without_reverse_transfer",
				},
			},
		})
	}))
	defer srv.Close()
	c := NewOutcomeClient(srv.URL, "")
	body, err := c.GetMarketplaceRefundGraphExceptions("t1", "c1")
	if err != nil {
		t.Fatal(err)
	}
	sigs, _ := body["signals"].([]any)
	if len(sigs) != 1 {
		t.Fatalf("%v", body)
	}
	via, err := CallTool(c, GetMarketplaceRefundGraphExceptions, "t1", "c1", "")
	if err != nil || via["signals"] == nil {
		t.Fatalf("%v %v", via, err)
	}
}

func TestMarketplaceVelocityFlagsHoldEnabledQuery(t *testing.T) {
	var sawHold string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/reconciliation/marketplace/velocity-flags" {
			t.Fatalf("path=%s", r.URL.Path)
		}
		sawHold = r.URL.Query().Get("hold_enabled")
		hold := sawHold == "true"
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tenant_id": "t1", "connector_id": "c1", "not_cash": true, "advisory": true,
			"flags": []any{
				map[string]any{
					"seller_id": "sel_1", "refund_count": float64(5), "refund_sum_minor": float64(9000),
					"reason": "velocity_count", "ops_flag": true, "hold_recommended": hold,
					"auto_block_payout": false,
				},
			},
		})
	}))
	defer srv.Close()
	c := NewOutcomeClient(srv.URL, "")

	off, err := c.GetMarketplaceVelocityFlags("t1", "c1", false)
	if err != nil {
		t.Fatal(err)
	}
	if sawHold != "false" {
		t.Fatalf("hold_enabled=%s", sawHold)
	}
	flags, _ := off["flags"].([]any)
	f0, _ := flags[0].(map[string]any)
	if f0["hold_recommended"] != false {
		t.Fatalf("HoldRecommended must be false when HoldEnabled false: %v", f0)
	}
	if f0["auto_block_payout"] != false {
		t.Fatal("never auto-block")
	}

	on, err := c.GetMarketplaceVelocityFlags("t1", "c1", true)
	if err != nil {
		t.Fatal(err)
	}
	if sawHold != "true" {
		t.Fatalf("hold_enabled=%s", sawHold)
	}
	flags, _ = on["flags"].([]any)
	f0, _ = flags[0].(map[string]any)
	if f0["hold_recommended"] != true {
		t.Fatalf("%v", f0)
	}

	via, err := CallTool(c, GetMarketplaceVelocityFlags, "t1", "c1", "true")
	if err != nil || via["flags"] == nil {
		t.Fatalf("%v %v", via, err)
	}
}

func TestMarketplaceToolsSoftFail(t *testing.T) {
	c := NewOutcomeClient("http://127.0.0.1:9", "")
	body, err := c.GetMarketplaceRefundGraphExceptions("t", "c")
	if err != nil {
		t.Fatal(err)
	}
	if body["error"] != "not_found" {
		t.Fatalf("%v", body)
	}
	if strings.Contains(strings.ToLower(compactJSON(body)), "hold recommended") {
		t.Fatal(body)
	}
}
