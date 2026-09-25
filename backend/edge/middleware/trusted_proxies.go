package middleware

import (
	"os"
	"strings"

	"github.com/gin-gonic/gin"
)

// EnvTrustedProxies names the comma-separated list of proxy IPs/CIDRs whose
// X-Forwarded-For / X-Real-IP headers gin may honor for ClientIP().
const EnvTrustedProxies = "EDGE_TRUSTED_PROXIES"

// ParseTrustedProxies splits a comma list of IPs/CIDRs. Empty input returns nil
// (trust no proxy: ClientIP is the socket RemoteAddr, so a client cannot spoof
// its per-IP rate-limit key with X-Forwarded-For).
func ParseTrustedProxies(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// TrustedProxiesFromEnv reads EDGE_TRUSTED_PROXIES (default: trust none).
func TrustedProxiesFromEnv() []string {
	return ParseTrustedProxies(os.Getenv(EnvTrustedProxies))
}

// ApplyTrustedProxies configures the engine. gin trusts ALL proxies by
// default, so this must be called on every engine that serves client traffic.
// Returns an error for an invalid IP/CIDR so startup can fail closed.
func ApplyTrustedProxies(r *gin.Engine, proxies []string) error {
	return r.SetTrustedProxies(proxies)
}
