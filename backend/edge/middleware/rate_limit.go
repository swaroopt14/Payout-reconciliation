package middleware

import (
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"zord-edge/logger"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

// ---------------------------------------------------------------------------
// Store
// ---------------------------------------------------------------------------

// RateLimitStore decides whether one more request under key is allowed.
// MemoryRateLimitStore is the implementation for a single edge replica; a
// Redis-backed store can satisfy the same interface if edge ever runs more
// than one replica (buckets would otherwise be per-pod).
type RateLimitStore interface {
	// Allow consumes one token from key's bucket (created on first use with
	// limit/burst). When denied it returns how long until a token is free.
	Allow(key string, limit rate.Limit, burst int, now time.Time) (allowed bool, retryAfter time.Duration)
}

type rateBucket struct {
	lim      *rate.Limiter
	lastSeen time.Time
}

// MemoryRateLimitStore keeps token buckets in memory with bounded size:
// buckets idle longer than idleTTL are swept, and when maxKeys is reached
// the least-recently-seen bucket is evicted before a new one is created.
// An evicted/idle bucket restarts full, which is correct because an idle
// bucket would have refilled anyway.
type MemoryRateLimitStore struct {
	mu        sync.Mutex
	buckets   map[string]*rateBucket
	idleTTL   time.Duration
	maxKeys   int
	lastSweep time.Time
}

func NewMemoryRateLimitStore(idleTTL time.Duration, maxKeys int) *MemoryRateLimitStore {
	if idleTTL <= 0 {
		idleTTL = 10 * time.Minute
	}
	if maxKeys <= 0 {
		maxKeys = 100000
	}
	return &MemoryRateLimitStore{buckets: map[string]*rateBucket{}, idleTTL: idleTTL, maxKeys: maxKeys}
}

// Len reports the number of live buckets (for tests/metrics).
func (s *MemoryRateLimitStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.buckets)
}

func (s *MemoryRateLimitStore) Allow(key string, limit rate.Limit, burst int, now time.Time) (bool, time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if now.Sub(s.lastSweep) >= s.idleTTL/2 {
		s.sweepLocked(now)
	}
	b, ok := s.buckets[key]
	if !ok {
		if len(s.buckets) >= s.maxKeys {
			s.sweepLocked(now)
			if len(s.buckets) >= s.maxKeys {
				s.evictOldestLocked()
			}
		}
		b = &rateBucket{lim: rate.NewLimiter(limit, burst)}
		s.buckets[key] = b
	}
	b.lastSeen = now

	res := b.lim.ReserveN(now, 1)
	if !res.OK() {
		// burst < 1: nothing can ever pass.
		return false, time.Second
	}
	if d := res.DelayFrom(now); d > 0 {
		res.CancelAt(now) // do not consume a token for a rejected request
		return false, d
	}
	return true, 0
}

func (s *MemoryRateLimitStore) sweepLocked(now time.Time) {
	for k, b := range s.buckets {
		if now.Sub(b.lastSeen) > s.idleTTL {
			delete(s.buckets, k)
		}
	}
	s.lastSweep = now
}

func (s *MemoryRateLimitStore) evictOldestLocked() {
	var oldestKey string
	var oldest time.Time
	for k, b := range s.buckets {
		if oldestKey == "" || b.lastSeen.Before(oldest) {
			oldestKey, oldest = k, b.lastSeen
		}
	}
	if oldestKey != "" {
		delete(s.buckets, oldestKey)
	}
}

// ---------------------------------------------------------------------------
// Middleware
// ---------------------------------------------------------------------------

// RateLimitPolicy is one bucket family. Key returns the bucket key for the
// request, or ok=false to skip this policy for the request.
type RateLimitPolicy struct {
	Name  string
	Limit rate.Limit
	Burst int
	Key   func(c *gin.Context) (key string, ok bool)
}

// RateLimit enforces every policy in order; the first exhausted bucket
// answers 429 with a Retry-After header (whole seconds, at least 1).
func RateLimit(store RateLimitStore, now func() time.Time, policies ...RateLimitPolicy) gin.HandlerFunc {
	if now == nil {
		now = time.Now
	}
	return func(c *gin.Context) {
		t := now()
		for _, p := range policies {
			key, ok := p.Key(c)
			if !ok || key == "" {
				continue
			}
			allowed, retry := store.Allow(p.Name+"|"+key, p.Limit, p.Burst, t)
			if allowed {
				continue
			}
			secs := int(math.Ceil(retry.Seconds()))
			if secs < 1 {
				secs = 1
			}
			c.Header("Retry-After", strconv.Itoa(secs))
			logger.Log.Warn("rate limit exceeded",
				slog.String("policy", p.Name),
				slog.String("path", c.FullPath()),
				slog.String("ip", c.ClientIP()),
				slog.Int("retry_after_s", secs))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": gin.H{"code": "RATE_LIMITED", "message": "too many requests", "retry_after_seconds": secs},
			})
			return
		}
		c.Next()
	}
}

