package services

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestSignup_NeverGrantsPayoutApprover (Slice 8, D39): the access token a
// fresh signup receives never carries PAYOUT_APPROVER, so zord-intent-engine's
// /v1/admin/intents/{id}/approve (RequireRole PAYOUT_APPROVER) refuses it.
// The role string must stay byte-identical to intents' auth.RolePayoutApprover.
func TestSignup_NeverGrantsPayoutApprover(t *testing.T) {
	if RolePayoutApprover != "PAYOUT_APPROVER" {
		t.Fatalf("role literal drifted from intents' auth.RolePayoutApprover: %q", RolePayoutApprover)
	}
	role := SignupRole()
	if strings.EqualFold(strings.TrimSpace(role), RolePayoutApprover) || IsControlledRole(role) {
		t.Fatalf("signup role %q must never be PAYOUT_APPROVER or another controlled role", role)
	}
	if role != RoleCustomerAdmin {
		t.Fatalf("signup role=%q, want CUSTOMER_ADMIN (which is not an approver)", role)
	}

	t.Setenv("JWT_SIGNING_SECRET", "test-only-edge-jwt-secret")
	t.Setenv("JWT_AUDIENCE", "")
	t.Setenv("JWT_ISSUER", "")
	if err := InitJWTSigningSecret(); err != nil {
		t.Fatal(err)
	}
	// Same call SignupNewTenant makes for the signup token.
	tokens, err := IssueTokens(uuid.New(), uuid.New(), "new@example.com", SignupRole(), time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := ParseAccessToken(tokens.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if strings.EqualFold(claims.Role, RolePayoutApprover) {
		t.Fatal("signup access token carries PAYOUT_APPROVER")
	}
}
