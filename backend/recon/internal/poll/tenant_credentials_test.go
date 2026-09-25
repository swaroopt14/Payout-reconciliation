package poll

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"zord-outcome-engine/internal/poll/providers/razorpay"
)

const (
	fakeTenantKeyID  = "rzp_test_FAKETENANTKEY"
	fakeTenantSecret = "fake_tenant_secret_DO_NOT_LEAK_7f3a"
	testReconToken   = "fake-recon-credentials-token"
)

func credServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	return s
}

func okCreds(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"key_id": fakeTenantKeyID, "key_secret": fakeTenantSecret, "mode": "test"})
}

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var b bytes.Buffer
	log.SetOutput(&b)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return &b
}

func TestEdgeCredentialClient_FetchesTenantCredsWithServiceHeaders(t *testing.T) {
	var gotAuth, gotTenant, gotPath, gotMode string
	s := credServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotTenant, gotPath, gotMode = r.Header.Get("Authorization"), r.Header.Get("X-Service-Tenant-ID"), r.URL.Path, r.URL.Query().Get("mode")
		okCreds(w)
	})
	c := &EdgeCredentialClient{BaseURL: s.URL, Token: testReconToken, HTTPClient: s.Client()}
	id, sec, err := c.TenantCredentials(context.Background(), tenantA, connA, "test")
	if err != nil || id != fakeTenantKeyID || sec != fakeTenantSecret {
		t.Fatalf("err=%v", err)
	}
	if gotAuth != "Bearer "+testReconToken || gotTenant != tenantA || gotMode != "test" ||
		gotPath != "/internal/v1/tenants/"+tenantA+"/connectors/"+connA+"/razorpay-credentials" {
		t.Fatalf("request shape: auth_ok=%v tenant=%s path=%s mode=%s", gotAuth == "Bearer "+testReconToken, gotTenant, gotPath, gotMode)
	}
}

