package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoEnv sets placeholder env values (never real secrets) so the committed
// config.yaml passes validate().
func repoEnv(t *testing.T) {
	t.Helper()
	t.Setenv("RELAY_CONFIG_FILE", "")
	t.Setenv("RELAY_AUTH_TOKEN", "change-me-relay-token")
	t.Setenv("RELAY_OPERATOR_AUTH_TOKEN", "change-me-operator-token")
	t.Setenv("RELAY_DB_URL", "postgres://relay_user:change-me@postgres:5432/zord_relay_db?sslmode=disable")
	t.Setenv("RELAY_TOKEN_ENCLAVE_BASE_URL", "http://zord-token-enclave:8087")
	t.Setenv("ROUTER_URL", "")
	t.Setenv("RELAY_DISPATCH_ENABLED", "")
	t.Setenv("RELAY_DISPATCH_ROUTER_URL", "")
}

func loadRepoConfig(t *testing.T) *Config {
	t.Helper()
	cfg, err := LoadFrom("..")
	if err != nil {
		t.Fatalf("load repo config.yaml: %v", err)
	}
	return cfg
}

func TestRepoConfigKeepsDispatchOff(t *testing.T) {
	repoEnv(t)
	cfg := loadRepoConfig(t)
	if cfg.Dispatch.Enabled {
		t.Fatal("dispatch.enabled must stay false in the committed config (D27)")
	}
}

func TestDispatchDefaultIsOffWithoutConfigFile(t *testing.T) {
	repoEnv(t)
	dir := t.TempDir()
	// Minimal file with no dispatch section: the default must be OFF.
	body := `kafka:
  brokers: "b:9092"
services:
  - name: s
    base_url: http://s
    auth_token: "${RELAY_AUTH_TOKEN}"
    default_topic: t
db:
  url: "${RELAY_DB_URL}"
tracing:
  enabled: false
token_enclave:
  base_url: http://enclave
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFrom(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Dispatch.Enabled {
		t.Fatal("dispatch must default to disabled")
	}
	if cfg.Services[0].AuthToken != "change-me-relay-token" {
		t.Fatalf("auth_token not expanded from env: %q", cfg.Services[0].AuthToken)
	}
}

func TestRepoConfigRouterURLIsInternalServiceURL(t *testing.T) {
	repoEnv(t)
	cfg := loadRepoConfig(t)
	if cfg.Dispatch.RouterURL != "http://zord-router:8091" {
		t.Fatalf("router_url=%q want http://zord-router:8091", cfg.Dispatch.RouterURL)
	}
	t.Setenv("ROUTER_URL", "http://router.internal:9000")
	cfg = loadRepoConfig(t)
	if cfg.Dispatch.RouterURL != "http://router.internal:9000" {
		t.Fatalf("ROUTER_URL override ignored: %q", cfg.Dispatch.RouterURL)
	}
}

func TestRepoConfigSecretsComeFromEnv(t *testing.T) {
	repoEnv(t)
	cfg := loadRepoConfig(t)
	for _, svc := range cfg.Services {
		if svc.AuthToken != "change-me-relay-token" {
			t.Fatalf("service %s auth_token=%q not from RELAY_AUTH_TOKEN", svc.Name, svc.AuthToken)
		}
	}
	if cfg.Relay.OperatorAuthToken != "change-me-operator-token" {
		t.Fatalf("operator token not from env: %q", cfg.Relay.OperatorAuthToken)
	}
	if !strings.Contains(cfg.DB.URL, "change-me") {
		t.Fatalf("db.url not from env")
	}
	raw, err := os.ReadFile("../config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"dev-dummy-token-123", "test123", "relay_password"} {
		if strings.Contains(string(raw), banned) {
			t.Fatalf("config.yaml still contains hardcoded secret %q", banned)
		}
	}
}

func TestMissingRelayAuthTokenFailsValidation(t *testing.T) {
	repoEnv(t)
	t.Setenv("RELAY_AUTH_TOKEN", "")
	if _, err := LoadFrom(".."); err == nil || !strings.Contains(err.Error(), "auth_token is required") {
		t.Fatalf("want auth_token required error, got %v", err)
	}
}

func TestExpandEnv(t *testing.T) {
	t.Setenv("CL_SET", "v1")
	t.Setenv("CL_EMPTY", "")
	cases := map[string]string{
		"${CL_SET}":            "v1",
		"${CL_EMPTY:-dflt}":    "dflt",
		"${CL_UNSET_X:-a:b/c}": "a:b/c",
		"${CL_UNSET_X}":        "",
		"$CL_SET":              "$CL_SET",
		"pre-${CL_SET}-post":   "pre-v1-post",
	}
	for in, want := range cases {
		if got := ExpandEnv(in); got != want {
			t.Errorf("ExpandEnv(%q)=%q want %q", in, got, want)
		}
	}
}
