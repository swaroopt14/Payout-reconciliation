package services

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"time"

	"zord-edge/db"

	"github.com/google/uuid"
)

// ConnectorAuditEvent is one audited connector-secret action (D52 / condition
// 7). It carries ids and an outcome only: never a secret, ref or ciphertext.
type ConnectorAuditEvent struct {
	Action      string // webhook_secret_set | credential_access | connector_resolve
	TenantID    uuid.UUID
	ConnectorID string // connectors.id UUID, or the slug for resolve misses
	UserID      string // JWT user for tenant-facing actions; "" for services
	Caller      string // service name for internal routes (zord-recon, zord-relay)
	Mode        string
	Result      string // ok | not_found | invalid | unauthorized | forbidden | unavailable | error
}

// ConnectorAuditFunc records a ConnectorAuditEvent. Injectable for tests.
type ConnectorAuditFunc func(ctx context.Context, ev ConnectorAuditEvent)

// AuditConnectorEvent is the production audit sink: a structured log line
// plus a row in auth_audit_events (event_type encodes action/result/ids).
// The insert is best-effort (a failed audit write never blocks the call) and
// skipped when the tenant id is unknown (FK to tenants).
func AuditConnectorEvent(ctx context.Context, ev ConnectorAuditEvent) {
	slog.Info("connector_audit",
		slog.String("action", ev.Action),
		slog.String("tenant_id", ev.TenantID.String()),
		slog.String("connector_id", ev.ConnectorID),
		slog.String("user_id", ev.UserID),
		slog.String("caller", ev.Caller),
		slog.String("mode", ev.Mode),
		slog.String("result", ev.Result))
	if db.DB == nil || ev.TenantID == uuid.Nil {
		return
	}
	var userID *uuid.UUID
	if u, err := uuid.Parse(strings.TrimSpace(ev.UserID)); err == nil {
		userID = &u
	}
	writeAuditEvent(ctx, db.DB, ev.TenantID, userID, ConnectorAuditEventType(ev), "", ev.Caller)
}

// ConnectorAuditEventType renders the auth_audit_events.event_type value, e.g.
// "CONNECTOR_CREDENTIAL_ACCESS:ok:connector=<uuid>:mode=test:caller=zord-recon".
func ConnectorAuditEventType(ev ConnectorAuditEvent) string {
	var b strings.Builder
	b.WriteString("CONNECTOR_" + strings.ToUpper(ev.Action) + ":" + ev.Result)
	if ev.ConnectorID != "" {
		b.WriteString(":connector=" + ev.ConnectorID)
	}
	if ev.Mode != "" {
		b.WriteString(":mode=" + ev.Mode)
	}
	if ev.Caller != "" {
		b.WriteString(":caller=" + ev.Caller)
	}
	return b.String()
}

// ConnectorCredentialRow is the credential material of one connector as
// stored: api_key_ref holds the tenant key id, api_secret_ref the enc:v1:
// sealed key secret (or a legacy env:/plaintext value, which is refused).
type ConnectorCredentialRow struct {
	APIKeyRef    string
	APISecretRef string
	UpdatedAt    time.Time
}

// CredentialLookupFunc loads an active razorpay connector's credential row,
// tenant-scoped. Missing/inactive/other tenant/other mode → ErrConnectorNotFound.
type CredentialLookupFunc func(ctx context.Context, tenantID, connectorID uuid.UUID, mode string) (ConnectorCredentialRow, error)

// ConnectorResolution is the non-secret identity of a connector.
type ConnectorResolution struct {
	ID          uuid.UUID
	Provider    string
	ConnectorID string // slug, e.g. con_razorpay_test_1a2b3c4d
	Mode        string
	Active      bool
}

// ConnectorResolveFunc maps (tenant, provider, slug) to the connector row,
// tenant-scoped. Not found → ErrConnectorNotFound.
type ConnectorResolveFunc func(ctx context.Context, tenantID uuid.UUID, provider, slug string) (ConnectorResolution, error)

// SQLCredentialLookup is the production CredentialLookupFunc.
func SQLCredentialLookup(q *sql.DB) CredentialLookupFunc {
	return func(ctx context.Context, tenantID, connectorID uuid.UUID, mode string) (ConnectorCredentialRow, error) {
		var key, sec sql.NullString
		var row ConnectorCredentialRow
		err := q.QueryRowContext(ctx, `
			SELECT api_key_ref, api_secret_ref, updated_at
			FROM connectors
			WHERE id = $1 AND tenant_id = $2 AND provider = 'razorpay'
			  AND provider_mode = $3 AND active`,
			connectorID, tenantID, mode).Scan(&key, &sec, &row.UpdatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return ConnectorCredentialRow{}, ErrConnectorNotFound
		}
		if err != nil {
			return ConnectorCredentialRow{}, err
		}
		row.APIKeyRef, row.APISecretRef = key.String, sec.String
		return row, nil
	}
}

// SQLConnectorResolve is the production ConnectorResolveFunc. It selects
// identity columns only; secrets and refs are never read.
func SQLConnectorResolve(q *sql.DB) ConnectorResolveFunc {
	return func(ctx context.Context, tenantID uuid.UUID, provider, slug string) (ConnectorResolution, error) {
		var r ConnectorResolution
		var mode sql.NullString
		err := q.QueryRowContext(ctx, `
			SELECT id, provider, connector_id, provider_mode, active
			FROM connectors
			WHERE tenant_id = $1 AND provider = $2 AND connector_id = $3`,
			tenantID, provider, slug).Scan(&r.ID, &r.Provider, &r.ConnectorID, &mode, &r.Active)
		if errors.Is(err, sql.ErrNoRows) {
			return ConnectorResolution{}, ErrConnectorNotFound
		}
		if err != nil {
			return ConnectorResolution{}, err
		}
		r.Mode = mode.String
		return r, nil
	}
}
