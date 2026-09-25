package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func doXFF(r http.Handler, path, remoteIP, xff string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, nil)
	req.RemoteAddr = remoteIP + ":12345"
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestRateLimit_XForwardedForIgnoredWithoutTrustedProxy(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	r := newRouter(testLimits(clock))
	if err := ApplyTrustedProxies(r, TrustedProxiesFromEnvValue("")); err != nil {
		t.Fatal(err)
	}
	// Same socket peer rotating spoofed X-Forwarded-For values: all share one
	// key (RemoteAddr), so the login burst of 2 still trips on the 3rd call.
	for i, xff := range []string{"1.1.1.1", "2.2.2.2"} {
		if w := doXFF(r, "/v1/auth/login", "10.0.0.9", xff); w.Code != http.StatusOK {
			t.Fatalf("login %d: want 200 got %d", i, w.Code)
		}
	}
	if w := doXFF(r, "/v1/auth/login", "10.0.0.9", "3.3.3.3"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("spoofed XFF must not mint a fresh key: want 429 got %d", w.Code)
	}
}

func TestRateLimit_XForwardedForHonoredWithTrustedProxy(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	r := newRouter(testLimits(clock))
	if err := ApplyTrustedProxies(r, TrustedProxiesFromEnvValue(" 10.0.0.0/8 , 192.168.1.1")); err != nil {
		t.Fatal(err)
	}
	// Behind a trusted LB, distinct real clients get distinct keys.
	for i := 0; i < 2; i++ {
		if w := doXFF(r, "/v1/auth/login", "10.1.2.3", "203.0.113.7"); w.Code != http.StatusOK {
			t.Fatalf("client A %d: got %d", i, w.Code)
		}
	}
	if w := doXFF(r, "/v1/auth/login", "10.1.2.3", "203.0.113.7"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("client A 3rd: want 429 got %d", w.Code)
	}
	if w := doXFF(r, "/v1/auth/login", "10.1.2.3", "203.0.113.8"); w.Code != http.StatusOK {
		t.Fatalf("client B via trusted proxy must have own key: got %d", w.Code)
	}
	// An untrusted peer's XFF is still ignored even when some proxies are trusted.
	for i := 0; i < 2; i++ {
		_ = doXFF(r, "/v1/auth/login", "198.51.100.1", "203.0.113.50")
	}
	if w := doXFF(r, "/v1/auth/login", "198.51.100.1", "203.0.113.51"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("untrusted peer XFF honored: got %d", w.Code)
	}
}

func TestTrustedProxies_EnvParsingAndInvalid(t *testing.T) {
	t.Setenv(EnvTrustedProxies, "")
	if got := TrustedProxiesFromEnv(); got != nil {
		t.Fatalf("empty env must be nil, got %v", got)
	}
	t.Setenv(EnvTrustedProxies, "10.0.0.0/8,,172.16.0.1 ")
	if got := TrustedProxiesFromEnv(); len(got) != 2 || got[0] != "10.0.0.0/8" || got[1] != "172.16.0.1" {
		t.Fatalf("got %v", got)
	}
	r := newRouter(testLimits(&fakeClock{t: time.Unix(0, 0)}))
	if err := ApplyTrustedProxies(r, []string{"not-an-ip"}); err == nil {
		t.Fatal("invalid proxy must error so startup fails closed")
	}
}

// TrustedProxiesFromEnvValue parses a literal env value (test helper).
func TrustedProxiesFromEnvValue(v string) []string { return ParseTrustedProxies(v) }
