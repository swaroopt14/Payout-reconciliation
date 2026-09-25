package services

import (
	"context"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/google/uuid"
)

type fakeRoleStore struct {
	roles  map[uuid.UUID]string
	audits []string
}

func (f *fakeRoleStore) SetUserRole(_ context.Context, _, userID uuid.UUID, role string) (string, error) {
	if _, ok := f.roles[userID]; !ok {
		return "", ErrRoleUserNotFound
	}
	f.roles[userID] = role
	return "ops@example.com", nil
}

func (f *fakeRoleStore) AuditRoleChange(_ context.Context, _, _ uuid.UUID, eventType, _, _ string) {
	f.audits = append(f.audits, eventType)
}

func TestSignupRoleIsNeverControlled(t *testing.T) {
	if signupRole() != "CUSTOMER_ADMIN" {
		t.Fatalf("signup role=%q", signupRole())
	}
	if IsControlledRole(signupRole()) {
		t.Fatal("signup must never yield PLATFORM_ADMIN, PAYOUT_APPROVER or CONNECTOR_ADMIN")
	}
	for _, r := range []string{RolePlatformAdmin, RolePayoutApprover, RoleConnectorAdmin, " payout_approver ", "connector_admin"} {
		if !IsControlledRole(r) {
			t.Fatalf("%q should be controlled", r)
		}
	}
}

// Source guard: SignupNewTenant must take its role only from signupRole(),
// never from a request field or a controlled-role literal.
func TestSignupNewTenantUsesSignupRoleOnly(t *testing.T) {
	src, err := os.ReadFile("user_auth_service.go")
	if err != nil {
		t.Fatal(err)
	}
	body := regexp.MustCompile(`(?s)func SignupNewTenant\(.*?\n}\n`).Find(src)
	if body == nil {
		t.Fatal("SignupNewTenant not found")
	}
	for _, banned := range []string{"PLATFORM_ADMIN", "PAYOUT_APPROVER", "CONNECTOR_ADMIN", "RolePlatformAdmin", "RolePayoutApprover", "RoleConnectorAdmin"} {
		if regexp.MustCompile(regexp.QuoteMeta(banned)).Match(body) {
			t.Fatalf("SignupNewTenant references controlled role %s", banned)
		}
	}
	if n := len(regexp.MustCompile(`signupRole\(\)`).FindAll(body, -1)); n < 3 {
		t.Fatalf("SignupNewTenant should use signupRole() for insert, token and response (got %d)", n)
	}
}

func TestGrantUserRoleMintsControlledRoles(t *testing.T) {
	t.Setenv("JWT_SIGNING_SECRET", "test-only-edge-jwt-secret")
	t.Setenv("JWT_AUDIENCE", "")
	t.Setenv("JWT_ISSUER", "")
	if err := InitJWTSigningSecret(); err != nil {
		t.Fatal(err)
	}
	tenant, user := uuid.New(), uuid.New()
	store := &fakeRoleStore{roles: map[uuid.UUID]string{user: RoleCustomerAdmin}}
	for _, role := range []string{RolePayoutApprover, RolePlatformAdmin, RoleConnectorAdmin} {
		grant, err := GrantUserRole(context.Background(), store, tenant, user, role, "10.0.0.1", "test")
		if err != nil {
			t.Fatalf("grant %s: %v", role, err)
		}
		if store.roles[user] != role || grant.Role != role {
			t.Fatalf("role not persisted: %v", store.roles[user])
		}
		// Next login/refresh mints the stored role into the access token.
		tokens, err := IssueTokens(tenant, user, grant.Email, store.roles[user], timeZero())
		if err != nil {
			t.Fatal(err)
		}
		claims, err := ParseAccessToken(tokens.AccessToken)
		if err != nil {
			t.Fatal(err)
		}
		if claims.Role != role {
			t.Fatalf("minted role=%q want %q", claims.Role, role)
		}
		if len(claims.Audience) != 1 || claims.Audience[0] != "zord-console" {
			t.Fatalf("aud=%v want [zord-console]", claims.Audience)
		}
		if claims.Issuer != "zord-edge" {
			t.Fatalf("iss=%q", claims.Issuer)
		}
	}
	if len(store.audits) != 3 {
		t.Fatalf("each grant must be audited, got %v", store.audits)
	}
}

func TestGrantUserRoleRejectsUnknownRoleAndUser(t *testing.T) {
	tenant, user := uuid.New(), uuid.New()
	store := &fakeRoleStore{roles: map[uuid.UUID]string{user: RoleCustomerAdmin}}
	if _, err := GrantUserRole(context.Background(), store, tenant, user, "SUPERUSER", "", ""); err != ErrRoleNotGrantable {
		t.Fatalf("want ErrRoleNotGrantable, got %v", err)
	}
	if _, err := GrantUserRole(context.Background(), store, tenant, uuid.New(), RolePayoutApprover, "", ""); err != ErrRoleUserNotFound {
		t.Fatalf("want ErrRoleUserNotFound, got %v", err)
	}
	if store.roles[user] != RoleCustomerAdmin {
		t.Fatal("rejected grant must not change role")
	}
}

func timeZero() (t0 time.Time) { return }
