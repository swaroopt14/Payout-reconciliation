package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"zord-outcome-engine/internal/observe"
	"zord-outcome-engine/internal/poll"

	"github.com/gin-gonic/gin"
)

func TestObservationIngestCaptured(t *testing.T) {
	t.Setenv("RELAY_AUTH_TOKEN", "secret-token")
	gin.SetMode(gin.TestMode)
	store := poll.NewMemoryStore()
	h := &ObservationHandler{Processor: observe.NewProcessor(store)}
	r := gin.New()
	r.POST("/internal/observations/provider", h.Ingest)

	body := []byte(`{
		"event_name":"provider.observation.received",
		"tenant_id":"11111111-1111-1111-1111-111111111111",
		"connector_id":"22222222-2222-2222-2222-222222222222",
		"provider":"razorpay",
		"provider_mode":"test",
		"provider_event_id":"evt_http",
		"provider_event_type":"payment.captured",
		"provider_entity_type":"payment",
		"provider_entity_id":"pay_http_1",
		"receipt_id":"33333333-3333-3333-3333-333333333333",
		"raw_body_hash":"sha256:x",
		"amount":100,
		"currency":"INR",
		"status":"captured",
		"captured":true
	}`)
	req := httptest.NewRequest(http.MethodPost, "/internal/observations/provider", bytes.NewReader(body))
	req.Header.Set("X-Relay-Token", "secret-token")
	req.Header.Set("X-Relay-Tenant-ID", "11111111-1111-1111-1111-111111111111")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != string(observe.ResultInserted) {
		t.Fatalf("resp=%v", resp)
	}
	if len(store.Payments) != 1 {
		t.Fatalf("payments=%d", len(store.Payments))
	}
}

func TestObservationIngestUnauthorized(t *testing.T) {
	t.Setenv("RELAY_AUTH_TOKEN", "secret-token")
	gin.SetMode(gin.TestMode)
	h := &ObservationHandler{Processor: observe.NewProcessor(poll.NewMemoryStore())}
	r := gin.New()
	r.POST("/internal/observations/provider", h.Ingest)
	req := httptest.NewRequest(http.MethodPost, "/internal/observations/provider", bytes.NewReader([]byte(`{}`)))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("code=%d", w.Code)
	}
}
