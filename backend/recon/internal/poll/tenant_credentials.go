package poll

// D52: recon gets each tenant's Razorpay key pair from edge over an internal
// endpoint. Credentials live in memory only: never on disk, in logs, Kafka,
// the DB or error strings. Every error below is built from fixed text plus
// ids/status codes; no upstream body or decoder message is ever wrapped.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// ErrCredentialSourceUnavailable means the credential source could not answer
// (edge down, timeout, 5xx, 503, bad response, misconfiguration). Transient:
// recon skips enrichment and the job reports it; it never falls back.
var ErrCredentialSourceUnavailable = errors.New("tenant credential source unavailable")

// ErrLiveCredentialsNeedTLS means a live-mode credential request was about to
// go over plain http:// (D52: TLS on /internal/v1/* before live mode).
var ErrLiveCredentialsNeedTLS = errors.New("live-mode credentials require an https:// edge URL")

// Env var names (D52). RECON_CREDENTIALS_TOKEN must differ from
// RELAY_AUTH_TOKEN so relay's token can never read credentials.
const (
	EnvReconCredentialsToken = "RECON_CREDENTIALS_TOKEN"
	EnvRelayAuthToken        = "RELAY_AUTH_TOKEN"
)

// ValidateCredentialsToken fails closed when the recon credentials token is
// empty or equal to the relay token.
func ValidateCredentialsToken(reconToken, relayToken string) error {
	reconToken, relayToken = strings.TrimSpace(reconToken), strings.TrimSpace(relayToken)
	if reconToken == "" {
		return fmt.Errorf("%s is not set", EnvReconCredentialsToken)
	}
	if reconToken == relayToken {
		return fmt.Errorf("%s must differ from %s", EnvReconCredentialsToken, EnvRelayAuthToken)
	}
	return nil
}

// EdgeCredentialClient fetches one tenant connector's key pair from edge:
// GET {BaseURL}/internal/v1/tenants/{tenant}/connectors/{connector}/razorpay-credentials?mode=
type EdgeCredentialClient struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

// NewEdgeCredentialClientFromEnv builds the client from ZORD_EDGE_URL and
// RECON_CREDENTIALS_TOKEN, failing closed on a missing/shared token.
func NewEdgeCredentialClientFromEnv() (*EdgeCredentialClient, error) {
	token := os.Getenv(EnvReconCredentialsToken)
	if err := ValidateCredentialsToken(token, os.Getenv(EnvRelayAuthToken)); err != nil {
		return nil, err
	}
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("ZORD_EDGE_URL")), "/")
	if base == "" {
		return nil, errors.New("ZORD_EDGE_URL is not set")
	}
	return &EdgeCredentialClient{BaseURL: base, Token: strings.TrimSpace(token), HTTPClient: &http.Client{Timeout: 3 * time.Second}}, nil
}

