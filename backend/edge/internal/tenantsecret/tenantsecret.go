// Package tenantsecret resolves a connector's (tenant's) webhook secret.
//
// Rules (D32, D11):
//   - The connectors.secret column is always stored encrypted (secretbox
//     enc:v1:). Legacy plaintext is rejected, never used.
//   - The existing per-connector reference pattern (webhook_secret_ref =
//     "env:NAME") is kept, but a reference to a shared, process-wide Razorpay
//     secret (RAZORPAY_WEBHOOK_SECRET, RAZORPAY_KEY_SECRET, ...) is refused:
//     that is the cross-tenant fallback D32 rules out.
//   - There is no fallback to a global env secret. A tenant with no secret
//     gets ErrNoTenantSecret and its webhooks are rejected.
package tenantsecret

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"zord-edge/internal/secretbox"
)

// ErrNoTenantSecret means the connector has no usable secret of its own.
var ErrNoTenantSecret = errors.New("tenant connector has no webhook secret configured")

// ErrSharedSecretRef means webhook_secret_ref points at a shared global secret.
var ErrSharedSecretRef = errors.New("webhook_secret_ref points at a shared global secret; configure a per-tenant secret")

// sharedEnvNames are process-wide Razorpay secrets that must never serve as a
// tenant's secret.
var sharedEnvNames = map[string]bool{
	"RAZORPAY_WEBHOOK_SECRET":  true,
	"RAZORPAY_KEY_SECRET":      true,
	"RAZORPAY_LIVE_KEY_SECRET": true,
	"RAZORPAY_KEY_ID":          true,
	"RAZORPAY_LIVE_KEY_ID":     true,
}

// IsSharedEnvName reports whether name is a shared global Razorpay secret.
func IsSharedEnvName(name string) bool {
	return sharedEnvNames[strings.ToUpper(strings.TrimSpace(name))]
}

// ResolveWebhookSecret returns the plaintext webhook secret for a connector.
//
// stored is connectors.secret (must be enc:v1:), ref is
// connectors.webhook_secret_ref. If stored is present it wins, and any
// decrypt error (missing key, tampered, legacy plaintext) is returned as-is
// (fail closed; it never falls through to ref or env).
func ResolveWebhookSecret(stored, ref string) (string, error) {
	stored = strings.TrimSpace(stored)
	if stored != "" {
		plain, err := secretbox.Decrypt(stored)
		if err != nil {
			return "", fmt.Errorf("connector secret: %w", err)
		}
		return plain, nil
	}
	ref = strings.TrimSpace(ref)
	if strings.HasPrefix(ref, "env:") {
		name := strings.TrimSpace(strings.TrimPrefix(ref, "env:"))
		if IsSharedEnvName(name) {
			return "", ErrSharedSecretRef
		}
		if v := os.Getenv(name); v != "" {
			return v, nil
		}
	}
	return "", ErrNoTenantSecret
}
