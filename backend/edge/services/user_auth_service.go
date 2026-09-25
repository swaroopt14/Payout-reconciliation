package services

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"zord-edge/security"

	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

type AuthUser struct {
	UserID    uuid.UUID `json:"user_id"`
	TenantID  uuid.UUID `json:"tenant_id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	Role      string    `json:"role"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

type AuthTenant struct {
	TenantID   uuid.UUID `json:"tenant_id"`
	TenantName string    `json:"tenant_name"`
	KeyPrefix  string    `json:"-"`                 // tenants.key_prefix → workspace_code in JSON
	APIKey     string    `json:"api_key,omitempty"` // returned only on signup
}

type AuthBundle struct {
	User              AuthUser
	Tenant            AuthTenant
	AccessToken       string
	AccessExpiresAt   time.Time
	RefreshToken      string
	RefreshExpiresAt  time.Time
	IdleExpiresAt     time.Time // now + 15 min; reset on activity
	AbsoluteExpiresAt time.Time // login + 8 h; never extended
}

// Session timeout sentinel errors.
var (
	ErrInvalidCredentials     = errors.New("invalid email or password")
	ErrAccountLocked          = errors.New("account temporarily locked due to failed login attempts")
	ErrAccountDisabled        = errors.New("account is disabled")
	ErrEmailTaken             = errors.New("email already registered")
	ErrTenantNameTaken        = errors.New("tenant name already in use")
	ErrSessionIdleExpired     = errors.New("session expired due to inactivity")
	ErrSessionAbsoluteExpired = errors.New("session absolute lifetime exceeded")
)

const (
	roleCustomerAdmin = "CUSTOMER_ADMIN"
	statusActive      = "ACTIVE"
	lockoutThreshold  = 5
	lockoutDuration   = 15 * time.Minute
	// idleWindow is the server-side idle timeout; must match accessTokenTTL.
	idleWindow = 15 * time.Minute
)

func hashRefreshToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// SignupNewTenant creates a tenant + first admin user atomically and issues tokens.
func SignupNewTenant(ctx context.Context, db *sql.DB, tenantName, name, email, password, ip, userAgent string) (*AuthBundle, error) {
	tenantName = strings.TrimSpace(tenantName)
	name = strings.TrimSpace(name)
	email = normalizeEmail(email)

	if tenantName == "" || name == "" || email == "" || len(password) < 8 {
		return nil, errors.New("tenant_name, name, email, and password (min 8 chars) are required")
	}

	fullAPIKey, prefix, secret, err := GenerateApiKey(tenantName)
	if err != nil {
		return nil, err
	}
	keyHash, err := security.HashApiKey(secret)
	if err != nil {
		return nil, err
	}
	pwHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var tenantID uuid.UUID
	err = tx.QueryRowContext(ctx,
		`INSERT INTO tenants (tenant_name, key_prefix, key_hash) VALUES ($1, $2, $3) RETURNING tenant_id`,
		tenantName, prefix, keyHash,
	).Scan(&tenantID)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrTenantNameTaken
		}
		return nil, err
	}

	userID := uuid.New()
	_, err = tx.ExecContext(ctx,
		`INSERT INTO auth_users (user_id, tenant_id, email, password_hash, role, status, name)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		userID, tenantID, email, string(pwHash), signupRole(), statusActive, name,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrEmailTaken
		}
		return nil, err
	}

	// Signup never mints a controlled role (D39); see role_grant.go.
	tokens, err := IssueTokens(tenantID, userID, email, signupRole(), time.Time{})
	if err != nil {
		return nil, err
	}
	if err := storeRefreshTokenTx(ctx, tx, tokens, tenantID, userID, ip, userAgent); err != nil {
		return nil, err
	}

	writeAuditEventTx(ctx, tx, tenantID, &userID, "USER_SIGNUP", ip, userAgent)

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &AuthBundle{
		User: AuthUser{
			UserID:   userID,
			TenantID: tenantID,
			Email:    email,
			Name:     name,
			Role:     signupRole(),
			Status:   statusActive,
		},
		Tenant: AuthTenant{
			TenantID:   tenantID,
			TenantName: tenantName,
			KeyPrefix:  prefix,
			APIKey:     fullAPIKey,
		},
		AccessToken:      tokens.AccessToken,
		AccessExpiresAt:  tokens.AccessExpiresAt,
		RefreshToken:     tokens.RefreshToken,
		RefreshExpiresAt: tokens.RefreshExpiresAt,
	}, nil
}

// LoginUser verifies credentials, tracks failed attempts, and issues tokens.
func LoginUser(ctx context.Context, db *sql.DB, email, password, ip, userAgent string) (*AuthBundle, error) {
	email = normalizeEmail(email)
	if email == "" || password == "" {
		return nil, ErrInvalidCredentials
	}

	var (
		userID         uuid.UUID
		tenantID       uuid.UUID
		passwordHash   string
		role           string
		status         string
		failedAttempts int
		lockedUntil    sql.NullTime
		name           string
		tenantName     string
		keyPrefix      string
	)
	err := db.QueryRowContext(ctx,
		`SELECT u.user_id, u.tenant_id, u.password_hash, u.role, u.status, u.failed_login_attempts, u.locked_until, u.name, t.tenant_name, t.key_prefix
		 FROM auth_users u
		 JOIN tenants t ON t.tenant_id = u.tenant_id
		 WHERE u.email = $1`,
		email,
	).Scan(&userID, &tenantID, &passwordHash, &role, &status, &failedAttempts, &lockedUntil, &name, &tenantName, &keyPrefix)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}

	if status != statusActive {
		return nil, ErrAccountDisabled
	}
	if lockedUntil.Valid && lockedUntil.Time.After(time.Now().UTC()) {
		return nil, ErrAccountLocked
	}

	if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password)); err != nil {
		newAttempts := failedAttempts + 1
		var lockUntil sql.NullTime
		if newAttempts >= lockoutThreshold {
			lockUntil = sql.NullTime{Time: time.Now().UTC().Add(lockoutDuration), Valid: true}
		}
		_, _ = db.ExecContext(ctx,
			`UPDATE auth_users SET failed_login_attempts = $1, locked_until = $2, updated_at = now() WHERE user_id = $3`,
			newAttempts, lockUntil, userID,
		)
		writeAuditEvent(ctx, db, tenantID, &userID, "LOGIN_FAILED", ip, userAgent)
		return nil, ErrInvalidCredentials
	}

	tokens, err := IssueTokens(tenantID, userID, email, role, time.Time{})
	if err != nil {
		return nil, err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`UPDATE auth_users SET failed_login_attempts = 0, locked_until = NULL, last_login_at = now(), updated_at = now() WHERE user_id = $1`,
		userID,
	); err != nil {
		return nil, err
	}
	if err := storeRefreshTokenTx(ctx, tx, tokens, tenantID, userID, ip, userAgent); err != nil {
		return nil, err
	}
	writeAuditEventTx(ctx, tx, tenantID, &userID, "LOGIN_SUCCESS", ip, userAgent)
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &AuthBundle{
		User: AuthUser{
			UserID:   userID,
			TenantID: tenantID,
			Email:    email,
			Name:     name,
			Role:     role,
			Status:   status,
		},
		Tenant: AuthTenant{
			TenantID:   tenantID,
			TenantName: tenantName,
			KeyPrefix:  keyPrefix,
		},
		AccessToken:      tokens.AccessToken,
		AccessExpiresAt:  tokens.AccessExpiresAt,
		RefreshToken:     tokens.RefreshToken,
		RefreshExpiresAt: tokens.RefreshExpiresAt,
	}, nil
}

// RefreshSession validates a refresh token, enforces idle and absolute session
// timeouts, rotates the token, and issues fresh access + refresh tokens.
func RefreshSession(ctx context.Context, db *sql.DB, refreshTokenStr, ip, userAgent string) (*AuthBundle, error) {
	claims, err := ParseRefreshToken(refreshTokenStr)
	if err != nil {
		return nil, ErrInvalidCredentials
	}

	tokenHash := hashRefreshToken(refreshTokenStr)
	var (
		revokedAt       sql.NullTime
		expiresAt       time.Time
		idleExpiresAt   time.Time
		absoluteExpires time.Time
	)
	err = db.QueryRowContext(ctx,
		`SELECT revoked_at, expires_at, idle_expires_at, absolute_expires_at
		 FROM auth_refresh_tokens
		 WHERE token_id = $1 AND token_hash = $2`,
		claims.TokenID, tokenHash,
	).Scan(&revokedAt, &expiresAt, &idleExpiresAt, &absoluteExpires)
	if err != nil {
		return nil, ErrInvalidCredentials
	}
	if revokedAt.Valid {
		return nil, ErrInvalidCredentials
	}
	// Backend-authoritative timeout checks (OWASP mandatory server-side enforcement).
	now := time.Now().UTC()
	if idleExpiresAt.Before(now) {
		return nil, ErrSessionIdleExpired
	}
	if absoluteExpires.Before(now) {
		return nil, ErrSessionAbsoluteExpired
	}
	// Also honour the JWT exp claim as a belt-and-suspenders check.
	if expiresAt.Before(now) {
		return nil, ErrSessionAbsoluteExpired
	}

	var (
		email      string
		role       string
		status     string
		name       string
		tenantName string
		keyPrefix  string
	)
	err = db.QueryRowContext(ctx,
		`SELECT u.email, u.role, u.status, u.name, t.tenant_name, t.key_prefix
		 FROM auth_users u JOIN tenants t ON t.tenant_id = u.tenant_id
		 WHERE u.user_id = $1`,
		claims.UserID,
	).Scan(&email, &role, &status, &name, &tenantName, &keyPrefix)
	if err != nil {
		return nil, ErrInvalidCredentials
	}
	if status != statusActive {
		return nil, ErrAccountDisabled
	}

	// Carry the original session creation time forward so the absolute wall is preserved.
	sessionCreatedAt := time.Unix(claims.SessionCreatedAt, 0).UTC()
	if sessionCreatedAt.IsZero() {
		// Older tokens (before this field was added) fall back to absolute_expires_at - 8 h.
		sessionCreatedAt = absoluteExpires.Add(-refreshTokenTTL)
	}

	tokens, err := IssueTokens(claims.TenantID, claims.UserID, email, role, sessionCreatedAt)
	if err != nil {
		return nil, err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`UPDATE auth_refresh_tokens
		 SET revoked_at = now(), replaced_by_token_id = $1, updated_at = now()
		 WHERE token_id = $2`,
		tokens.RefreshTokenID.String(), claims.TokenID,
	); err != nil {
		return nil, err
	}
	if err := storeRefreshTokenTx(ctx, tx, tokens, claims.TenantID, claims.UserID, ip, userAgent); err != nil {
		return nil, err
	}
	writeAuditEventTx(ctx, tx, claims.TenantID, &claims.UserID, "TOKEN_REFRESH", ip, userAgent)
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return &AuthBundle{
		User: AuthUser{
			UserID:   claims.UserID,
			TenantID: claims.TenantID,
			Email:    email,
			Name:     name,
			Role:     role,
			Status:   status,
		},
		Tenant: AuthTenant{
			TenantID:   claims.TenantID,
			TenantName: tenantName,
			KeyPrefix:  keyPrefix,
		},
		AccessToken:       tokens.AccessToken,
		AccessExpiresAt:   tokens.AccessExpiresAt,
		RefreshToken:      tokens.RefreshToken,
		RefreshExpiresAt:  tokens.RefreshExpiresAt,
		IdleExpiresAt:     tokens.AccessExpiresAt.Add(idleWindow - accessTokenTTL), // idle = now+15m
		AbsoluteExpiresAt: sessionCreatedAt.Add(refreshTokenTTL),
	}, nil
}

// RevokeRefreshToken marks a refresh token as revoked (logout).
func RevokeRefreshToken(ctx context.Context, db *sql.DB, refreshTokenStr, ip, userAgent string) error {
	claims, err := ParseRefreshToken(refreshTokenStr)
	if err != nil {
		return nil // logout is idempotent
	}
	_, _ = db.ExecContext(ctx,
		`UPDATE auth_refresh_tokens SET revoked_at = now(), updated_at = now() WHERE token_id = $1 AND revoked_at IS NULL`,
		claims.TokenID,
	)
	writeAuditEvent(ctx, db, claims.TenantID, &claims.UserID, "LOGOUT", ip, userAgent)
	return nil
}

// RevokeAllUserSessions marks all refresh tokens for a user as revoked (logout everywhere).
func RevokeAllUserSessions(ctx context.Context, db *sql.DB, tenantID, userID uuid.UUID, ip, userAgent string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE auth_refresh_tokens SET revoked_at = now(), updated_at = now() WHERE user_id = $1 AND revoked_at IS NULL`,
		userID,
	)
	writeAuditEvent(ctx, db, tenantID, &userID, "LOGOUT_ALL_SESSIONS", ip, userAgent)
	return err
}

