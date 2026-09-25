package services

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/google/uuid"
)

// Roles edge can put in an access token. PLATFORM_ADMIN, PAYOUT_APPROVER and
// CONNECTOR_ADMIN are "controlled": they are never granted at signup (every signup gets
// CUSTOMER_ADMIN, D39) and can only be set through GrantUserRole, which is
// exposed solely behind the internal admin key (POST /v1/admin/roles/grant).
const (
	RoleCustomerAdmin  = roleCustomerAdmin
	RolePlatformAdmin  = "PLATFORM_ADMIN"
	RolePayoutApprover = "PAYOUT_APPROVER"
	RoleConnectorAdmin = "CONNECTOR_ADMIN"
)

var (
	ErrRoleNotGrantable = errors.New("role is not grantable")
	ErrRoleUserNotFound = errors.New("user not found in tenant")
)

// IsControlledRole reports whether role may only be minted via the controlled
// admin path (never via signup).
func IsControlledRole(role string) bool {
	switch strings.ToUpper(strings.TrimSpace(role)) {
	case RolePlatformAdmin, RolePayoutApprover, RoleConnectorAdmin:
		return true
	}
	return false
}

// signupRole is the only role a self-service signup can receive. It is a
// function (not an inline literal) so tests can pin it.
func signupRole() string { return roleCustomerAdmin }

// SignupRole exposes signupRole for handler-level tests.
func SignupRole() string { return signupRole() }

// grantableRoles: the controlled roles, plus CUSTOMER_ADMIN so an admin
// can revoke a controlled role by setting the user back.
var grantableRoles = map[string]struct{}{
	RolePlatformAdmin:  {},
	RolePayoutApprover: {},
	RoleConnectorAdmin: {},
	RoleCustomerAdmin:  {},
}

// RoleStore persists a role change. SQLRoleStore is the production impl.
type RoleStore interface {
	SetUserRole(ctx context.Context, tenantID, userID uuid.UUID, role string) (email string, err error)
	AuditRoleChange(ctx context.Context, tenantID, userID uuid.UUID, eventType, ip, userAgent string)
}

// RoleGrant is the result of a successful grant.
type RoleGrant struct {
	TenantID uuid.UUID
	UserID   uuid.UUID
	Email    string
	Role     string
}

// GrantUserRole sets a user's role. The new role is minted into the user's
// next access token (login and refresh both read auth_users.role).
func GrantUserRole(ctx context.Context, store RoleStore, tenantID, userID uuid.UUID, role, ip, userAgent string) (RoleGrant, error) {
	role = strings.ToUpper(strings.TrimSpace(role))
	if _, ok := grantableRoles[role]; !ok {
		return RoleGrant{}, ErrRoleNotGrantable
	}
	if tenantID == uuid.Nil || userID == uuid.Nil {
		return RoleGrant{}, ErrRoleUserNotFound
	}
	email, err := store.SetUserRole(ctx, tenantID, userID, role)
	if err != nil {
		return RoleGrant{}, err
	}
	store.AuditRoleChange(ctx, tenantID, userID, "ROLE_GRANTED:"+role, ip, userAgent)
	return RoleGrant{TenantID: tenantID, UserID: userID, Email: email, Role: role}, nil
}

// SQLRoleStore updates auth_users.role scoped by tenant_id + user_id.
type SQLRoleStore struct{ DB *sql.DB }

func (s SQLRoleStore) SetUserRole(ctx context.Context, tenantID, userID uuid.UUID, role string) (string, error) {
	var email string
	err := s.DB.QueryRowContext(ctx,
		`UPDATE auth_users SET role = $1, updated_at = now() WHERE tenant_id = $2 AND user_id = $3 RETURNING email`,
		role, tenantID, userID,
	).Scan(&email)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrRoleUserNotFound
	}
	return email, err
}

func (s SQLRoleStore) AuditRoleChange(ctx context.Context, tenantID, userID uuid.UUID, eventType, ip, userAgent string) {
	writeAuditEvent(ctx, s.DB, tenantID, &userID, eventType, ip, userAgent)
}
