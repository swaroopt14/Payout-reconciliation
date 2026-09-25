package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"zord-intent-engine/internal/auth"
	"zord-intent-engine/internal/handlers"

	"github.com/golang-jwt/jwt/v5"
)

const (
	testJWTSecret    = "test-jwt-secret-slice8"
	testTenant       = "11111111-1111-1111-1111-111111111111"
	testOtherTenant  = "99999999-9999-9999-9999-999999999999"
	testConsoleToken = "console-internal-token"
	testRelayToken   = "relay-internal-token"
)

func TestMain(m *testing.M) {
	os.Setenv("JWT_SIGNING_SECRET", testJWTSecret)
	os.Setenv("JWT_ISSUER", "zord-edge")
	os.Setenv("INTENT_ENGINE_INTERNAL_SERVICE_TOKEN", testConsoleToken)
	os.Setenv("RELAY_AUTH_TOKEN", testRelayToken)
	if err := auth.InitJWTSigningSecret(); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

func mintToken(t *testing.T, tenantID, userID, role string) string {
	t.Helper()
	claims := auth.AccessClaims{
		TenantID: tenantID, UserID: userID, Email: userID + "@example.com", Role: role, SessionID: "s1",
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "zord-edge",
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
		},
	}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testJWTSecret))
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

type fakeApprover struct {
	calls      int
	tenantID   string
	intentID   string
	approvedBy string
}

func (f *fakeApprover) ApproveHeldIntent(_ context.Context, tenantID, intentID, approvedBy string) (string, error) {
	f.calls++
	f.tenantID, f.intentID, f.approvedBy = tenantID, intentID, approvedBy
	return "ACCEPT", nil
}

func okHandler(called *int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		*called++
		w.WriteHeader(http.StatusOK)
	}
}

func adminMux(approver *fakeApprover, dummyCalls *int) *http.ServeMux {
	mux := http.NewServeMux()
	registerAdminRoutes(mux, adminRouteHandlers{
		MappingProfilesListOrCreate: okHandler(dummyCalls),
		MappingProfileItem:          okHandler(dummyCalls),
		TenantSynonymsListOrCreate:  okHandler(dummyCalls),
		TenantSynonymDeactivate:     okHandler(dummyCalls),
		IntentApprove:               handlers.NewIntentApprovalHandler(approver).Approve,
	})
	return mux
}

func approveReq(authz string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/intents/intent-123/approve", nil)
	req.Header.Set("X-Tenant-ID", testTenant)
	if authz != "" {
		req.Header.Set("Authorization", authz)
	}
	return req
}

func serve(mux http.Handler, req *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	return w
}

func TestApprove_RequiresPayoutApprover(t *testing.T) {
	approver := &fakeApprover{}
	var dummy int
	mux := adminMux(approver, &dummy)

	if w := serve(mux, approveReq("")); w.Code != http.StatusUnauthorized {
		t.Fatalf("no token: want 401 got %d", w.Code)
	}
	for _, role := range []string{"", "ACTION_APPROVER", "POLICY_ADMIN", "PLATFORM_ADMIN", "payout_approver_x"} {
		if w := serve(mux, approveReq("Bearer "+mintToken(t, testTenant, "user-x", role))); w.Code != http.StatusForbidden {
			t.Fatalf("role %q: want 403 got %d", role, w.Code)
		}
	}
	if approver.calls != 0 {
		t.Fatalf("approver reached %d times without PAYOUT_APPROVER", approver.calls)
	}

	w := serve(mux, approveReq("Bearer "+mintToken(t, testTenant, "approver-7", auth.RolePayoutApprover)))
	if w.Code != http.StatusOK {
		t.Fatalf("PAYOUT_APPROVER: want 200 got %d body=%s", w.Code, w.Body.String())
	}
	if approver.calls != 1 || approver.approvedBy != "approver-7" || approver.tenantID != testTenant || approver.intentID != "intent-123" {
		t.Fatalf("approver got %+v", approver)
	}

	// A PAYOUT_APPROVER of another tenant cannot approve this tenant's intent.
	w = serve(mux, approveReq("Bearer "+mintToken(t, testOtherTenant, "approver-9", auth.RolePayoutApprover)))
	if w.Code != http.StatusForbidden || approver.calls != 1 {
		t.Fatalf("cross-tenant approver: want 403 got %d calls=%d", w.Code, approver.calls)
	}
}

