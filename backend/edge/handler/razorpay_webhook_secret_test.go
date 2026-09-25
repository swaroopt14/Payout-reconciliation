package handler

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"testing"

	"zord-edge/internal/secretbox"
	"zord-edge/internal/tenantsecret"
	"zord-edge/services"
	"zord-edge/validator"

	"github.com/google/uuid"
)

func setHandlerSecretsKey(t *testing.T) {
	t.Helper()
	k := make([]byte, 32)
	_, _ = rand.Read(k)
	t.Setenv(secretbox.EnvKey, base64.StdEncoding.EncodeToString(k))
}

// realLookup mimics lookupRazorpayConnectorSQL: per-connector stored column
// (enc:v1:) and ref, resolved by the production tenantsecret resolver.
func realLookup(rows map[uuid.UUID]struct {
	tenant      uuid.UUID
	stored, ref string
}) func(uuid.UUID) (uuid.UUID, string, string, error) {
	return func(id uuid.UUID) (uuid.UUID, string, string, error) {
		r, ok := rows[id]
		if !ok {
			return uuid.Nil, "", "", sql.ErrNoRows
		}
		s, err := tenantsecret.ResolveWebhookSecret(r.stored, r.ref)
		return r.tenant, "test", s, err
	}
}

type row = struct {
	tenant      uuid.UUID
	stored, ref string
}

func TestWebhook_TenantWithEncryptedSecretAccepted(t *testing.T) {
	setHandlerSecretsKey(t)
	t.Setenv("RAZORPAY_WEBHOOK_SECRET", "global_fake_secret")
	enc, _ := secretbox.Encrypt(handlerWebhookSecret)
	conn, tenant := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	store := services.NewMemoryWebhookStore()
	h := &Handler{
		ReceiveRazorpayWebhook:  services.NewRazorpayWebhookServiceWithStore(store).Receive,
		LookupRazorpayConnector: realLookup(map[uuid.UUID]row{conn: {tenant, enc, ""}}),
	}
	body := loadHandlerFixture(t, "payment_captured.json")
	w := postWebhook(h, conn, "evt_enc_ok", validator.SignRazorpayWebhook(body, handlerWebhookSecret), body)
	if w.Code != 200 || store.OutboxCount() != 1 {
		t.Fatalf("code=%d outbox=%d body=%s", w.Code, store.OutboxCount(), w.Body.String())
	}
}

func TestWebhook_TenantWithoutSecretRejectedEvenWithGlobalEnv(t *testing.T) {
	setHandlerSecretsKey(t)
	const global = "global_fake_secret"
	t.Setenv("RAZORPAY_WEBHOOK_SECRET", global)
	t.Setenv("RAZORPAY_KEY_SECRET", global)
	conn, shared := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	store := services.NewMemoryWebhookStore()
	h := &Handler{
		ReceiveRazorpayWebhook: services.NewRazorpayWebhookServiceWithStore(store).Receive,
		LookupRazorpayConnector: realLookup(map[uuid.UUID]row{
			conn:   {uuid.Must(uuid.NewV7()), "", ""},
			shared: {uuid.Must(uuid.NewV7()), "", "env:RAZORPAY_WEBHOOK_SECRET"},
		}),
	}
	body := loadHandlerFixture(t, "payment_captured.json")
	// Signed with the global secret: the old fallback would have accepted it.
	sig := validator.SignRazorpayWebhook(body, global)
	for _, c := range []uuid.UUID{conn, shared} {
		w := postWebhook(h, c, "evt_no_secret", sig, body)
		if w.Code != 401 {
			t.Fatalf("connector without own secret must be 401, got %d", w.Code)
		}
	}
	if store.ReceiptCount() != 0 || store.OutboxCount() != 0 {
		t.Fatal("rejected webhook must not be processed")
	}
	// A hook returning an empty secret with nil error is also rejected.
	h2 := &Handler{
		ReceiveRazorpayWebhook: services.NewRazorpayWebhookServiceWithStore(store).Receive,
		LookupRazorpayConnector: func(uuid.UUID) (uuid.UUID, string, string, error) {
			return uuid.Must(uuid.NewV7()), "test", "", nil
		},
	}
	if w := postWebhook(h2, conn, "evt_empty", sig, body); w.Code != 401 {
		t.Fatalf("empty secret must be 401, got %d", w.Code)
	}
}

