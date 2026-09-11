package auth

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

const testJWTSecret = "test-only-jwt-signing-secret-not-for-production"

func TestMain(m *testing.M) {
	_ = os.Setenv("JWT_SIGNING_SECRET", testJWTSecret)
	if err := InitJWTSigningSecret(); err != nil {
		panic(err)
	}
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

func signTenantToken(t *testing.T, tenantID string) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, &accessClaims{
		TenantID: tenantID,
		UserID:   "user-1",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "zord-edge",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	})
	signed, err := tok.SignedString([]byte(testJWTSecret))
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func protectRouter() *gin.Engine {
	r := gin.New()
	r.Use(GinProtect())
	r.GET("/v1/x", func(c *gin.Context) {
		tenant, ok := PrincipalTenant(c)
		if !ok {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "missing principal"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"principal": tenant, "query": c.Query("tenant_id")})
	})
	return r
}

func TestGinProtectRejectsMismatchedQueryTenant(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/x?tenant_id=tenant-B", nil)
	req.Header.Set("Authorization", "Bearer "+signTenantToken(t, "tenant-A"))
	w := httptest.NewRecorder()
	protectRouter().ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestGinProtectRejectsHeaderQueryDisagreement(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/x?tenant_id=tenant-B", nil)
	req.Header.Set("Authorization", "Bearer "+signTenantToken(t, "tenant-A"))
	req.Header.Set("X-Tenant-ID", "tenant-A")
	w := httptest.NewRecorder()
	protectRouter().ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("header+query disagreement must 403, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestGinProtectAllowsMatchingHeaderAndQuery(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/x?tenant_id=tenant-A", nil)
	req.Header.Set("Authorization", "Bearer "+signTenantToken(t, "tenant-A"))
	req.Header.Set("X-Tenant-ID", "tenant-A")
	w := httptest.NewRecorder()
	protectRouter().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestRequestedTenantsCollectsEveryValue(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/x?tenant_id=tenant-B", nil)
	req.Header.Set("X-Tenant-ID", "tenant-A")
	got := requestedTenants(req)
	if len(got) != 2 {
		t.Fatalf("tenants=%v", got)
	}
	if mismatchedRequestedTenant("tenant-A", got) != "tenant-B" {
		t.Fatalf("expected tenant-B mismatch, got %q from %v", mismatchedRequestedTenant("tenant-A", got), got)
	}
}