func TestApprove_CustomerAdminForbidden(t *testing.T) {
	approver := &fakeApprover{}
	var dummy int
	mux := adminMux(approver, &dummy)
	w := serve(mux, approveReq("Bearer "+mintToken(t, testTenant, "admin-1", "CUSTOMER_ADMIN")))
	if w.Code != http.StatusForbidden {
		t.Fatalf("CUSTOMER_ADMIN: want 403 got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "ROLE_FORBIDDEN") {
		t.Fatalf("want ROLE_FORBIDDEN body, got %s", w.Body.String())
	}
	if approver.calls != 0 {
		t.Fatal("CUSTOMER_ADMIN must never reach the approver")
	}
}

func TestApprove_APIKeyForbidden(t *testing.T) {
	approver := &fakeApprover{}
	var dummy int
	mux := adminMux(approver, &dummy)

	// Edge's tenant API key shape ("prefix.secret") as a bearer: rejected.
	if w := serve(mux, approveReq("Bearer zl_live_abc.s3cr3t")); w.Code != http.StatusUnauthorized && w.Code != http.StatusForbidden {
		t.Fatalf("API key bearer: want 401/403 got %d", w.Code)
	}
	// ApiKey scheme: rejected.
	if w := serve(mux, approveReq("ApiKey zl_live_abc.s3cr3t")); w.Code != http.StatusUnauthorized && w.Code != http.StatusForbidden {
		t.Fatalf("ApiKey scheme: want 401/403 got %d", w.Code)
	}
	// A valid approver JWT with an API key riding along: rejected (403).
	req := approveReq("Bearer " + mintToken(t, testTenant, "approver-7", auth.RolePayoutApprover))
	req.Header.Set("X-API-Key", "zl_live_abc.s3cr3t")
	if w := serve(mux, req); w.Code != http.StatusForbidden {
		t.Fatalf("JWT + X-API-Key: want 403 got %d", w.Code)
	}
	// A non-user principal (API key / service) holding the role string: 403.
	guard := auth.RequireRole(auth.RolePayoutApprover, handlers.NewIntentApprovalHandler(approver).Approve)
	for _, p := range []auth.AuthPrincipal{
		{SubjectID: "key-1", SubjectType: "api_key", TenantID: testTenant, Roles: []string{auth.RolePayoutApprover}, AuthMethod: "api_key"},
		{SubjectID: "svc", SubjectType: "service", TenantID: testTenant, Roles: []string{auth.RolePayoutApprover}, AuthMethod: "service_token"},
	} {
		r := approveReq("")
		r = r.WithContext(auth.WithPrincipal(r.Context(), p))
		if w := serve(guard, r); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "USER_REQUIRED") {
			t.Fatalf("%s principal: want 403 USER_REQUIRED got %d %s", p.SubjectType, w.Code, w.Body.String())
		}
	}
	if approver.calls != 0 {
		t.Fatalf("API key reached approver %d times", approver.calls)
	}
}

