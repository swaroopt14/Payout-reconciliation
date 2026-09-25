package middleware

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

type fakeClock struct{ t time.Time }

func (f *fakeClock) now() time.Time { return f.t }

func testLimits(clock *fakeClock) *EdgeRateLimits {
	return &EdgeRateLimits{
		Store:              NewMemoryRateLimitStore(time.Minute, 1000),
		WebhookTenant:      RateLimitPolicy{Name: "webhook_tenant", Limit: rate.Limit(1), Burst: 3, Key: KeyTenant},
		WebhookIPConnector: RateLimitPolicy{Name: "webhook_ip_connector", Limit: rate.Limit(1), Burst: 2, Key: KeyIPConnector},
		LoginIP:            RateLimitPolicy{Name: "login_ip", Limit: rate.Every(30 * time.Second), Burst: 2, Key: KeyIP},
		now:                clock.now,
	}
}

// setTenant stands in for VerifyWebhookSignature, which puts the VERIFIED
// tenant on the context. The test reads it from a header only to drive it.
func setTenant() gin.HandlerFunc {
	return func(c *gin.Context) {
		if t := c.GetHeader("X-Test-Verified-Tenant"); t != "" {
			c.Set("tenant_id", t)
		}
		c.Next()
	}
}

func newRouter(rl *EdgeRateLimits) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	ok := func(c *gin.Context) { c.Status(http.StatusOK) }
	r.POST("/v1/auth/login", rl.Login(), ok)
	r.POST("/v1/raw/envelopes/webhooks/:provider/:connectorID", rl.WebhookPreAuth(), setTenant(), rl.WebhookTenantLimit(), ok)
	r.POST("/v1/webhooks/razorpay/:connectorID", rl.WebhookPreAuth(), rl.WebhookTenantLimit(), ok)
	return r
}

func do(r http.Handler, path, ip, tenant string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, nil)
	req.RemoteAddr = ip + ":12345"
	if tenant != "" {
		req.Header.Set("X-Test-Verified-Tenant", tenant)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestRateLimit_429WithRetryAfter(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	r := newRouter(testLimits(clock))

	for i := 0; i < 2; i++ {
		if w := do(r, "/v1/auth/login", "10.0.0.1", ""); w.Code != http.StatusOK {
			t.Fatalf("login %d: want 200 got %d", i, w.Code)
		}
	}
	w := do(r, "/v1/auth/login", "10.0.0.1", "")
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("want 429 got %d", w.Code)
	}
	ra, err := strconv.Atoi(w.Header().Get("Retry-After"))
	if err != nil || ra < 1 || ra > 30 {
		t.Fatalf("Retry-After=%q, want 1..30 seconds", w.Header().Get("Retry-After"))
	}
	// A rejected request must not consume a token: after Retry-After passes,
	// exactly one more request is allowed.
	clock.t = clock.t.Add(time.Duration(ra) * time.Second)
	if w := do(r, "/v1/auth/login", "10.0.0.1", ""); w.Code != http.StatusOK {
		t.Fatalf("after Retry-After: want 200 got %d", w.Code)
	}
	if w := do(r, "/v1/auth/login", "10.0.0.1", ""); w.Code != http.StatusTooManyRequests {
		t.Fatalf("bucket should be empty again, got %d", w.Code)
	}
	// Another IP is unaffected.
	if w := do(r, "/v1/auth/login", "10.0.0.2", ""); w.Code != http.StatusOK {
		t.Fatalf("other IP: want 200 got %d", w.Code)
	}
}

func TestRateLimit_PerTenantIsolation(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	r := newRouter(testLimits(clock))
	// Tenant A floods through many IPs/connectors so only the tenant bucket
	// (burst 3) can trip.
	for i := 0; i < 3; i++ {
		ip := "10.1.0." + strconv.Itoa(i+1)
		if w := do(r, "/v1/raw/envelopes/webhooks/razorpay/conn-a"+strconv.Itoa(i), ip, "tenant-A"); w.Code != http.StatusOK {
			t.Fatalf("tenant A %d: want 200 got %d", i, w.Code)
		}
	}
	w := do(r, "/v1/raw/envelopes/webhooks/razorpay/conn-a9", "10.1.0.9", "tenant-A")
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Fatalf("tenant A over limit: want 429 + Retry-After got %d", w.Code)
	}
	// Tenant B is untouched by A's exhaustion.
	for i := 0; i < 3; i++ {
		ip := "10.2.0." + strconv.Itoa(i+1)
		if w := do(r, "/v1/raw/envelopes/webhooks/razorpay/conn-b"+strconv.Itoa(i), ip, "tenant-B"); w.Code != http.StatusOK {
			t.Fatalf("tenant B %d: want 200 got %d (isolation broken)", i, w.Code)
		}
	}
}

func TestRateLimit_PerIPConnectorOnWebhook(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_800_000_000, 0)}
	rl := testLimits(clock)
	rl.WebhookTenant.Burst = 1000 // isolate the IP+connector bucket
	r := newRouter(rl)

	path := "/v1/webhooks/razorpay/conn-1"
	for i := 0; i < 2; i++ {
		if w := do(r, path, "10.9.9.9", ""); w.Code != http.StatusOK {
			t.Fatalf("req %d: want 200 got %d", i, w.Code)
		}
	}
	if w := do(r, path, "10.9.9.9", ""); w.Code != http.StatusTooManyRequests {
		t.Fatalf("same IP+connector: want 429 got %d", w.Code)
	}
	// Same IP, different connector: separate bucket.
	if w := do(r, "/v1/webhooks/razorpay/conn-2", "10.9.9.9", ""); w.Code != http.StatusOK {
		t.Fatalf("other connector: want 200 got %d", w.Code)
	}
	// Different IP, same connector: separate bucket.
	if w := do(r, path, "10.9.9.8", ""); w.Code != http.StatusOK {
		t.Fatalf("other IP: want 200 got %d", w.Code)
	}
	// Refill: one token per second.
	clock.t = clock.t.Add(time.Second)
	if w := do(r, path, "10.9.9.9", ""); w.Code != http.StatusOK {
		t.Fatalf("after refill: want 200 got %d", w.Code)
	}
}

func TestRateLimit_EvictsIdleAndBoundsKeys(t *testing.T) {
	store := NewMemoryRateLimitStore(time.Minute, 3)
	now := time.Unix(1_800_000_000, 0)
	for i := 0; i < 10; i++ {
		store.Allow("k"+strconv.Itoa(i), 1, 1, now.Add(time.Duration(i)*time.Millisecond))
	}
	if n := store.Len(); n > 3 {
		t.Fatalf("store grew past maxKeys: %d", n)
	}
	store.Allow("fresh", 1, 1, now.Add(2*time.Minute))
	if n := store.Len(); n != 1 {
		t.Fatalf("idle buckets not swept: %d", n)
	}
}
