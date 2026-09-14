package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"zord-outcome-engine/internal/poll"
	"zord-outcome-engine/internal/poll/providers/razorpay"

	"github.com/gin-gonic/gin"
)

func TestBackfillRoutesRequireRelayToken(t *testing.T) {
	t.Setenv("RELAY_AUTH_TOKEN", "secret-token")
	gin.SetMode(gin.TestMode)
	from := time.Date(2026, 8, 26, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	store := poll.NewMemoryStore()
	svc := poll.NewBackfillService(store, poll.NewFreshnessService(store, poll.MemoryWebhookIndex{}), staticCreds{}, func(razorpay.Config) (poll.BackfillProvider, error) {
		return &emptyProvider{}, nil
	})
	r := gin.New()
	h := &BackfillHandler{Service: svc, Freshness: poll.NewFreshnessService(store, poll.MemoryWebhookIndex{})}
	r.GET("/internal/backfill/jobs/:job_id", h.GetJob)
	r.POST("/internal/backfill/payments", h.CreatePayments)

	req := httptest.NewRequest(http.MethodGet, "/internal/backfill/jobs/missing", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status=%d", w.Code)
	}

	job, err := svc.CreateJob(context.Background(), poll.CreateBackfillRequest{
		TenantID: "11111111-1111-1111-1111-111111111111", ConnectorID: "22222222-2222-2222-2222-222222222222",
		Mode: "test", ResourceType: poll.ResourcePayments, WindowFrom: from, WindowTo: to,
	})
	if err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodGet, "/internal/backfill/jobs/"+job.ID, nil)
	req.Header.Set("X-Relay-Token", "secret-token")
	req.Header.Set("X-Relay-Tenant-ID", "11111111-1111-1111-1111-111111111111")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get job status=%d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	cursor, _ := body["cursor"].(map[string]any)
	if cursor == nil {
		t.Fatalf("expected cursor in job json: %s", w.Body.String())
	}
}

func TestCreatePaymentsAccepted(t *testing.T) {
	t.Setenv("RELAY_AUTH_TOKEN", "secret-token")
	gin.SetMode(gin.TestMode)
	store := poll.NewMemoryStore()
	svc := poll.NewBackfillService(store, nil, staticCreds{}, func(razorpay.Config) (poll.BackfillProvider, error) {
		return &emptyProvider{}, nil
	})
	r := gin.New()
	r.POST("/internal/backfill/payments", (&BackfillHandler{Service: svc}).CreatePayments)
	payload, _ := json.Marshal(map[string]string{
		"tenant_id":     "11111111-1111-1111-1111-111111111111",
		"connector_id":  "22222222-2222-2222-2222-222222222222",
		"window_from":   "2026-08-26T00:00:00Z",
		"window_to":     "2026-08-26T01:00:00Z",
		"mode":          "test",
		"trigger_type":  "manual",
	})
	req := httptest.NewRequest(http.MethodPost, "/internal/backfill/payments", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Relay-Token", "secret-token")
	req.Header.Set("X-Relay-Tenant-ID", "11111111-1111-1111-1111-111111111111")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

type staticCreds struct{}

func (staticCreds) Resolve(context.Context, string, string, string) (razorpay.Config, error) {
	cfg := razorpay.DefaultConfig()
	cfg.KeyID = "rzp_test_x"
	cfg.KeySecret = "secret"
	return cfg, nil
}

type emptyProvider struct{}

func (emptyProvider) ListPaymentsPage(_ context.Context, _, _ time.Time, _, _ int) (razorpay.NeutralPage[razorpay.NeutralPayment], error) {
	return razorpay.NeutralPage[razorpay.NeutralPayment]{Meta: razorpay.ResponseMeta{Status: 200}}, nil
}

func (emptyProvider) ListSettlementDay(_ context.Context, _ razorpay.CivilDate, _, _ int) (razorpay.NeutralPage[razorpay.NeutralSettlementLine], error) {
	return razorpay.NeutralPage[razorpay.NeutralSettlementLine]{Meta: razorpay.ResponseMeta{Status: 200}}, nil
}
