package handler

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"zord-edge/internal/secretbox"
	"zord-edge/services"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	testReconTok = "recon-cred-token-fake-111"
	testRelayTok = "relay-token-fake-222"
	fakeKeyID    = "rzp_test_FAKEKEYID0001"
	fakeKeySec   = "fake_key_secret_NEVER_LOG_3f9a"
)

type credRow struct {
	tenant uuid.UUID
	mode   string
	active bool
	row    services.ConnectorCredentialRow
	slug   string
}

type internalFixture struct {
	mu     sync.Mutex
	rows   map[uuid.UUID]*credRow
	audits []services.ConnectorAuditEvent
	r      *gin.Engine
}

func (f *internalFixture) lookup(_ context.Context, tenant, conn uuid.UUID, mode string) (services.ConnectorCredentialRow, error) {
	r, ok := f.rows[conn]
	if !ok || r.tenant != tenant || r.mode != mode || !r.active {
		return services.ConnectorCredentialRow{}, services.ErrConnectorNotFound
	}
	return r.row, nil
}

func (f *internalFixture) resolve(_ context.Context, tenant uuid.UUID, provider, slug string) (services.ConnectorResolution, error) {
	for id, r := range f.rows {
		if r.tenant == tenant && r.slug == slug && provider == "razorpay" {
			return services.ConnectorResolution{ID: id, Provider: "razorpay", ConnectorID: r.slug, Mode: r.mode, Active: r.active}, nil
		}
	}
	return services.ConnectorResolution{}, services.ErrConnectorNotFound
}

func (f *internalFixture) last(t *testing.T) services.ConnectorAuditEvent {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.audits) == 0 {
		t.Fatal("expected an audit event")
	}
	return f.audits[len(f.audits)-1]
}

func newInternalFixture(t *testing.T) (*internalFixture, uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	setHandlerSecretsKey(t)
	t.Setenv(EnvReconCredentialsToken, testReconTok)
	t.Setenv(EnvRelayAuthToken, testRelayTok)
	t.Setenv(EnvInternalTLSProxy, "")
	sealed, err := secretbox.Encrypt(fakeKeySec)
	if err != nil {
		t.Fatal(err)
	}
	tenantA, tenantB := uuid.New(), uuid.New()
	connA := uuid.New()
	f := &internalFixture{rows: map[uuid.UUID]*credRow{
		connA: {tenant: tenantA, mode: "test", active: true, slug: "con_razorpay_test_1a2b3c4d",
			row: services.ConnectorCredentialRow{APIKeyRef: fakeKeyID, APISecretRef: sealed, UpdatedAt: time.Unix(1_790_000_000, 0)}},
	}}
	h := &InternalConnectorHandler{LookupCredentials: f.lookup, Resolve: f.resolve,
		Audit: func(_ context.Context, ev services.ConnectorAuditEvent) {
			f.mu.Lock()
			f.audits = append(f.audits, ev)
			f.mu.Unlock()
		}}
	gin.SetMode(gin.TestMode)
	f.r = gin.New()
	RegisterInternalConnectorRoutes(f.r, h)
	return f, tenantA, tenantB, connA
}

type reqOpt func(*http.Request)

func withBearer(tok string) reqOpt { return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok) } }
func withRelayHeader(tok string) reqOpt {
	return func(r *http.Request) { r.Header.Set("X-Relay-Token", tok) }
}
func withTenantHdr(t uuid.UUID) reqOpt {
	return func(r *http.Request) { r.Header.Set(HeaderServiceTenantID, t.String()) }
}

func (f *internalFixture) do(path string, opts ...reqOpt) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for _, o := range opts {
		o(req)
	}
	w := httptest.NewRecorder()
	f.r.ServeHTTP(w, req)
	return w
}

func credPath(tenant, conn uuid.UUID, mode string) string {
	return "/internal/v1/tenants/" + tenant.String() + "/connectors/" + conn.String() + "/razorpay-credentials?mode=" + mode
}

func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func TestInternalCredentials_FoundNoStoreAndAudited(t *testing.T) {
	logs := captureSlog(t)
	f, tenantA, _, connA := newInternalFixture(t)
	w := f.do(credPath(tenantA, connA, "test"), withBearer(testReconTok), withTenantHdr(tenantA))
	if w.Code != 200 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	var got map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got["key_id"] != fakeKeyID || got["key_secret"] != fakeKeySec || got["mode"] != "test" || got["version"] == "" {
		t.Fatalf("resp keys=%v", len(got))
	}
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Pragma") != "no-cache" {
		t.Fatalf("headers=%v", w.Header())
	}
	ev := f.last(t)
	if ev.Action != "credential_access" || ev.Result != "ok" || ev.TenantID != tenantA || ev.ConnectorID != connA.String() || ev.Mode != "test" || ev.Caller != "zord-recon" {
		t.Fatalf("audit=%+v", ev)
	}
	evText := services.ConnectorAuditEventType(ev)
	if strings.Contains(evText, fakeKeySec) || strings.Contains(evText, fakeKeyID) || strings.Contains(logs.String(), fakeKeySec) {
		t.Fatal("audit/logs must never contain the key")
	}
}

