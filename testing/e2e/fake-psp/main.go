// Command fake-psp is a test-only payout provider for the Clearline B12 E2E
// harness. It implements the contract in b12-harness-spec.md §3: it never
// talks to a real PSP, stores everything in memory, and never logs the
// webhook secret. It also serves an interim token-enclave stub
// (/v1/detokenize) because relay does not yet send a service JWT to vault
// (spec §6 R-DETOK).
package main

import (
	"log"
	"net/http"
	"os"
	"strconv"
	"time"
)

func main() {
	cfg := ConfigFromEnv()
	s := NewServer(cfg)
	addr := ":" + envOr("FAKE_PSP_PORT", "8099")
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("fake-psp listening on %s (edge=%s auto_webhook_ms=%d timeout_hold=%s)",
		addr, cfg.EdgeURL, cfg.AutoWebhookMS, cfg.TimeoutHold)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

// Config is the runtime configuration. The webhook secret is set at runtime
// through POST /_admin/config (or FAKE_PSP_WEBHOOK_SECRET) and never logged.
type Config struct {
	EdgeURL       string
	AutoWebhookMS int
	TimeoutHold   time.Duration
	ConnectorID   string
	WebhookSecret string
}

// ConfigFromEnv reads FAKE_PSP_* and RELAY_PSP_TIMEOUT_SECONDS.
// The timeout-mode hold is RELAY_PSP_TIMEOUT_SECONDS+5s unless
// FAKE_PSP_TIMEOUT_HOLD_MS overrides it (used by unit tests).
func ConfigFromEnv() Config {
	timeoutSecs := envInt("RELAY_PSP_TIMEOUT_SECONDS", 5)
	hold := time.Duration(timeoutSecs+5) * time.Second
	if ms := envInt("FAKE_PSP_TIMEOUT_HOLD_MS", 0); ms > 0 {
		hold = time.Duration(ms) * time.Millisecond
	}
	return Config{
		EdgeURL:       envOr("FAKE_PSP_EDGE_URL", "http://edge:8080"),
		AutoWebhookMS: envInt("FAKE_PSP_AUTO_WEBHOOK_MS", 500),
		TimeoutHold:   hold,
		ConnectorID:   os.Getenv("FAKE_PSP_CONNECTOR_ID"),
		WebhookSecret: os.Getenv("FAKE_PSP_WEBHOOK_SECRET"),
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
