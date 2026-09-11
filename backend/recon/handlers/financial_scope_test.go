package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"zord-outcome-engine/internal/auth"

	"github.com/gin-gonic/gin"
)

func TestScopeUsesPrincipalWhenQueryDisagrees(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &FinancialHandler{}
	r := gin.New()
	r.GET("/v1/x", func(c *gin.Context) {
		c.Request = c.Request.WithContext(auth.WithPrincipalForTest(c.Request.Context(), "tenant-A"))
		tenant, connector, ok := h.scope(c)
		if !ok {
			return
		}
		c.JSON(http.StatusOK, gin.H{"tenant": tenant, "connector": connector})
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/x?tenant_id=tenant-B&connector_id=c1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestScopeUsesPrincipalWhenQueryOmitted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &FinancialHandler{}
	r := gin.New()
	r.GET("/v1/x", func(c *gin.Context) {
		c.Request = c.Request.WithContext(auth.WithPrincipalForTest(c.Request.Context(), "tenant-A"))
		tenant, connector, ok := h.scope(c)
		if !ok {
			return
		}
		c.JSON(http.StatusOK, gin.H{"tenant": tenant, "connector": connector})
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/x?connector_id=c1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"tenant":"tenant-A"`) {
		t.Fatalf("body=%s", w.Body.String())
	}
}
