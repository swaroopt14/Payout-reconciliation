package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPSurfaceHitProtectedIntentStubs(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"healthy"}`))
	})
	dummy := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}
	mux.HandleFunc("/v1/dlq", Protect(dummy))
	mux.HandleFunc("/v1/intents", Protect(dummy))
	mux.HandleFunc("/api/prod/intents/batch-ids", Protect(dummy))

	for _, path := range []string{"/health", "/v1/dlq", "/v1/intents", "/api/prod/intents/batch-ids"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		t.Logf("HIT GET %s -> %d", path, w.Code)
		if path == "/health" && w.Code != 200 {
			t.Fatalf("health want 200 got %d", w.Code)
		}
		if path != "/health" && w.Code != http.StatusUnauthorized {
			t.Errorf("%s want 401 got %d body=%s", path, w.Code, w.Body.String())
		}
	}
}