func TestWebhook_MissingEncryptionKeyFailsClosed(t *testing.T) {
	setHandlerSecretsKey(t)
	enc, _ := secretbox.Encrypt(handlerWebhookSecret)
	t.Setenv(secretbox.EnvKey, "")
	t.Setenv("RAZORPAY_WEBHOOK_SECRET", handlerWebhookSecret)
	conn := uuid.Must(uuid.NewV7())
	store := services.NewMemoryWebhookStore()
	h := &Handler{
		ReceiveRazorpayWebhook:  services.NewRazorpayWebhookServiceWithStore(store).Receive,
		LookupRazorpayConnector: realLookup(map[uuid.UUID]row{conn: {uuid.Must(uuid.NewV7()), enc, ""}}),
	}
	body := loadHandlerFixture(t, "payment_captured.json")
	w := postWebhook(h, conn, "evt_nokey", validator.SignRazorpayWebhook(body, handlerWebhookSecret), body)
	if w.Code != 503 || store.ReceiptCount() != 0 {
		t.Fatalf("missing key must be 503 and unprocessed, got %d", w.Code)
	}
}

func TestWebhook_LegacyPlaintextSecretRejected(t *testing.T) {
	setHandlerSecretsKey(t)
	conn := uuid.Must(uuid.NewV7())
	store := services.NewMemoryWebhookStore()
	h := &Handler{
		ReceiveRazorpayWebhook:  services.NewRazorpayWebhookServiceWithStore(store).Receive,
		LookupRazorpayConnector: realLookup(map[uuid.UUID]row{conn: {uuid.Must(uuid.NewV7()), handlerWebhookSecret, ""}}),
	}
	body := loadHandlerFixture(t, "payment_captured.json")
	w := postWebhook(h, conn, "evt_legacy", validator.SignRazorpayWebhook(body, handlerWebhookSecret), body)
	if w.Code != 503 || store.ReceiptCount() != 0 {
		t.Fatalf("legacy plaintext must be rejected (503, unprocessed), got %d", w.Code)
	}
}

func TestWebhook_WrongSignatureRejected(t *testing.T) {
	setHandlerSecretsKey(t)
	enc, _ := secretbox.Encrypt(handlerWebhookSecret)
	conn := uuid.Must(uuid.NewV7())
	store := services.NewMemoryWebhookStore()
	h := &Handler{
		ReceiveRazorpayWebhook:  services.NewRazorpayWebhookServiceWithStore(store).Receive,
		LookupRazorpayConnector: realLookup(map[uuid.UUID]row{conn: {uuid.Must(uuid.NewV7()), enc, ""}}),
	}
	body := loadHandlerFixture(t, "payment_captured.json")
	w := postWebhook(h, conn, "evt_badsig", validator.SignRazorpayWebhook(body, "some_other_fake_secret"), body)
	if w.Code != 401 || store.ReceiptCount() != 0 {
		t.Fatalf("wrong signature must be 401 and unprocessed, got %d", w.Code)
	}
}

func TestWebhook_ReplayedEventIDDedupedOtherTenantProcessed(t *testing.T) {
	setHandlerSecretsKey(t)
	encA, _ := secretbox.Encrypt(handlerWebhookSecret)
	encB, _ := secretbox.Encrypt("whsec_fake_tenant_b")
	connA, connB := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	store := services.NewMemoryWebhookStore()
	h := &Handler{
		ReceiveRazorpayWebhook: services.NewRazorpayWebhookServiceWithStore(store).Receive,
		LookupRazorpayConnector: realLookup(map[uuid.UUID]row{
			connA: {uuid.Must(uuid.NewV7()), encA, ""},
			connB: {uuid.Must(uuid.NewV7()), encB, ""},
		}),
	}
	body := loadHandlerFixture(t, "payment_captured.json")
	sigA := validator.SignRazorpayWebhook(body, handlerWebhookSecret)
	status := func(b []byte) string {
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		s, _ := m["status"].(string)
		return s
	}
	first := postWebhook(h, connA, "evt_replay_http", sigA, body)
	replay := postWebhook(h, connA, "evt_replay_http", sigA, body)
	if first.Code != 200 || status(first.Body.Bytes()) != "accepted" {
		t.Fatalf("first: %d %s", first.Code, first.Body.String())
	}
	if replay.Code != 200 || status(replay.Body.Bytes()) != "duplicate" {
		t.Fatalf("replay must be 200 duplicate: %d %s", replay.Code, replay.Body.String())
	}
	if store.OutboxCount() != 1 {
		t.Fatalf("replay processed twice: outbox=%d", store.OutboxCount())
	}
	other := postWebhook(h, connB, "evt_replay_http", validator.SignRazorpayWebhook(body, "whsec_fake_tenant_b"), body)
	if other.Code != 200 || status(other.Body.Bytes()) != "accepted" || store.OutboxCount() != 2 {
		t.Fatalf("other tenant same event id must be processed: %d outbox=%d", other.Code, store.OutboxCount())
	}
}