func TestEdgeCredentialClient_NotFoundIsNoTenantSecret(t *testing.T) {
	s := credServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"error":{"code":"NO_TENANT_CREDENTIALS"}}`))
	})
	c := &EdgeCredentialClient{BaseURL: s.URL, Token: testReconToken}
	if _, _, err := c.TenantCredentials(context.Background(), tenantA, connA, "test"); !errors.Is(err, ErrNoTenantSecret) {
		t.Fatalf("want ErrNoTenantSecret, got %v", err)
	}
}

// D52 condition 5: live mode never goes over plain http://; test mode may.
func TestEdgeCredentialClient_LiveRequiresHTTPS(t *testing.T) {
	var calls atomic.Int32
	plain := credServer(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); okCreds(w) })
	c := &EdgeCredentialClient{BaseURL: plain.URL, Token: testReconToken}
	if _, _, err := c.TenantCredentials(context.Background(), tenantA, connA, "live"); !errors.Is(err, ErrLiveCredentialsNeedTLS) {
		t.Fatalf("live over http must be refused, got %v", err)
	}
	if calls.Load() != 0 {
		t.Fatal("no request may be sent for live over http")
	}
	if _, _, err := c.TenantCredentials(context.Background(), tenantA, connA, "test"); err != nil {
		t.Fatalf("test mode over http is allowed: %v", err)
	}
	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("mode") != "live" {
			w.WriteHeader(400)
			return
		}
		okCreds(w)
	}))
	defer tlsSrv.Close()
	ct := &EdgeCredentialClient{BaseURL: tlsSrv.URL, Token: testReconToken, HTTPClient: tlsSrv.Client()}
	if _, sec, err := ct.TenantCredentials(context.Background(), tenantA, connA, "live"); err != nil || sec != fakeTenantSecret {
		t.Fatalf("live over https must work: %v", err)
	}
}

// D52 condition 1: forced error paths never surface the secret in errors or logs.
func TestCredentialErrorPaths_NeverLeakSecret(t *testing.T) {
	logs := captureLogs(t)
	var errs []error

	// Edge 5xx whose body contains the secret.
	s5 := credServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte("boom key_secret=" + fakeTenantSecret))
	})
	_, _, err := (&EdgeCredentialClient{BaseURL: s5.URL, Token: testReconToken}).TenantCredentials(context.Background(), tenantA, connA, "test")
	if !errors.Is(err, ErrCredentialSourceUnavailable) {
		t.Fatalf("5xx: %v", err)
	}
	errs = append(errs, err)

	// Decode error on a body that contains the secret.
	sd := credServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"key_id":"x","key_secret":"` + fakeTenantSecret + `"` + "\x00garbage"))
	})
	_, _, err = (&EdgeCredentialClient{BaseURL: sd.URL, Token: testReconToken}).TenantCredentials(context.Background(), tenantA, connA, "test")
	if !errors.Is(err, ErrCredentialSourceUnavailable) {
		t.Fatalf("decode: %v", err)
	}
	errs = append(errs, err)

	// Timeout.
	st := credServer(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		okCreds(w)
	})
	_, _, err = (&EdgeCredentialClient{BaseURL: st.URL, Token: testReconToken, HTTPClient: &http.Client{Timeout: 30 * time.Millisecond}}).TenantCredentials(context.Background(), tenantA, connA, "test")
	if !errors.Is(err, ErrCredentialSourceUnavailable) || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("timeout: %v", err)
	}
	errs = append(errs, err)

	// Razorpay 401 during a backfill run with the tenant's key; the provider
	// echoes the key secret in its error body.
	rzp := credServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":{"code":"BAD_REQUEST_ERROR","description":"bad key ` + fakeTenantSecret + ` ` + fakeTenantKeyID + `"}}`))
	})
	edge := credServer(t, func(w http.ResponseWriter, r *http.Request) { okCreds(w) })
	cache := &CachedCredentialSource{Src: &EdgeCredentialClient{BaseURL: edge.URL, Token: testReconToken}}
	store := NewMemoryStore()
	svc := NewBackfillService(store, NewFreshnessService(store, MemoryWebhookIndex{}), EnvCredentialResolver{Tenant: cache}, func(cfg razorpay.Config) (BackfillProvider, error) {
		cfg.BaseURL = rzp.URL
		cfg.MaxRetries = 0
		c, err := razorpay.NewClient(cfg, nil, nil, nil)
		if err != nil {
			return nil, err
		}
		return razorpay.NewBackfillAdapter(c), nil
	})
	svc.now = func() time.Time { return time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC) }
	from, to := testWindow()
	job, err := svc.CreateJob(context.Background(), CreateBackfillRequest{TenantID: tenantA, ConnectorID: connA, Mode: "test", ResourceType: ResourcePayments, WindowFrom: from, WindowTo: to})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := svc.RunPayments(context.Background(), job.ID)
	if err == nil {
		t.Fatal("expected razorpay 401 failure")
	}
	errs = append(errs, err)
	got, _ := svc.GetJob(context.Background(), job.ID)
	raw, _ := json.Marshal(summary)
	rawJob, _ := json.Marshal(got)

	for i, e := range errs {
		if strings.Contains(e.Error(), fakeTenantSecret) || strings.Contains(e.Error(), fakeTenantKeyID) {
			t.Fatalf("error %d leaks key material: %q", i, e.Error())
		}
	}
	for name, v := range map[string]string{"logs": logs.String(), "summary": string(raw), "job": string(rawJob)} {
		if strings.Contains(v, fakeTenantSecret) {
			t.Fatalf("%s leaks the secret", name)
		}
	}
	// The 401 dropped the cached pair.
	cache.mu.Lock()
	n := len(cache.entries)
	cache.mu.Unlock()
	if n != 0 {
		t.Fatal("razorpay 401 must invalidate the cached credentials")
	}
}

type countingSource struct {
	calls atomic.Int32
	err   error
}

func (c *countingSource) TenantCredentials(_ context.Context, tenantID, _, _ string) (string, string, error) {
	c.calls.Add(1)
	if c.err != nil {
		return "", "", c.err
	}
	return "id_" + tenantID, "sec_" + tenantID, nil
}