// GetUserByID returns user + tenant details for /me.
func GetUserByID(ctx context.Context, db *sql.DB, userID uuid.UUID) (*AuthUser, *AuthTenant, error) {
	var (
		tenantID   uuid.UUID
		email      string
		name       string
		role       string
		status     string
		createdAt  time.Time
		tenantName string
		keyPrefix  string
	)
	err := db.QueryRowContext(ctx,
		`SELECT u.tenant_id, u.email, u.name, u.role, u.status, u.created_at, t.tenant_name, t.key_prefix
		 FROM auth_users u JOIN tenants t ON t.tenant_id = u.tenant_id
		 WHERE u.user_id = $1`,
		userID,
	).Scan(&tenantID, &email, &name, &role, &status, &createdAt, &tenantName, &keyPrefix)
	if err != nil {
		return nil, nil, err
	}
	return &AuthUser{
			UserID:    userID,
			TenantID:  tenantID,
			Email:     email,
			Name:      name,
			Role:      role,
			Status:    status,
			CreatedAt: createdAt,
		}, &AuthTenant{
			TenantID:   tenantID,
			TenantName: tenantName,
			KeyPrefix:  keyPrefix,
		}, nil
}

func storeRefreshTokenTx(ctx context.Context, tx *sql.Tx, tokens *IssuedTokens, tenantID, userID uuid.UUID, ip, userAgent string) error {
	now := time.Now().UTC()
	idleExp := now.Add(idleWindow)
	absoluteExp := tokens.SessionCreatedAt.Add(refreshTokenTTL)
	_, err := tx.ExecContext(ctx,
		`INSERT INTO auth_refresh_tokens
			(token_id, user_id, tenant_id, session_id, token_hash, expires_at,
			 created_ip, created_user_agent, idle_expires_at, absolute_expires_at, last_activity_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		tokens.RefreshTokenID, userID, tenantID, tokens.SessionID,
		hashRefreshToken(tokens.RefreshToken), tokens.RefreshExpiresAt,
		nullableString(ip), nullableString(userAgent),
		idleExp, absoluteExp, now,
	)
	return err
}

func writeAuditEvent(ctx context.Context, db *sql.DB, tenantID uuid.UUID, userID *uuid.UUID, eventType, ip, userAgent string) {
	_, _ = db.ExecContext(ctx,
		`INSERT INTO auth_audit_events (tenant_id, user_id, event_type, ip, user_agent) VALUES ($1, $2, $3, $4, $5)`,
		tenantID, userID, eventType, nullableString(ip), nullableString(userAgent),
	)
}

func writeAuditEventTx(ctx context.Context, tx *sql.Tx, tenantID uuid.UUID, userID *uuid.UUID, eventType, ip, userAgent string) {
	_, _ = tx.ExecContext(ctx,
		`INSERT INTO auth_audit_events (tenant_id, user_id, event_type, ip, user_agent) VALUES ($1, $2, $3, $4, $5)`,
		tenantID, userID, eventType, nullableString(ip), nullableString(userAgent),
	)
}

func nullableString(s string) sql.NullString {
	if strings.TrimSpace(s) == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "duplicate key") || strings.Contains(msg, "unique constraint")
}