func TestAdminRoutes_RequireAuth(t *testing.T) {
	approver := &fakeApprover{}
	var dummy int
	mux := adminMux(approver, &dummy)
	paths := []struct{ method, path string }{
		{http.MethodGet, "/v1/admin/mapping-profiles"},
		{http.MethodPost, "/v1/admin/mapping-profiles"},
		{http.MethodGet, "/v1/admin/mapping-profiles/p1"},
		{http.MethodGet, "/v1/admin/tenant-synonyms"},
		{http.MethodPost, "/v1/admin/tenant-synonyms"},
		{http.MethodDelete, "/v1/admin/tenant-synonyms/s1"},
		{http.MethodPost, "/v1/admin/intents/i1/approve"},
	}
	for _, p := range paths {
		// No credentials, and a relay token alone, are both unauthenticated here.
		for _, hdr := range []map[string]string{{}, {"X-Relay-Token": testRelayToken}, {"Authorization": "Bearer not-a-jwt"}} {
			req := httptest.NewRequest(p.method, p.path, nil)
			for k, v := range hdr {
				req.Header.Set(k, v)
			}
			if w := serve(mux, req); w.Code != http.StatusUnauthorized {
				t.Fatalf("%s %s headers=%v: want 401 got %d", p.method, p.path, hdr, w.Code)
			}
		}
	}
	if dummy != 0 || approver.calls != 0 {
		t.Fatalf("unauthenticated request reached a handler (dummy=%d approver=%d)", dummy, approver.calls)
	}

	// Authenticated user of the same tenant reaches CRUD; another tenant's
	// tenant_id is refused (RequireTenantMatch).
	tok := mintToken(t, testTenant, "admin-1", "CUSTOMER_ADMIN")
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/mapping-profiles", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("X-Tenant-ID", testTenant)
	if w := serve(mux, req); w.Code != http.StatusOK || dummy != 1 {
		t.Fatalf("authenticated admin CRUD: want 200 got %d dummy=%d", w.Code, dummy)
	}
	req = httptest.NewRequest(http.MethodGet, "/v1/admin/tenant-synonyms", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("X-Tenant-ID", testOtherTenant)
	if w := serve(mux, req); w.Code != http.StatusForbidden || dummy != 1 {
		t.Fatalf("tenant mismatch: want 403 got %d", w.Code)
	}

	// main.go must not register any admin or internal route outside the
	// guarded helpers.
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if m := regexp.MustCompile(`HandleFunc\("/(v1/admin|internal)/`).FindString(string(src)); m != "" {
		t.Fatalf("main.go registers an unguarded route: %s", m)
	}
}

func TestInternalRoutes_RequireInternalScope(t *testing.T) {
	var called int
	h := okHandler(&called)
	routes := internalRoutes(internalRouteHandlers{
		DLQCount: h, OutboxLease: h, OutboxAck: h, OutboxNack: h,
		DLQLease: h, DLQAck: h, DLQNack: h,
		BatchLease: h, BatchAck: h, BatchNack: h,
		AirflowTransform: h, NormalizationQuality: h,
	})
	if len(routes) != 12 {
		t.Fatalf("expected 12 internal routes, got %d", len(routes))
	}
	mux := http.NewServeMux()
	registerInternalRoutes(mux, internalRouteHandlers{
		DLQCount: h, OutboxLease: h, OutboxAck: h, OutboxNack: h,
		DLQLease: h, DLQAck: h, DLQNack: h,
		BatchLease: h, BatchAck: h, BatchNack: h,
		AirflowTransform: h, NormalizationQuality: h,
	})
	userJWT := mintToken(t, testTenant, "u1", auth.RolePayoutApprover)

	for _, rt := range routes {
		if !strings.HasPrefix(rt.Pattern, "/internal/") || rt.Scope == "" || rt.Handler == nil {
			t.Fatalf("bad internal route entry %+v", rt)
		}
		check := func(name string, hdr map[string]string, want int) {
			t.Helper()
			before := called
			req := httptest.NewRequest(http.MethodPost, rt.Pattern, nil)
			for k, v := range hdr {
				req.Header.Set(k, v)
			}
			w := serve(mux, req)
			if w.Code != want {
				t.Fatalf("%s %s: want %d got %d", rt.Pattern, name, want, w.Code)
			}
			if reached := called > before; reached != (want == http.StatusOK) {
				t.Fatalf("%s %s: handler reached=%v with status %d", rt.Pattern, name, reached, w.Code)
			}
		}
		check("no credentials", nil, http.StatusUnauthorized)
		check("user JWT only", map[string]string{"Authorization": "Bearer " + userJWT}, http.StatusUnauthorized)
		check("wrong internal token", map[string]string{"X-Internal-Service-Token": "nope"}, http.StatusUnauthorized)
		check("wrong relay token", map[string]string{"X-Relay-Token": "nope"}, http.StatusUnauthorized)

		if rt.Scope == auth.ScopeIntentReadCrossTenant {
			check("console token", map[string]string{"X-Internal-Service-Token": testConsoleToken}, http.StatusOK)
			check("relay token lacks cross-tenant", map[string]string{"X-Relay-Token": testRelayToken}, http.StatusForbidden)
		} else {
			check("relay token", map[string]string{"X-Relay-Token": testRelayToken}, http.StatusOK)
			check("console token lacks relay scope", map[string]string{"X-Internal-Service-Token": testConsoleToken}, http.StatusForbidden)
		}
	}
}
