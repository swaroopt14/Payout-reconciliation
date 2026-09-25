package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"zord-edge/internal/secretbox"
	"zord-edge/internal/tenantsecret"
	"zord-edge/middleware"
	"zord-edge/services"
	"zord-edge/validator"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// memConnectors is a fake connectors table that implements services.SQLExecer
// for the real tenant-scoped UPDATE in services.SetWebhookSecretWith.
type memConnectors struct {
	mu     sync.Mutex
	rows   map[uuid.UUID]*memConnRow
	writes int
}

type memConnRow struct {
	tenant uuid.UUID
	secret string
}

type rowsResult int64

func (r rowsResult) LastInsertId() (int64, error) { return 0, nil }
func (r rowsResult) RowsAffected() (int64, error) { return int64(r), nil }

func (m *memConnectors) ExecContext(_ context.Context, _ string, args ...any) (sql.Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sealed, conn, tenant := args[0].(string), args[2].(uuid.UUID), args[3].(uuid.UUID)
	r, ok := m.rows[conn]
	if !ok || r.tenant != tenant {
		return rowsResult(0), nil
	}
	r.secret = sealed
	m.writes++
	return rowsResult(1), nil
}

func (m *memConnectors) set(ctx context.Context, tenantID, connectorID uuid.UUID, secret string) (time.Time, error) {
	return services.SetWebhookSecretWith(ctx, m, tenantID, connectorID, secret)
}

// Webhook lookup over the same fake table, via the production resolver.
func (m *memConnectors) lookup(id uuid.UUID) (uuid.UUID, string, string, error) {
	m.mu.Lock()
	r, ok := m.rows[id]
	m.mu.Unlock()
	if !ok {
		return uuid.Nil, "", "", sql.ErrNoRows
	}
	s, err := tenantsecret.ResolveWebhookSecret(r.secret, "")
	return r.tenant, "test", s, err
}

type authAs struct {
	tenant uuid.UUID
	role   string
}

// fakeAuth stamps the same context keys Authenticate() sets for a JWT user.
func fakeAuth(a *authAs) gin.HandlerFunc {
	return func(c *gin.Context) {
		if a != nil {
			c.Set("tenant_id", a.tenant.String())
			c.Set("user_id", uuid.NewString())
			c.Set("role", a.role)
		}
		c.Next()
	}
}

func secretRouter(store *memConnectors, auth gin.HandlerFunc) *gin.Engine {
	r := gin.New()
	g := r.Group("/v1")
	g.Use(auth)
	RegisterConnectorRoutes(g, NewConnectorHandlerWithSecretWriter(store.set))
	return r
}

func putSecret(r *gin.Engine, conn string, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPut, "/v1/connectors/"+conn+"/webhook-secret", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

const newSecret = "whsec_fake_rotated_by_admin"

func fixture(t *testing.T) (*memConnectors, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	setHandlerSecretsKey(t)
	tenantA, tenantB := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	connA, connB := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	return &memConnectors{rows: map[uuid.UUID]*memConnRow{
		connA: {tenant: tenantA},
		connB: {tenant: tenantB},
	}}, tenantA, tenantB, connA, connB
}

func TestSetWebhookSecret_AdminStoresEncryptedNoEcho(t *testing.T) {
	store, tenantA, _, connA, _ := fixture(t)
	r := secretRouter(store, fakeAuth(&authAs{tenantA, services.RoleConnectorAdmin}))
	w := putSecret(r, connA.String(), `{"webhook_secret":"`+newSecret+`"}`)
	if w.Code != 200 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), newSecret) || strings.Contains(w.Body.String(), "enc:v1:") {
		t.Fatal("response must not echo the secret or ciphertext")
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["connector_id"] != connA.String() || resp["webhook_secret_set"] != true || resp["updated_at"] == nil {
		t.Fatalf("resp=%v", resp)
	}
	stored := store.rows[connA].secret
	if !secretbox.IsEncrypted(stored) || strings.Contains(stored, newSecret) {
		t.Fatal("stored value must be enc:v1: ciphertext")
	}
	if got, err := secretbox.Decrypt(stored); err != nil || got != newSecret {
		t.Fatalf("stored value must decrypt: %v", err)
	}
}