func TestInternalCredentials_RelayTokenAndWrongTokenAre401(t *testing.T) {
	f, tenantA, _, connA := newInternalFixture(t)
	for name, opt := range map[string]reqOpt{
		"relay bearer":   withBearer(testRelayTok),
		"relay header":   withRelayHeader(testRelayTok),
		"wrong bearer":   withBearer("nope"),
		"missing header": func(*http.Request) {},
	} {
		w := f.do(credPath(tenantA, connA, "test"), opt, withTenantHdr(tenantA))
		if w.Code != http.StatusUnauthorized || strings.Contains(w.Body.String(), fakeKeySec) {
			t.Fatalf("%s: code=%d", name, w.Code)
		}
		if ev := f.last(t); ev.Result != "unauthorized" {
			t.Fatalf("%s: audit=%+v", name, ev)
		}
	}
}

func TestInternalCredentials_FailsClosedWhenTokenUnsetOrShared(t *testing.T) {
	f, tenantA, _, connA := newInternalFixture(t)
	t.Setenv(EnvReconCredentialsToken, "")
	if w := f.do(credPath(tenantA, connA, "test"), withBearer(""), withTenantHdr(tenantA)); w.Code != 503 {
		t.Fatalf("unset: %d", w.Code)
	}
	t.Setenv(EnvReconCredentialsToken, testRelayTok)
	if w := f.do(credPath(tenantA, connA, "test"), withBearer(testRelayTok), withTenantHdr(tenantA)); w.Code != 503 {
		t.Fatalf("shared with relay: %d", w.Code)
	}
	if _, err := ReconCredentialsToken(); !errors.Is(err, ErrReconCredentialsTokenShared) {
		t.Fatalf("err=%v", err)
	}
}

func TestInternalCredentials_TenantHeaderMismatch403AndOtherTenant404(t *testing.T) {
	f, tenantA, tenantB, connA := newInternalFixture(t)
	if w := f.do(credPath(tenantA, connA, "test"), withBearer(testReconTok), withTenantHdr(tenantB)); w.Code != 403 {
		t.Fatalf("mismatch: %d", w.Code)
	}
	if w := f.do(credPath(tenantA, connA, "test"), withBearer(testReconTok)); w.Code != 403 {
		t.Fatalf("missing header: %d", w.Code)
	}
	if ev := f.last(t); ev.Result != "forbidden" {
		t.Fatalf("audit=%+v", ev)
	}
	// tenant B asking for tenant A's connector (consistent header) → 404.
	w := f.do(credPath(tenantB, connA, "test"), withBearer(testReconTok), withTenantHdr(tenantB))
	if w.Code != 404 || !strings.Contains(w.Body.String(), "NO_TENANT_CREDENTIALS") {
		t.Fatalf("other tenant: %d %s", w.Code, w.Body.String())
	}
	if ev := f.last(t); ev.Result != "not_found" {
		t.Fatalf("audit=%+v", ev)
	}
	// wrong mode for the connector → 404
	if w := f.do(credPath(tenantA, connA, "bogus"), withBearer(testReconTok), withTenantHdr(tenantA)); w.Code != 400 {
		t.Fatalf("bad mode: %d", w.Code)
	}
}