func (c *EdgeCredentialClient) TenantCredentials(ctx context.Context, tenantID, connectorID, mode string) (string, string, error) {
	if c == nil || strings.TrimSpace(c.BaseURL) == "" || strings.TrimSpace(c.Token) == "" {
		return "", "", fmt.Errorf("%w: client not configured", ErrCredentialSourceUnavailable)
	}
	if mode != "live" {
		mode = "test"
	}
	base, err := url.Parse(strings.TrimRight(c.BaseURL, "/"))
	if err != nil || base.Host == "" {
		return "", "", fmt.Errorf("%w: invalid edge URL", ErrCredentialSourceUnavailable)
	}
	if mode == "live" && !strings.EqualFold(base.Scheme, "https") {
		return "", "", ErrLiveCredentialsNeedTLS
	}
	u := base.JoinPath("internal", "v1", "tenants", tenantID, "connectors", connectorID, "razorpay-credentials")
	q := url.Values{}
	q.Set("mode", mode)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", "", fmt.Errorf("%w: build request", ErrCredentialSourceUnavailable)
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("X-Service-Tenant-ID", tenantID)
	req.Header.Set("Accept", "application/json")
	hc := c.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 3 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		// Never wrap the transport error: keep the message fixed.
		reason := "request failed"
		if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
			reason = "timeout"
		}
		return "", "", fmt.Errorf("%w: %s", ErrCredentialSourceUnavailable, reason)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return "", "", ErrNoTenantSecret
	case resp.StatusCode != http.StatusOK:
		// The body is dropped: it may echo anything.
		return "", "", fmt.Errorf("%w: edge HTTP %d", ErrCredentialSourceUnavailable, resp.StatusCode)
	}
	var parsed struct {
		KeyID     string `json:"key_id"`
		KeySecret string `json:"key_secret"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", "", fmt.Errorf("%w: malformed edge response", ErrCredentialSourceUnavailable)
	}
	if strings.TrimSpace(parsed.KeyID) == "" || strings.TrimSpace(parsed.KeySecret) == "" {
		return "", "", ErrNoTenantSecret
	}
	return parsed.KeyID, parsed.KeySecret, nil
}

func isTimeout(err error) bool {
	var t interface{ Timeout() bool }
	return errors.As(err, &t) && t.Timeout()
}

// CredentialInvalidator drops a cached key pair (called on a Razorpay 401).
type CredentialInvalidator interface {
	Invalidate(tenantID, connectorID, mode string)
}

type credEntry struct {
	keyID, keySecret string
	err              error
	expires          time.Time
}

// CachedCredentialSource caches a TenantCredentialSource in memory only.
// Positive entries live TTL (default 5m); ErrNoTenantSecret lives NegativeTTL
// (default 60s); unavailable errors are never cached. At most Max entries
// (default 1024); the soonest-expiring entry is evicted first.
type CachedCredentialSource struct {
	Src         TenantCredentialSource
	TTL         time.Duration
	NegativeTTL time.Duration
	Max         int
	Now         func() time.Time

	mu      sync.Mutex
	entries map[string]credEntry
}

func credKey(tenantID, connectorID, mode string) string {
	return strings.ToLower(strings.TrimSpace(tenantID)) + "|" + strings.ToLower(strings.TrimSpace(connectorID)) + "|" + mode
}

func (c *CachedCredentialSource) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *CachedCredentialSource) TenantCredentials(ctx context.Context, tenantID, connectorID, mode string) (string, string, error) {
	if c == nil || c.Src == nil {
		return "", "", fmt.Errorf("%w: no source", ErrCredentialSourceUnavailable)
	}
	if mode != "live" {
		mode = "test"
	}
	key := credKey(tenantID, connectorID, mode)
	now := c.now()
	c.mu.Lock()
	if e, ok := c.entries[key]; ok && now.Before(e.expires) {
		c.mu.Unlock()
		return e.keyID, e.keySecret, e.err
	}
	c.mu.Unlock()

	keyID, keySecret, err := c.Src.TenantCredentials(ctx, tenantID, connectorID, mode)
	var ttl time.Duration
	switch {
	case err == nil:
		ttl = c.TTL
		if ttl <= 0 {
			ttl = 5 * time.Minute
		}
	case errors.Is(err, ErrNoTenantSecret):
		ttl = c.NegativeTTL
		if ttl <= 0 {
			ttl = 60 * time.Second
		}
	default:
		return "", "", err
	}
	c.mu.Lock()
	if c.entries == nil {
		c.entries = map[string]credEntry{}
	}
	max := c.Max
	if max <= 0 {
		max = 1024
	}
	for len(c.entries) >= max {
		var oldest string
		var oldestAt time.Time
		for k, e := range c.entries {
			if oldest == "" || e.expires.Before(oldestAt) {
				oldest, oldestAt = k, e.expires
			}
		}
		delete(c.entries, oldest)
	}
	c.entries[key] = credEntry{keyID: keyID, keySecret: keySecret, err: err, expires: now.Add(ttl)}
	c.mu.Unlock()
	return keyID, keySecret, err
}

// Invalidate drops one cached entry so the next call refetches from edge.
func (c *CachedCredentialSource) Invalidate(tenantID, connectorID, mode string) {
	if c == nil {
		return
	}
	if mode != "live" {
		mode = "test"
	}
	c.mu.Lock()
	delete(c.entries, credKey(tenantID, connectorID, mode))
	c.mu.Unlock()
}

// Invalidate forwards to the tenant source when it can drop cached creds.
func (r EnvCredentialResolver) Invalidate(tenantID, connectorID, mode string) {
	if inv, ok := r.Tenant.(CredentialInvalidator); ok {
		inv.Invalidate(tenantID, connectorID, mode)
	}
}

// unavailableSource is wired when the credential client is misconfigured:
// every tenant lookup fails closed as unavailable (never the platform key).
type unavailableSource struct{ reason string }

func (u unavailableSource) TenantCredentials(context.Context, string, string, string) (string, string, error) {
	return "", "", fmt.Errorf("%w: %s", ErrCredentialSourceUnavailable, u.reason)
}

// NewUnavailableCredentialSource returns a source that always fails closed.
func NewUnavailableCredentialSource(reason string) TenantCredentialSource {
	return unavailableSource{reason: reason}
}

// CredentialFailureCode maps a credential resolution error to the job's
// visible reason code.
func CredentialFailureCode(err error) string {
	switch {
	case errors.Is(err, ErrNoTenantSecret):
		return "NO_CREDENTIALS"
	case errors.Is(err, ErrCredentialSourceUnavailable), errors.Is(err, ErrLiveCredentialsNeedTLS):
		return "CREDENTIALS_UNAVAILABLE"
	}
	return "CREDENTIALS"
}

// redactSecrets removes each non-empty secret from msg.
func redactSecrets(msg string, secrets ...string) string {
	for _, s := range secrets {
		if strings.TrimSpace(s) != "" {
			msg = strings.ReplaceAll(msg, s, "[redacted]")
		}
	}
	return msg
}

// labeledCounter is an in-process counter keyed by a label value.
type labeledCounter struct {
	mu sync.Mutex
	m  map[string]int64
}

func (c *labeledCounter) Inc(label string) {
	c.mu.Lock()
	if c.m == nil {
		c.m = map[string]int64{}
	}
	c.m[label]++
	c.mu.Unlock()
}

func (c *labeledCounter) Get(label string) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.m[label]
}

var (
	// observe_enrichment_skipped_total{reason}
	enrichmentSkippedTotal labeledCounter
	// backfill_credentials_missing_total{reason}
	backfillCredentialsMissingTotal labeledCounter
)

// ObserveEnrichmentSkipped increments observe_enrichment_skipped_total{reason}.
func ObserveEnrichmentSkipped(reason string) { enrichmentSkippedTotal.Inc(reason) }

// EnrichmentSkippedTotal reads observe_enrichment_skipped_total{reason}.
func EnrichmentSkippedTotal(reason string) int64 { return enrichmentSkippedTotal.Get(reason) }

// BackfillCredentialsMissingTotal reads backfill_credentials_missing_total{reason}.
func BackfillCredentialsMissingTotal(reason string) int64 {
	return backfillCredentialsMissingTotal.Get(reason)
}
