package services

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"zord-edge/internal/secretbox"
	"zord-edge/internal/tenantsecret"

	"github.com/google/uuid"
)

func setSecretsKey(t *testing.T) {
	t.Helper()
	k := make([]byte, 32)
	_, _ = rand.Read(k)
	t.Setenv(secretbox.EnvKey, base64.StdEncoding.EncodeToString(k))
}

type captureExec struct {
	query string
	args  []any
	rows  int64
}

type fakeResult struct{ n int64 }

func (r fakeResult) LastInsertId() (int64, error) { return 0, nil }
func (r fakeResult) RowsAffected() (int64, error) { return r.n, nil }

func (c *captureExec) ExecContext(_ context.Context, q string, args ...any) (sql.Result, error) {
	c.query, c.args = q, args
	return fakeResult{n: c.rows}, nil
}

func TestSetWebhookSecret_StoredValueIsEncryptedNotPlaintext(t *testing.T) {
	setSecretsKey(t)
	const plain = "whsec_fake_tenant_a"
	ex := &captureExec{rows: 1}
	tenant, conn := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	if _, err := SetWebhookSecretWith(context.Background(), ex, tenant, conn, plain); err != nil {
		t.Fatal(err)
	}
	stored, _ := ex.args[0].(string)
	if !strings.HasPrefix(stored, secretbox.Prefix) || strings.Contains(stored, plain) {
		t.Fatal("connectors.secret must be written as enc:v1: ciphertext, never plaintext")
	}
	if ex.args[2] != conn || ex.args[3] != tenant {
		t.Fatal("write must be scoped by connector id and tenant id")
	}
	got, err := tenantsecret.ResolveWebhookSecret(stored, "")
	if err != nil || got != plain {
		t.Fatalf("stored value must decrypt back on read: %v", err)
	}
}

func TestSetWebhookSecret_MissingKeyFailsClosedWritesNothing(t *testing.T) {
	t.Setenv(secretbox.EnvKey, "")
	ex := &captureExec{rows: 1}
	_, err := SetWebhookSecretWith(context.Background(), ex, uuid.New(), uuid.New(), "whsec_fake")
	if !errors.Is(err, secretbox.ErrMissingKey) {
		t.Fatalf("want ErrMissingKey, got %v", err)
	}
	if ex.query != "" {
		t.Fatal("nothing may be written without a key")
	}
}

type memSecretStore struct{ rows map[uuid.UUID]string }

func (m *memSecretStore) ListLegacySecrets(context.Context) ([]LegacySecretRow, error) {
	var out []LegacySecretRow
	for id, s := range m.rows {
		if s != "" && !secretbox.IsEncrypted(s) {
			out = append(out, LegacySecretRow{ID: id, Secret: s})
		}
	}
	return out, nil
}

func (m *memSecretStore) ReplaceSecret(_ context.Context, id uuid.UUID, old, sealed string) (bool, error) {
	if m.rows[id] != old {
		return false, nil
	}
	m.rows[id] = sealed
	return true, nil
}

func TestReEncryptConnectorSecrets_LegacyRowsSealedIdempotent(t *testing.T) {
	setSecretsKey(t)
	legacyID, encID := uuid.New(), uuid.New()
	already, _ := secretbox.Encrypt("whsec_fake_b")
	st := &memSecretStore{rows: map[uuid.UUID]string{legacyID: "whsec_fake_legacy", encID: already}}

	// Before: the legacy row is rejected on read.
	if _, err := tenantsecret.ResolveWebhookSecret(st.rows[legacyID], ""); !errors.Is(err, secretbox.ErrLegacyPlaintext) {
		t.Fatalf("legacy plaintext must be rejected, got %v", err)
	}
	n, err := ReEncryptConnectorSecrets(context.Background(), st)
	if err != nil || n != 1 {
		t.Fatalf("want 1 row re-encrypted, n=%d err=%v", n, err)
	}
	if !secretbox.IsEncrypted(st.rows[legacyID]) || strings.Contains(st.rows[legacyID], "whsec_fake_legacy") {
		t.Fatal("legacy row must now be ciphertext")
	}
	if st.rows[encID] != already {
		t.Fatal("already-encrypted row must be untouched")
	}
	n2, err := ReEncryptConnectorSecrets(context.Background(), st)
	if err != nil || n2 != 0 {
		t.Fatalf("rerun must be a no-op, n=%d err=%v", n2, err)
	}
	got, err := tenantsecret.ResolveWebhookSecret(st.rows[legacyID], "")
	if err != nil || got != "whsec_fake_legacy" {
		t.Fatalf("re-encrypted row must resolve: %v", err)
	}
}

