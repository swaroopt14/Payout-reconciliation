package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"zord-edge/middleware"
	"zord-edge/services"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type memRoleStore struct {
	roles map[uuid.UUID]string
}

func (m *memRoleStore) SetUserRole(_ context.Context, _, userID uuid.UUID, role string) (string, error) {
	if _, ok := m.roles[userID]; !ok {
		return "", services.ErrRoleUserNotFound
	}
	m.roles[userID] = role
	return "ops@example.com", nil
}

func (m *memRoleStore) AuditRoleChange(context.Context, uuid.UUID, uuid.UUID, string, string, string) {
}

func roleRouter(store services.RoleStore) *gin.Engine {
	gin.SetMode(gin.TestMode)
	roleStoreFactory = func() services.RoleStore { return store }
	r := gin.New()
	admin := r.Group("/v1/admin")
	admin.Use(middleware.AdminAuthMiddleware())
	admin.POST("/roles/grant", GrantRole)
	return r
}

func grantReq(key string, body map[string]string) *http.Request {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/roles/grant", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("X-Zord-ADMIN-KEY", key)
	}
	return req
}

func TestGrantRoleRejectsUnauthorizedCallers(t *testing.T) {
	user := uuid.New()
	store := &memRoleStore{roles: map[uuid.UUID]string{user: services.RoleCustomerAdmin}}
	body := map[string]string{"tenant_id": uuid.NewString(), "user_id": user.String(), "role": services.RolePayoutApprover}

	// Admin key not configured: fail closed even with a header.
	t.Setenv("INTERNAL_ADMIN_KEY", "")
	w := httptest.NewRecorder()
	roleRouter(store).ServeHTTP(w, grantReq("anything", body))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unconfigured key: status=%d", w.Code)
	}

	t.Setenv("INTERNAL_ADMIN_KEY", "test-only-admin-key")
	for _, key := range []string{"", "wrong-key", "test-only-admin-key-extra"} {
		w = httptest.NewRecorder()
		roleRouter(store).ServeHTTP(w, grantReq(key, body))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("key %q: status=%d", key, w.Code)
		}
	}
	// A customer JWT is not an admin credential either.
	w = httptest.NewRecorder()
	req := grantReq("", body)
	req.Header.Set("Authorization", "Bearer some.customer.jwt")
	roleRouter(store).ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("bearer-only: status=%d", w.Code)
	}
	if store.roles[user] != services.RoleCustomerAdmin {
		t.Fatalf("unauthorized call changed role to %s", store.roles[user])
	}
}

func TestGrantRoleWithAdminKeyMintsControlledRoles(t *testing.T) {
	t.Setenv("INTERNAL_ADMIN_KEY", "test-only-admin-key")
	tenant, user := uuid.New(), uuid.New()
	store := &memRoleStore{roles: map[uuid.UUID]string{user: services.RoleCustomerAdmin}}
	for _, role := range []string{services.RolePayoutApprover, services.RolePlatformAdmin, services.RoleConnectorAdmin} {
		w := httptest.NewRecorder()
		roleRouter(store).ServeHTTP(w, grantReq("test-only-admin-key", map[string]string{
			"tenant_id": tenant.String(), "user_id": user.String(), "role": role,
		}))
		if w.Code != http.StatusOK {
			t.Fatalf("grant %s: status=%d body=%s", role, w.Code, w.Body.String())
		}
		if store.roles[user] != role {
			t.Fatalf("role=%s want %s", store.roles[user], role)
		}
	}
	w := httptest.NewRecorder()
	roleRouter(store).ServeHTTP(w, grantReq("test-only-admin-key", map[string]string{
		"tenant_id": tenant.String(), "user_id": user.String(), "role": "SUPERUSER",
	}))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unknown role: status=%d", w.Code)
	}
}

// Signup has no role input at all: a client-supplied "role" is dropped.
func TestSignupRequestHasNoRoleField(t *testing.T) {
	typ := reflect.TypeOf(signupRequest{})
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if strings.EqualFold(f.Name, "role") || strings.Contains(strings.ToLower(f.Tag.Get("json")), "role") {
			t.Fatalf("signupRequest must not accept a role (field %s)", f.Name)
		}
	}
	var req signupRequest
	_ = json.Unmarshal([]byte(`{"tenant_name":"t","name":"n","email":"a@b.co","password":"12345678","role":"PLATFORM_ADMIN"}`), &req)
	if services.IsControlledRole(services.SignupRole()) {
		t.Fatal("signup role must not be controlled")
	}
}