func TestInternalCredentials_EnvRefsLegacyPlaintextInactiveAre404(t *testing.T) {
	f, tenantA, _, connA := newInternalFixture(t)
	good := f.rows[connA].row
	cases := map[string]services.ConnectorCredentialRow{
		"env key ref":      {APIKeyRef: "env:RAZORPAY_KEY_ID", APISecretRef: good.APISecretRef},
		"env secret ref":   {APIKeyRef: fakeKeyID, APISecretRef: "env:RAZORPAY_KEY_SECRET"},
		"legacy plaintext": {APIKeyRef: fakeKeyID, APISecretRef: fakeKeySec},
		"missing":          {},
	}
	for name, row := range cases {
		f.rows[connA].row = row
		w := f.do(credPath(tenantA, connA, "test"), withBearer(testReconTok), withTenantHdr(tenantA))
		if w.Code != 404 || strings.Contains(w.Body.String(), fakeKeySec) {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	f.rows[connA].row = good
	f.rows[connA].active = false
	if w := f.do(credPath(tenantA, connA, "test"), withBearer(testReconTok), withTenantHdr(tenantA)); w.Code != 404 {
		t.Fatalf("inactive: %d", w.Code)
	}
}

func TestInternalCredentials_MissingSecretsKeyIs503(t *testing.T) {
	f, tenantA, _, connA := newInternalFixture(t)
	t.Setenv("CLEARLINE_SECRETS_KEY", "")
	w := f.do(credPath(tenantA, connA, "test"), withBearer(testReconTok), withTenantHdr(tenantA))
	if w.Code != 503 || strings.Contains(w.Body.String(), fakeKeySec) {
		t.Fatalf("code=%d", w.Code)
	}
	if ev := f.last(t); ev.Result != "unavailable" {
		t.Fatalf("audit=%+v", ev)
	}
}

func TestInternalCredentials_LiveRequiresTLS(t *testing.T) {
	f, tenantA, _, connA := newInternalFixture(t)
	f.rows[connA].mode = "live"
	path := credPath(tenantA, connA, "live")
	w := f.do(path, withBearer(testReconTok), withTenantHdr(tenantA))
	if w.Code != 403 || !strings.Contains(w.Body.String(), "LIVE_CREDENTIALS_REQUIRE_TLS") {
		t.Fatalf("plain http live: %d %s", w.Code, w.Body.String())
	}
	// X-Forwarded-Proto alone is not trusted without the proxy flag.
	fwd := func(r *http.Request) { r.Header.Set("X-Forwarded-Proto", "https") }
	if w := f.do(path, withBearer(testReconTok), withTenantHdr(tenantA), fwd); w.Code != 403 {
		t.Fatalf("untrusted forwarded proto: %d", w.Code)
	}
	if w := f.do(path, withBearer(testReconTok), withTenantHdr(tenantA), func(r *http.Request) { r.TLS = &tls.ConnectionState{} }); w.Code != 200 {
		t.Fatalf("direct TLS: %d", w.Code)
	}
	t.Setenv(EnvInternalTLSProxy, "true")
	if w := f.do(path, withBearer(testReconTok), withTenantHdr(tenantA), fwd); w.Code != 200 {
		t.Fatalf("trusted proxy TLS: %d", w.Code)
	}
}

func resolvePath(tenant uuid.UUID, slug string) string {
	return "/internal/v1/tenants/" + tenant.String() + "/connectors/resolve?provider=razorpay&connector_id=" + slug
}

func TestInternalResolve_FoundForRelayAndReconNoSecrets(t *testing.T) {
	f, tenantA, _, connA := newInternalFixture(t)
	slug := f.rows[connA].slug
	for name, opt := range map[string]reqOpt{
		"relay header": withRelayHeader(testRelayTok),
		"relay bearer": withBearer(testRelayTok),
		"recon bearer": withBearer(testReconTok),
	} {
		w := f.do(resolvePath(tenantA, slug), opt, withTenantHdr(tenantA))
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body.String())
		}
		var got map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got["id"] != connA.String() || got["provider"] != "razorpay" || got["connector_id"] != slug || got["mode"] != "test" || got["active"] != true {
			t.Fatalf("%s: %v", name, got)
		}
		if len(got) != 5 {
			t.Fatalf("%s: unexpected fields %v", name, got)
		}
		body := w.Body.String()
		for _, banned := range []string{fakeKeySec, fakeKeyID, "enc:v1:", "secret", "ref", "key_"} {
			if strings.Contains(body, banned) {
				t.Fatalf("%s: response contains %q", name, banned)
			}
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("no-store")
		}
		ev := f.last(t)
		if ev.Action != "connector_resolve" || ev.Result != "ok" || ev.ConnectorID != connA.String() || ev.Caller == "" {
			t.Fatalf("audit=%+v", ev)
		}
	}
}

func TestInternalResolve_OtherTenantInactiveWrongTokenMismatch(t *testing.T) {
	f, tenantA, tenantB, connA := newInternalFixture(t)
	slug := f.rows[connA].slug
	if w := f.do(resolvePath(tenantB, slug), withRelayHeader(testRelayTok), withTenantHdr(tenantB)); w.Code != 404 {
		t.Fatalf("other tenant: %d", w.Code)
	}
	if w := f.do(resolvePath(tenantA, "razorpayx-v1"), withRelayHeader(testRelayTok), withTenantHdr(tenantA)); w.Code != 404 {
		t.Fatalf("unknown slug: %d", w.Code)
	}
	if w := f.do(resolvePath(tenantA, slug), withBearer("wrong"), withTenantHdr(tenantA)); w.Code != 401 {
		t.Fatalf("wrong token: %d", w.Code)
	}
	if ev := f.last(t); ev.Result != "unauthorized" {
		t.Fatalf("audit=%+v", ev)
	}
	if w := f.do(resolvePath(tenantA, slug), withRelayHeader(testRelayTok), withTenantHdr(tenantB)); w.Code != 403 {
		t.Fatalf("mismatch: %d", w.Code)
	}
	f.rows[connA].active = false
	if w := f.do(resolvePath(tenantA, slug), withRelayHeader(testRelayTok), withTenantHdr(tenantA)); w.Code != 404 {
		t.Fatalf("inactive: %d", w.Code)
	}
	if ev := f.last(t); ev.Result != "not_found" {
		t.Fatalf("audit=%+v", ev)
	}
}