func TestSetWebhookSecret_NonAdminForbidden(t *testing.T) {
	store, tenantA, _, connA, _ := fixture(t)
	// CUSTOMER_ADMIN is every signup's role: it must not set connector secrets.
	for _, role := range []string{services.RoleCustomerAdmin, services.RolePayoutApprover, ""} { // "" = tenant API key (no role)
		r := secretRouter(store, fakeAuth(&authAs{tenantA, role}))
		if w := putSecret(r, connA.String(), `{"webhook_secret":"`+newSecret+`"}`); w.Code != 403 {
			t.Fatalf("role %q: code=%d", role, w.Code)
		}
	}
	if store.writes != 0 {
		t.Fatal("forbidden request must write nothing")
	}
}

func TestSetWebhookSecret_UnauthenticatedUnauthorized(t *testing.T) {
	store, _, _, connA, _ := fixture(t)
	// Real Authenticate(): no bearer header is rejected before any DB access.
	r := secretRouter(store, middleware.Authenticate())
	if w := putSecret(r, connA.String(), `{"webhook_secret":"`+newSecret+`"}`); w.Code != 401 {
		t.Fatalf("code=%d", w.Code)
	}
	// RequireRole alone with no tenant context is also 401.
	r2 := secretRouter(store, fakeAuth(nil))
	if w := putSecret(r2, connA.String(), `{"webhook_secret":"`+newSecret+`"}`); w.Code != 401 {
		t.Fatalf("code=%d", w.Code)
	}
	if store.writes != 0 {
		t.Fatal("unauthenticated request must write nothing")
	}
}

func TestSetWebhookSecret_OtherTenantsConnectorNotFound(t *testing.T) {
	store, tenantA, _, _, connB := fixture(t)
	r := secretRouter(store, fakeAuth(&authAs{tenantA, services.RoleConnectorAdmin}))
	w := putSecret(r, connB.String(), `{"webhook_secret":"`+newSecret+`"}`)
	if w.Code != 404 {
		t.Fatalf("code=%d", w.Code)
	}
	if store.writes != 0 || store.rows[connB].secret != "" {
		t.Fatal("other tenant's connector must not be written")
	}
	if w := putSecret(r, "not-a-uuid", `{"webhook_secret":"`+newSecret+`"}`); w.Code != 404 {
		t.Fatalf("malformed id: code=%d", w.Code)
	}
}

func TestSetWebhookSecret_InvalidSecretBadRequest(t *testing.T) {
	store, tenantA, _, connA, _ := fixture(t)
	r := secretRouter(store, fakeAuth(&authAs{tenantA, services.RoleConnectorAdmin}))
	for _, body := range []string{
		`{"webhook_secret":""}`,
		`{}`,
		`{"webhook_secret":"  "}`,
		`{"webhook_secret":"` + strings.Repeat("a", services.MaxWebhookSecretLen+1) + `"}`,
		`not json`,
	} {
		w := putSecret(r, connA.String(), body)
		if w.Code != 400 {
			t.Fatalf("body %.20q: code=%d", body, w.Code)
		}
	}
	if store.writes != 0 {
		t.Fatal("invalid secret must write nothing")
	}
}

func TestSetWebhookSecret_MissingKeyServiceUnavailable(t *testing.T) {
	store, tenantA, _, connA, _ := fixture(t)
	t.Setenv(secretbox.EnvKey, "")
	r := secretRouter(store, fakeAuth(&authAs{tenantA, services.RoleConnectorAdmin}))
	w := putSecret(r, connA.String(), `{"webhook_secret":"`+newSecret+`"}`)
	if w.Code != 503 {
		t.Fatalf("code=%d", w.Code)
	}
	if store.writes != 0 || store.rows[connA].secret != "" {
		t.Fatal("missing key must write nothing")
	}
	if strings.Contains(w.Body.String(), newSecret) {
		t.Fatal("secret echoed")
	}
}

