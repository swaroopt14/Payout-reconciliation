package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestHTTPSurfaceHitPublicEdgeRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterPublicAuthRoutes(r)
	r.GET("/v1/health", HealthCheck)
	r.GET("/health", HealthCheck)
	h := &Handler{}
	r.POST("/v1/webhooks/razorpay/:connectorID", h.HandleRazorpayWebhook)
	r.GET("/v1/webhooks/razorpay/receipt/:receiptID", h.HandleRazorpayWebhookStatus)
	r.GET("/v1/webhooks/razorpay/receipts/:connectorID", h.HandleRazorpayWebhookList)

	type call struct{ method, path, body string }
	calls := []call{
		{"GET", "/v1/health", ""},
		{"GET", "/health", ""},
		{"POST", "/v1/auth/signup", `{}`},
		{"POST", "/v1/auth/login", `{}`},
		{"POST", "/v1/auth/refresh", `{}`},
		{"POST", "/v1/auth/logout", `{}`},
		{"GET", "/v1/auth/me", ""},
		{"GET", "/v1/auth/principal", ""},
		{"POST", "/v1/webhooks/razorpay/not-a-uuid", `{}`},
		{"GET", "/v1/webhooks/razorpay/receipt/not-a-uuid", ""},
		{"GET", "/v1/webhooks/razorpay/receipts/not-a-uuid", ""},
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
	}
}
