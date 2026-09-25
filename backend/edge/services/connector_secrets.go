package services

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"zord-edge/db"
	"zord-edge/internal/secretbox"

	"github.com/google/uuid"
)

// Connector secrets at rest (D32).
//
// Every write of a per-tenant secret goes through secretbox (AES-256-GCM,
// "enc:v1:" prefix, key CLEARLINE_SECRETS_KEY). Reads decrypt via
// internal/tenantsecret. Legacy plaintext rows are rejected on read;
// ReEncryptConnectorSecrets is the idempotent helper that seals them.

// SQLExecer is the write surface SetWebhookSecretWith needs (*sql.DB, *sql.Tx
// or a test fake).
type SQLExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// ErrConnectorNotFound means no connector with that id exists for the
// caller's tenant (another tenant's connector looks identical: not found).
var ErrConnectorNotFound = errors.New("connector not found")

// ErrWebhookSecretInvalid means the submitted secret failed validation.
var ErrWebhookSecretInvalid = errors.New("webhook secret must be 1-256 characters with no leading/trailing whitespace")

// MaxWebhookSecretLen bounds a tenant-supplied webhook secret.
const MaxWebhookSecretLen = 256

// ValidateWebhookSecret checks a tenant-supplied webhook secret without
// echoing it in the error.
func ValidateWebhookSecret(secret string) error {
	if secret == "" || len(secret) > MaxWebhookSecretLen || strings.TrimSpace(secret) != secret {
		return ErrWebhookSecretInvalid
	}
	for _, r := range secret {
		if r < 0x20 || r == 0x7f {
			return ErrWebhookSecretInvalid
		}
	}
	return nil
}

// SetWebhookSecret stores a tenant connector's webhook secret, encrypted.
// A missing or invalid CLEARLINE_SECRETS_KEY fails closed: nothing is written.
func (s *ConnectorService) SetWebhookSecret(ctx context.Context, tenantID, connectorID uuid.UUID, plaintext string) (time.Time, error) {
	if db.DB == nil {
		return time.Time{}, errors.New("database handle is nil")
	}
	return SetWebhookSecretWith(ctx, db.DB, tenantID, connectorID, plaintext)
}

// SetWebhookSecretWith validates, seals (secretbox enc:v1:) and writes the
// secret with a tenant-scoped UPDATE (WHERE id AND tenant_id). Sealing happens
// before any SQL, so a key problem writes nothing.
func SetWebhookSecretWith(ctx context.Context, ex SQLExecer, tenantID, connectorID uuid.UUID, plaintext string) (time.Time, error) {
	if err := ValidateWebhookSecret(plaintext); err != nil {
		return time.Time{}, err
	}
	if tenantID == uuid.Nil || connectorID == uuid.Nil {
		return time.Time{}, ErrConnectorNotFound
	}
	sealed, err := secretbox.Encrypt(plaintext)
	if err != nil {
		return time.Time{}, fmt.Errorf("seal webhook secret: %w", err)
	}
	now := time.Now().UTC()
	res, err := ex.ExecContext(ctx, `
		UPDATE connectors
		SET secret = $1, updated_at = $2
		WHERE id = $3 AND tenant_id = $4
	`, sealed, now, connectorID, tenantID)
	if err != nil {
		return time.Time{}, fmt.Errorf("store webhook secret: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return time.Time{}, fmt.Errorf("store webhook secret: %w", err)
	}
	if n == 0 {
		return time.Time{}, ErrConnectorNotFound
	}
	return now, nil
}

// LegacySecretRow is one connector row whose secret column is not enc:v1:.
type LegacySecretRow struct {
	ID     uuid.UUID
	Secret string
}

// connectorSecretStore is the narrow surface ReEncryptConnectorSecrets needs.
type connectorSecretStore interface {
	ListLegacySecrets(ctx context.Context) ([]LegacySecretRow, error)
	// ReplaceSecret swaps old for sealed only if the row still holds old
	// (compare-and-set, so concurrent writers and reruns are safe).
	ReplaceSecret(ctx context.Context, id uuid.UUID, old, sealed string) (bool, error)
}

// ReEncryptConnectorSecrets seals every legacy plaintext connectors.secret.
// Idempotent: rows already enc:v1: are skipped, so a rerun changes nothing.
// Secret values are never logged or returned.
func ReEncryptConnectorSecrets(ctx context.Context, store connectorSecretStore) (int, error) {
	box, err := secretbox.FromEnv()
	if err != nil {
		return 0, err
	}
	rows, err := store.ListLegacySecrets(ctx)
	if err != nil {
		return 0, err
	}
	updated := 0
	for _, r := range rows {
		sealed, changed, err := box.EnsureEncrypted(r.Secret)
		if err != nil {
			return updated, fmt.Errorf("connector %s: %w", r.ID, err)
		}
		if !changed {
			continue
		}
		ok, err := store.ReplaceSecret(ctx, r.ID, r.Secret, sealed)
		if err != nil {
			return updated, fmt.Errorf("connector %s: %w", r.ID, err)
		}
		if ok {
			updated++
		}
	}
	return updated, nil
}

// SQLConnectorSecretStore is the Postgres connectorSecretStore.
type SQLConnectorSecretStore struct{ DB *sql.DB }

func (s SQLConnectorSecretStore) ListLegacySecrets(ctx context.Context) ([]LegacySecretRow, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, secret FROM connectors
		WHERE secret IS NOT NULL AND secret <> '' AND secret NOT LIKE 'enc:v1:%'
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LegacySecretRow
	for rows.Next() {
		var r LegacySecretRow
		if err := rows.Scan(&r.ID, &r.Secret); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s SQLConnectorSecretStore) ReplaceSecret(ctx context.Context, id uuid.UUID, old, sealed string) (bool, error) {
	res, err := s.DB.ExecContext(ctx, `
		UPDATE connectors SET secret = $1, updated_at = now()
		WHERE id = $2 AND secret = $3
	`, sealed, id, old)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}