// KeyIPConnector keys by client IP + :connectorID path param (webhooks).
func KeyIPConnector(c *gin.Context) (string, bool) {
	return c.ClientIP() + "|" + strings.TrimSpace(c.Param("connectorID")), true
}

// KeyIP keys by client IP only (pre-auth routes such as login).
func KeyIP(c *gin.Context) (string, bool) {
	return c.ClientIP(), true
}

// KeyTenant keys by the VERIFIED tenant_id an upstream middleware put on the
// context (e.g. VerifyWebhookSignature). It never trusts a caller-supplied
// tenant header: that would let anyone drain another tenant's bucket. When
// no verified tenant is available yet (the Razorpay route resolves the
// tenant inside the handler), it falls back to the connector, which belongs
// to exactly one tenant.
func KeyTenant(c *gin.Context) (string, bool) {
	if v, ok := c.Get("tenant_id"); ok {
		if s := strings.TrimSpace(fmt.Sprint(v)); s != "" && s != "<nil>" {
			return "tenant:" + s, true
		}
	}
	if conn := strings.TrimSpace(c.Param("connectorID")); conn != "" {
		return "connector:" + conn, true
	}
	return "", false
}

// ---------------------------------------------------------------------------
// Edge wiring
// ---------------------------------------------------------------------------

// EdgeRateLimits holds the configured limiters for edge's public routes.
type EdgeRateLimits struct {
	Store              RateLimitStore
	WebhookIPConnector RateLimitPolicy
	WebhookTenant      RateLimitPolicy
	LoginIP            RateLimitPolicy
	now                func() time.Time
}

// NewEdgeRateLimitsFromEnv builds limits from env (defaults in brackets):
//
//	EDGE_RL_WEBHOOK_TENANT_RPS [50]   EDGE_RL_WEBHOOK_TENANT_BURST [100]
//	EDGE_RL_WEBHOOK_IPCONN_RPS [20]   EDGE_RL_WEBHOOK_IPCONN_BURST [40]
//	EDGE_RL_LOGIN_PER_MIN      [10]   EDGE_RL_LOGIN_BURST          [5]
//	EDGE_RL_IDLE_TTL_SECONDS   [600]  EDGE_RL_MAX_KEYS             [100000]
func NewEdgeRateLimitsFromEnv() *EdgeRateLimits {
	store := NewMemoryRateLimitStore(
		time.Duration(envInt("EDGE_RL_IDLE_TTL_SECONDS", 600))*time.Second,
		envInt("EDGE_RL_MAX_KEYS", 100000),
	)
	return &EdgeRateLimits{
		Store: store,
		WebhookTenant: RateLimitPolicy{Name: "webhook_tenant",
			Limit: rate.Limit(envInt("EDGE_RL_WEBHOOK_TENANT_RPS", 50)), Burst: envInt("EDGE_RL_WEBHOOK_TENANT_BURST", 100), Key: KeyTenant},
		WebhookIPConnector: RateLimitPolicy{Name: "webhook_ip_connector",
			Limit: rate.Limit(envInt("EDGE_RL_WEBHOOK_IPCONN_RPS", 20)), Burst: envInt("EDGE_RL_WEBHOOK_IPCONN_BURST", 40), Key: KeyIPConnector},
		LoginIP: RateLimitPolicy{Name: "login_ip",
			Limit: rate.Limit(float64(envInt("EDGE_RL_LOGIN_PER_MIN", 10)) / 60.0), Burst: envInt("EDGE_RL_LOGIN_BURST", 5), Key: KeyIP},
	}
}

// WebhookPreAuth runs before signature verification: per IP+connector.
func (e *EdgeRateLimits) WebhookPreAuth() gin.HandlerFunc {
	return RateLimit(e.Store, e.now, e.WebhookIPConnector)
}

// WebhookTenantLimit runs after the tenant is known (or per connector).
func (e *EdgeRateLimits) WebhookTenantLimit() gin.HandlerFunc {
	return RateLimit(e.Store, e.now, e.WebhookTenant)
}

// Login limits unauthenticated credential endpoints per client IP.
func (e *EdgeRateLimits) Login() gin.HandlerFunc {
	return RateLimit(e.Store, e.now, e.LoginIP)
}

func envInt(name string, def int) int {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}
