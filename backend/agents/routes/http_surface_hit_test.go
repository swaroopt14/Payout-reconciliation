package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"zord-prompt-layer/handler"
	plmiddleware "zord-prompt-layer/middleware"
)

func TestHTTPSurfaceHitAgentRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	health := handler.NewHealthHandler("zord-prompt-layer")
	qh := handler.NewQueryHandler(nil)
	Register(r, health, qh, nil, nil, nil, plmiddleware.AuthConfig{
		SigningSecret: "test-only-jwt-signing-secret-not-for-production",
		Issuer:        "zord-edge",
	})

	calls := []struct{ method, path, body string }{
		{"GET", "/health", ""},
		{"POST", "/query", `{"question":"cash position"}`},
		{"POST", "/v1/ask-zord/finance/query", `{"question":"why unmatched"}`},
		{"POST", "/v1/investigations", `{}`},
		{"GET", "/v1/investigations/x", ""},
		{"POST", "/v1/finance/briefing", `{}`},
	}
	for _, c := range calls {
		var req *http.Request
		if c.body != "" {
			req = httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
			req.Header.Set("Content-Type", "application/json")
		} else {
			req = httptest.NewRequest(c.method, c.path, nil)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		t.Logf("HIT %s %s -> %d", c.method, c.path, w.Code)
		if w.Code >= 500 {
			t.Errorf("%s %s 5xx=%d body=%s", c.method, c.path, w.Code, w.Body.String())
		}
		if c.path == "/health" && w.Code != 200 {
			t.Errorf("health want 200 got %d", w.Code)
		}
	}
}