func TestReEncryptConnectorSecrets_MissingKeyFailsClosed(t *testing.T) {
	t.Setenv(secretbox.EnvKey, "")
	st := &memSecretStore{rows: map[uuid.UUID]string{uuid.New(): "whsec_fake_legacy"}}
	if _, err := ReEncryptConnectorSecrets(context.Background(), st); !errors.Is(err, secretbox.ErrMissingKey) {
		t.Fatalf("want ErrMissingKey, got %v", err)
	}
}

// D31: a replayed event id (same tenant + connector) is acknowledged but not
// processed twice; the same event id for another tenant is processed.
func TestReceive_ReplayedEventIDNotProcessedTwice_OtherTenantProcessed(t *testing.T) {
	store := NewMemoryWebhookStore()
	body := loadRazorpayFixture(t, "payment_captured.json")
	conn := uuid.Must(uuid.NewV7())
	tenantA := uuid.Must(uuid.NewV7())

	req, svc := signedRequest(t, store, body, "evt_replay_1", conn, tenantA)
	first, code, err := svc.Receive(context.Background(), req)
	if err != nil || code != 200 || !first.Published {
		t.Fatalf("first delivery must be processed: code=%d err=%v", code, err)
	}
	second, code, err := svc.Receive(context.Background(), req)
	if err != nil || code != 200 {
		t.Fatalf("replay must be acknowledged 2xx: code=%d err=%v", code, err)
	}
	if !second.Duplicate || second.Published {
		t.Fatalf("replay must be deduped, got %+v", second)
	}
	if store.OutboxCount() != 1 {
		t.Fatalf("replay must not be re-published, outbox=%d", store.OutboxCount())
	}

	// Different tenant (own connector), same provider event id: processed.
	reqB, _ := signedRequest(t, store, body, "evt_replay_1", uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()))
	if res, _, err := svc.Receive(context.Background(), reqB); err != nil || !res.Published {
		t.Fatalf("other tenant must be processed: %+v err=%v", res, err)
	}
	// Key mirrors UNIQUE(tenant_id, connector_id, event_id): even an equal
	// connector UUID under another tenant is a separate delivery.
	reqC, _ := signedRequest(t, store, body, "evt_replay_1", conn, uuid.Must(uuid.NewV7()))
	if res, _, err := svc.Receive(context.Background(), reqC); err != nil || !res.Published {
		t.Fatalf("tenant-scoped key: %+v err=%v", res, err)
	}
	if store.OutboxCount() != 3 {
		t.Fatalf("outbox=%d want 3", store.OutboxCount())
	}
}

func TestSetWebhookSecret_OtherTenantNotFoundAndValidation(t *testing.T) {
	setSecretsKey(t)
	ex := &captureExec{rows: 0} // tenant-scoped UPDATE matched nothing
	if _, err := SetWebhookSecretWith(context.Background(), ex, uuid.New(), uuid.New(), "whsec_fake"); !errors.Is(err, ErrConnectorNotFound) {
		t.Fatalf("want ErrConnectorNotFound, got %v", err)
	}
	for _, bad := range []string{"", " padded", "x\n", strings.Repeat("a", MaxWebhookSecretLen+1)} {
		ex2 := &captureExec{rows: 1}
		if _, err := SetWebhookSecretWith(context.Background(), ex2, uuid.New(), uuid.New(), bad); !errors.Is(err, ErrWebhookSecretInvalid) {
			t.Fatalf("want ErrWebhookSecretInvalid, got %v", err)
		}
		if ex2.query != "" {
			t.Fatal("invalid secret must write nothing")
		}
	}
}
