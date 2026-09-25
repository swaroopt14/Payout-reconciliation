package tenantsecret

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"testing"

	"zord-edge/internal/secretbox"
)

func setKey(t *testing.T) {
	t.Helper()
	k := make([]byte, 32)
	_, _ = rand.Read(k)
	t.Setenv(secretbox.EnvKey, base64.StdEncoding.EncodeToString(k))
}

func TestResolveWebhookSecret_TenantWithEncryptedSecretWorks(t *testing.T) {
	setKey(t)
	t.Setenv("RAZORPAY_WEBHOOK_SECRET", "global_fake_secret")
	enc, err := secretbox.Encrypt("tenant_fake_secret")
	if err != nil {
		t.Fatal(err)
	}
	got, err := ResolveWebhookSecret(enc, "")
	if err != nil || got != "tenant_fake_secret" {
		t.Fatalf("want tenant secret, err=%v", err)
	}
}

func TestResolveWebhookSecret_NoTenantSecretRejectedEvenWithGlobalEnv(t *testing.T) {
	setKey(t)
	t.Setenv("RAZORPAY_WEBHOOK_SECRET", "global_fake_secret")
	t.Setenv("RAZORPAY_KEY_SECRET", "global_fake_key_secret")
	if _, err := ResolveWebhookSecret("", ""); !errors.Is(err, ErrNoTenantSecret) {
		t.Fatalf("want ErrNoTenantSecret, got %v", err)
	}
	if _, err := ResolveWebhookSecret("", "env:RAZORPAY_WEBHOOK_SECRET"); !errors.Is(err, ErrSharedSecretRef) {
		t.Fatalf("shared ref must be refused, got %v", err)
	}
	if _, err := ResolveWebhookSecret("", "env:TENANT_A_UNSET_SECRET"); !errors.Is(err, ErrNoTenantSecret) {
		t.Fatalf("unset per-tenant ref must fail closed, got %v", err)
	}
}

func TestResolveWebhookSecret_PerTenantEnvRefStillWorks(t *testing.T) {
	t.Setenv("TENANT_A_WEBHOOK_SECRET", "tenant_a_fake")
	got, err := ResolveWebhookSecret("", "env:TENANT_A_WEBHOOK_SECRET")
	if err != nil || got != "tenant_a_fake" {
		t.Fatalf("per-tenant env ref: err=%v", err)
	}
}

func TestResolveWebhookSecret_LegacyPlaintextAndMissingKeyFailClosed(t *testing.T) {
	setKey(t)
	t.Setenv("RAZORPAY_WEBHOOK_SECRET", "global_fake_secret")
	if _, err := ResolveWebhookSecret("legacy_plain_secret", ""); !errors.Is(err, secretbox.ErrLegacyPlaintext) {
		t.Fatalf("legacy plaintext must be rejected, got %v", err)
	}
	enc, _ := secretbox.Encrypt("tenant_fake_secret")
	t.Setenv(secretbox.EnvKey, "")
	if _, err := ResolveWebhookSecret(enc, "env:TENANT_A_WEBHOOK_SECRET"); !errors.Is(err, secretbox.ErrMissingKey) {
		t.Fatalf("missing key must fail closed (no ref/env fallthrough), got %v", err)
	}
}