func TestCachedCredentialSource_TTLNegativeInvalidateIsolation(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	src := &countingSource{}
	c := &CachedCredentialSource{Src: src, Now: func() time.Time { return now }}
	ctx := context.Background()
	_, s1, _ := c.TenantCredentials(ctx, "tA", "c1", "test")
	_, s2, _ := c.TenantCredentials(ctx, "tA", "c1", "test")
	if src.calls.Load() != 1 || s1 != s2 {
		t.Fatalf("second call within TTL must hit cache, calls=%d", src.calls.Load())
	}
	_, sb, _ := c.TenantCredentials(ctx, "tB", "c1", "test")
	if sb == s1 || src.calls.Load() != 2 {
		t.Fatal("tenants must be cached separately")
	}
	c.Invalidate("tA", "c1", "test")
	_, _, _ = c.TenantCredentials(ctx, "tA", "c1", "test")
	if src.calls.Load() != 3 {
		t.Fatal("Invalidate must force a refetch")
	}
	now = now.Add(6 * time.Minute)
	_, _, _ = c.TenantCredentials(ctx, "tA", "c1", "test")
	if src.calls.Load() != 4 {
		t.Fatal("expired entry must refetch")
	}

	neg := &countingSource{err: ErrNoTenantSecret}
	cn := &CachedCredentialSource{Src: neg, Now: func() time.Time { return now }}
	_, _, e1 := cn.TenantCredentials(ctx, "tA", "c1", "test")
	_, _, _ = cn.TenantCredentials(ctx, "tA", "c1", "test")
	if !errors.Is(e1, ErrNoTenantSecret) || neg.calls.Load() != 1 {
		t.Fatal("no-creds must be negatively cached")
	}
	now = now.Add(61 * time.Second)
	_, _, _ = cn.TenantCredentials(ctx, "tA", "c1", "test")
	if neg.calls.Load() != 2 {
		t.Fatal("negative entry expires after 60s")
	}

	down := &countingSource{err: fmt.Errorf("%w: timeout", ErrCredentialSourceUnavailable)}
	cd := &CachedCredentialSource{Src: down}
	_, _, _ = cd.TenantCredentials(ctx, "tA", "c1", "test")
	_, _, _ = cd.TenantCredentials(ctx, "tA", "c1", "test")
	if down.calls.Load() != 2 {
		t.Fatal("unavailable errors must never be cached")
	}
}

// D52 condition 3: the recon token must be set and differ from the relay token.
func TestCredentialsToken_FailsClosedWhenEmptyOrEqualToRelay(t *testing.T) {
	if ValidateCredentialsToken("", "relay") == nil {
		t.Fatal("empty token must fail")
	}
	if ValidateCredentialsToken("same", "same") == nil {
		t.Fatal("recon token equal to relay token must fail")
	}
	if err := ValidateCredentialsToken("recon-x", "relay-y"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ZORD_EDGE_URL", "http://edge.local")
	t.Setenv(EnvRelayAuthToken, "shared-token")
	t.Setenv(EnvReconCredentialsToken, "shared-token")
	if _, err := NewEdgeCredentialClientFromEnv(); err == nil {
		t.Fatal("startup must fail closed when tokens are equal")
	}
	t.Setenv(EnvReconCredentialsToken, "")
	if _, err := NewEdgeCredentialClientFromEnv(); err == nil {
		t.Fatal("startup must fail closed when the token is empty")
	}
	// Misconfigured source: every tenant lookup is unavailable, never platform keys.
	setGlobalFakeKeys(t)
	_, err := EnvCredentialResolver{Tenant: NewUnavailableCredentialSource("x")}.Resolve(context.Background(), tenantA, connA, "test")
	if !errors.Is(err, ErrCredentialSourceUnavailable) {
		t.Fatalf("want unavailable, got %v", err)
	}
	if CredentialFailureCode(err) != "CREDENTIALS_UNAVAILABLE" {
		t.Fatal("reason code")
	}
}
