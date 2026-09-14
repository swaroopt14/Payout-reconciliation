package routes

import (
	"net/http/httptest"
	"testing"

	"zord-evidence/handlers"

	"github.com/gin-gonic/gin"
)

func TestHTTPSurfaceHitEvidenceRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	Register(r, &handlers.EvidenceHandler{}, &handlers.OutboxHandler{}, "internal-key", "jwt-secret")
	RegisterProofRoutes(r, &handlers.ProofHandler{}, "jwt-secret")

	calls := []struct{ method, path string }{
		{"GET", "/healthz"},
		{"GET", "/v1/evidence/packs"},
		{"GET", "/v1/evidence/packs/p1/old"},
		{"GET", "/v1/evidence/packs/p1/views/merchant"},
		{"GET", "/v1/evidence/packs/p1/inclusion-proofs"},
		{"GET", "/v1/evidence/batch/b1"},
		{"POST", "/v1/evidence/replay"},
		{"GET", "/v1/evidence/packs/p1"},
		{"GET", "/v1/evidence/packs/p1/timeline"},
		{"GET", "/v1/dispute/export/preview"},
		{"POST", "/internal/evidence/packs"},
		{"GET", "/internal/outbox/lease"},
	}
	for _, c := range calls {
		req := httptest.NewRequest(c.method, c.path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		t.Logf("HIT %s %s -> %d", c.method, c.path, w.Code)
		if c.path == "/healthz" && w.Code != 200 {
			t.Fatalf("healthz want 200 got %d", w.Code)
		}
		if c.path != "/healthz" && w.Code >= 500 {
			t.Errorf("%s %s 5xx=%d body=%s", c.method, c.path, w.Code, w.Body.String())
		}
	}
}
