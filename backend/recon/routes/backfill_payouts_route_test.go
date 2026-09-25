package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"zord-outcome-engine/handlers"
	"zord-outcome-engine/internal/poll"
	"zord-outcome-engine/internal/poll/providers/razorpay"

	"github.com/gin-gonic/gin"
)

type routeCreds struct{}

func (routeCreds) Resolve(context.Context, string, string, string) (razorpay.Config, error) {
	cfg := razorpay.DefaultConfig()
	cfg.KeyID = "rzp_test_placeholder"
	cfg.KeySecret = "test-only"
	return cfg, nil
}

type routeProvider struct{}

func (routeProvider) ListPaymentsPage(context.Context, time.Time, time.Time, int, int) (razorpay.NeutralPage[razorpay.NeutralPayment], error) {
	return razorpay.NeutralPage[razorpay.NeutralPayment]{}, nil
}

func (routeProvider) ListSettlementDay(context.Context, razorpay.CivilDate, int, int) (razorpay.NeutralPage[razorpay.NeutralSettlementLine], error) {
	return razorpay.NeutralPage[razorpay.NeutralSettlementLine]{}, nil
}

func (routeProvider) ListPayoutsPage(context.Context, time.Time, time.Time, int, int) (razorpay.NeutralPage[razorpay.NeutralPayout], error) {
	return razorpay.NeutralPage[razorpay.NeutralPayout]{}, nil
}

// The scheduler's payout timer DAG posts /internal/backfill/payouts with
// X-Relay-Token + X-Relay-Tenant-ID. The real BackfillRoutes registers it and
// it creates a "payouts" job (same payout-truth intake as webhooks, D26).
func TestBackfillPayoutsRouteTenantScoped(t *testing.T) {
	t.Setenv("RELAY_AUTH_TOKEN", "test-only-relay-token")
	gin.SetMode(gin.TestMode)
	store := poll.NewMemoryStore()
	svc := poll.NewBackfillService(store, nil, routeCreds{}, func(razorpay.Config) (poll.BackfillProvider, error) {
		return routeProvider{}, nil
	})
	r := gin.New()
	BackfillRoutes(r, &handlers.BackfillHandler{Service: svc})

	now := time.Now().UTC()
	body, _ := json.Marshal(map[string]any{
		"tenant_id":    "11111111-1111-1111-1111-111111111111",
		"connector_id": "22222222-2222-2222-2222-222222222222",
		"window_from":  now.Add(-2 * time.Hour).Format(time.RFC3339),
		"window_to":    now.Add(-time.Minute).Format(time.RFC3339),
		"trigger_type": "airflow",
		"mode":         "test",
	})
	post := func(tenant string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/internal/backfill/payouts", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Relay-Token", "test-only-relay-token")
		if tenant != "" {
			req.Header.Set("X-Relay-Tenant-ID", tenant)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	if w := post(""); w.Code != http.StatusForbidden {
		t.Fatalf("missing X-Relay-Tenant-ID status=%d", w.Code)
	}
	if w := post("33333333-3333-3333-3333-333333333333"); w.Code != http.StatusForbidden {
		t.Fatalf("tenant mismatch status=%d", w.Code)
	}
	w := post("11111111-1111-1111-1111-111111111111")
	if w.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out["resource_type"] != poll.ResourcePayouts {
		t.Fatalf("resource_type=%v", out["resource_type"])
	}
}
