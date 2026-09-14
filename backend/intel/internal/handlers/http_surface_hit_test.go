package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPSurfaceHitIntelRoutes(t *testing.T) {
	h := NewRouter(
		&HealthHandler{},
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
		nil, nil, nil, nil, nil, nil, nil, nil, nil,
	)
	calls := []struct{ method, path string }{
		{"GET", "/healthz"},
		{"GET", "/v1/intelligence/kpis"},
		{"GET", "/v1/intelligence/corridors/health"},
		{"GET", "/v1/intelligence/failures/top"},
		{"GET", "/v1/intelligence/sla"},
		{"GET", "/v1/intelligence/mode"},
		{"GET", "/v1/intelligence/mode/status"},
		{"GET", "/v1/intelligence/leakage"},
		{"GET", "/v1/intelligence/ambiguity"},
		{"GET", "/v1/intelligence/defensibility"},
		{"GET", "/v1/intelligence/rca/clusters"},
		{"GET", "/v1/intelligence/pattern"},
		{"GET", "/v1/intelligence/recommendation"},
		{"GET", "/v1/intelligence/batches"},
		{"GET", "/v1/intelligence/dashboard/leakage"},
		{"GET", "/v1/intelligence/policies"},
		{"GET", "/v1/intelligence/actions"},
		{"POST", "/v1/intelligence/explain-batch"},
	}
	for _, c := range calls {
		req := httptest.NewRequest(c.method, c.path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		t.Logf("HIT %s %s -> %d", c.method, c.path, w.Code)
		if c.path == "/healthz" && w.Code != 200 {
			t.Fatalf("healthz want 200 got %d body=%s", w.Code, w.Body.String())
		}
		if c.path != "/healthz" && w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s want 401 got %d body=%s", c.method, c.path, w.Code, w.Body.String())
		}
	}
}
