package poll

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"zord-outcome-engine/internal/poll/providers/razorpay"
)

const (
	tenantA = "11111111-1111-1111-1111-111111111111"
	connA   = "22222222-2222-2222-2222-222222222222"
)

func setGlobalFakeKeys(t *testing.T) {
	t.Helper()
	t.Setenv("RAZORPAY_KEY_ID", "rzp_test_FAKEGLOBAL")
	t.Setenv("RAZORPAY_KEY_SECRET", "fake_global_secret")
}

type mapTenantCreds map[string][2]string

func (m mapTenantCreds) TenantCredentials(_ context.Context, tenantID, connectorID, _ string) (string, string, error) {
	v, ok := m[tenantID+"|"+connectorID]
	if !ok {
		return "", "", ErrNoTenantSecret
	}
	return v[0], v[1], nil
}

func TestEnvCredentialResolver_TenantWithoutSecretRejectedEvenWithGlobalEnv(t *testing.T) {
	setGlobalFakeKeys(t)
	_, err := EnvCredentialResolver{}.Resolve(context.Background(), tenantA, connA, "test")
	if !errors.Is(err, ErrNoTenantSecret) {
		t.Fatalf("want ErrNoTenantSecret, got %v", err)
	}
	src := mapTenantCreds{}
	if _, err := (EnvCredentialResolver{Tenant: src}).Resolve(context.Background(), tenantA, connA, "test"); !errors.Is(err, ErrNoTenantSecret) {
		t.Fatalf("unknown tenant must fail closed, got %v", err)
	}
	if _, err := (EnvCredentialResolver{}).Resolve(context.Background(), "", connA, "test"); !errors.Is(err, ErrNoTenantSecret) {
		t.Fatalf("connector-only scope is tenant scope, got %v", err)
	}
}

func TestEnvCredentialResolver_TenantWithOwnSecretWorks(t *testing.T) {
	setGlobalFakeKeys(t)
	src := mapTenantCreds{tenantA + "|" + connA: {"rzp_test_FAKETENANTA", "fake_tenant_a_secret"}}
	cfg, err := EnvCredentialResolver{Tenant: src}.Resolve(context.Background(), tenantA, connA, "test")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.KeyID != "rzp_test_FAKETENANTA" || cfg.KeySecret != "fake_tenant_a_secret" {
		t.Fatal("tenant must get its own key pair, never the global one")
	}
}

// Boot path in cmd/main.go: Resolve(ctx, "", "", mode) is platform scope.
func TestEnvCredentialResolver_PlatformScopeBootPath(t *testing.T) {
	setGlobalFakeKeys(t)
	cfg, err := EnvCredentialResolver{}.Resolve(context.Background(), "", "", "test")
	if err != nil || cfg.KeyID != "rzp_test_FAKEGLOBAL" {
		t.Fatalf("platform scope must read process env: err=%v", err)
	}
	t.Setenv("RAZORPAY_KEY_ID", "")
	t.Setenv("RAZORPAY_KEY_SECRET", "")
	if _, err := (EnvCredentialResolver{}).Resolve(context.Background(), "", "", "test"); err == nil {
		t.Fatal("no env keys: boot path must get an error (Transfers stays nil)")
	}
}

func TestBackfill_TenantWithoutSecretFailsClosedNoProviderCall(t *testing.T) {
	setGlobalFakeKeys(t)
	store := NewMemoryStore()
	fresh := NewFreshnessService(store, MemoryWebhookIndex{})
	factoryCalled := false
	svc := NewBackfillService(store, fresh, EnvCredentialResolver{}, func(razorpay.Config) (BackfillProvider, error) {
		factoryCalled = true
		return &fakeProvider{}, nil
	})
	svc.now = func() time.Time { return time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC) }
	from, to := testWindow()
	job, err := svc.CreateJob(context.Background(), CreateBackfillRequest{
		TenantID: tenantA, ConnectorID: connA, Mode: "test",
		ResourceType: ResourcePayments, WindowFrom: from, WindowTo: to,
	})
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	before := BackfillCredentialsMissingTotal("NO_CREDENTIALS")

	summary, err := svc.RunPayments(context.Background(), job.ID)
	if !errors.Is(err, ErrNoTenantSecret) {
		t.Fatalf("want ErrNoTenantSecret, got %v", err)
	}
	if factoryCalled {
		t.Fatal("no provider client may be built with shared keys")
	}
	got, _ := svc.GetJob(context.Background(), job.ID)
	// EM: never a silent success; the reason is visible on the job.
	if got.Status != JobFailed || got.Status == JobSucceeded {
		t.Fatalf("job status must be failed, got %s", got.Status)
	}
	if got.LastErrorCode != "NO_CREDENTIALS" || strings.Contains(got.LastErrorMessage, "fake_global_secret") {
		t.Fatalf("job must fail with NO_CREDENTIALS and no secret: code=%s", got.LastErrorCode)
	}
	raw, _ := json.Marshal(summary)
	if !strings.Contains(string(raw), "NO_CREDENTIALS") {
		t.Fatalf("summary must carry the reason: %s", raw)
	}
	if BackfillCredentialsMissingTotal("NO_CREDENTIALS") != before+1 {
		t.Fatal("backfill_credentials_missing_total{reason=NO_CREDENTIALS} must increment")
	}
	l := logs.String()
	if !strings.Contains(l, "WARN backfill: no usable credentials") || !strings.Contains(l, "tenant_id="+tenantA) || !strings.Contains(l, "reason=NO_CREDENTIALS") {
		t.Fatalf("per-tenant WARN missing: %q", l)
	}
	if strings.Contains(l, "fake_global_secret") {
		t.Fatal("secret in logs")
	}
}