// End to end: admin sets the secret, then a Razorpay webhook signed with it
// is accepted by the real webhook handler; the old/global secret is not.
func TestSetWebhookSecret_ThenSignedWebhookAccepted(t *testing.T) {
	store, tenantA, _, connA, _ := fixture(t)
	t.Setenv("RAZORPAY_WEBHOOK_SECRET", "global_fake_secret")
	whStore := services.NewMemoryWebhookStore()
	wh := &Handler{
		ReceiveRazorpayWebhook:  services.NewRazorpayWebhookServiceWithStore(whStore).Receive,
		LookupRazorpayConnector: store.lookup,
	}
	body := loadHandlerFixture(t, "payment_captured.json")

	// Before: connector has no secret -> 401.
	if w := postWebhook(wh, connA, "evt_e2e_0", validator.SignRazorpayWebhook(body, "global_fake_secret"), body); w.Code != 401 {
		t.Fatalf("before set: code=%d", w.Code)
	}
	r := secretRouter(store, fakeAuth(&authAs{tenantA, services.RoleConnectorAdmin}))
	if w := putSecret(r, connA.String(), `{"webhook_secret":"`+newSecret+`"}`); w.Code != 200 {
		t.Fatalf("set: code=%d", w.Code)
	}
	if w := postWebhook(wh, connA, "evt_e2e_1", validator.SignRazorpayWebhook(body, newSecret), body); w.Code != 200 {
		t.Fatalf("signed with new secret: code=%d body=%s", w.Code, w.Body.String())
	}
	if w := postWebhook(wh, connA, "evt_e2e_2", validator.SignRazorpayWebhook(body, "global_fake_secret"), body); w.Code != 401 {
		t.Fatalf("global secret must not verify: code=%d", w.Code)
	}
	if whStore.OutboxCount() != 1 {
		t.Fatalf("outbox=%d", whStore.OutboxCount())
	}
}

func TestSetWebhookSecret_PlatformAdminAllowed(t *testing.T) {
	store, tenantA, _, connA, _ := fixture(t)
	r := secretRouter(store, fakeAuth(&authAs{tenantA, services.RolePlatformAdmin}))
	if w := putSecret(r, connA.String(), `{"webhook_secret":"`+newSecret+`"}`); w.Code != 200 {
		t.Fatalf("code=%d", w.Code)
	}
}

// Condition 7: every set attempt is audited with tenant, connector, user and
// result — never the secret.
func TestSetWebhookSecret_AuditedWithoutSecret(t *testing.T) {
	store, tenantA, _, connA, connB := fixture(t)
	var got []services.ConnectorAuditEvent
	h := NewConnectorHandlerWithSecretWriter(store.set).WithAudit(func(_ context.Context, ev services.ConnectorAuditEvent) {
		got = append(got, ev)
	})
	r := gin.New()
	g := r.Group("/v1")
	g.Use(fakeAuth(&authAs{tenantA, services.RoleConnectorAdmin}))
	RegisterConnectorRoutes(g, h)

	putSecret(r, connA.String(), `{"webhook_secret":"`+newSecret+`"}`)
	putSecret(r, connB.String(), `{"webhook_secret":"`+newSecret+`"}`)
	putSecret(r, connA.String(), `{"webhook_secret":""}`)
	t.Setenv(secretbox.EnvKey, "")
	putSecret(r, connA.String(), `{"webhook_secret":"`+newSecret+`"}`)

	want := []string{"ok", "not_found", "invalid", "unavailable"}
	if len(got) != len(want) {
		t.Fatalf("audits=%+v", got)
	}
	for i, ev := range got {
		if ev.Result != want[i] || ev.Action != "webhook_secret_set" || ev.TenantID != tenantA || ev.UserID == "" {
			t.Fatalf("audit %d = %+v", i, ev)
		}
		if blob := services.ConnectorAuditEventType(ev) + ev.UserID + ev.ConnectorID; strings.Contains(blob, newSecret) || strings.Contains(blob, "enc:v1:") {
			t.Fatal("audit must not carry the secret")
		}
	}
	if got[0].ConnectorID != connA.String() || got[1].ConnectorID != connB.String() {
		t.Fatalf("connector ids: %+v", got)
	}
}
