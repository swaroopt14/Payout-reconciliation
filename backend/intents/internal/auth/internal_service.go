package auth

import (
	"crypto/subtle"
	"log"
	"net/http"
	"os"
	"strings"
)

// ScopeIntentReadCrossTenant is required to call /internal/dlq/count, which
// reads DLQ data with no tenant filter, across every tenant, so it demands a
// stronger credential than an end-user session.
const ScopeIntentReadCrossTenant = "intent.read.cross_tenant"

// Scopes for the remaining /internal/* routes (Slice 8). They are held by
// the relay/scheduler principal authenticated with RELAY_AUTH_TOKEN via
// X-Relay-Token — the credential those callers already send — so wrapping
// the routes in RequireInternalScope adds a uniform, audited, fail-closed
// gate without breaking any existing caller. The console token does NOT get
// them, and the relay token does NOT get ScopeIntentReadCrossTenant.
const (
	// ScopeOutboxRelay: /internal/outbox/*, /internal/dlq/{lease,ack,nack},
	// /internal/relay/canonical_batches/*.
	ScopeOutboxRelay = "intent.outbox.relay"
	// ScopeETLTransform: /internal/airflow/transform.
	ScopeETLTransform = "intent.etl.transform"
	// ScopeNormalizationRead: /internal/normalization/quality.
	ScopeNormalizationRead = "intent.normalization.read"
)

// relayServiceScopes are granted to a caller presenting a valid X-Relay-Token.
var relayServiceScopes = []string{ScopeOutboxRelay, ScopeETLTransform, ScopeNormalizationRead}

// InternalServicePrincipal is the verified identity of a same-cluster
// service calling an /internal/* route directly — never an end-user.
type InternalServicePrincipal struct {
	ServiceID string
	Scopes    []string
}

func (p InternalServicePrincipal) HasScope(scope string) bool {
	for _, s := range p.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

// internalServiceTokens returns the configured token -> principal mapping.
// Today there is exactly one known internal caller of these routes
// (zord-console's server-side BFF, see services/backend/intents.ts),
// authenticated via INTENT_ENGINE_INTERNAL_SERVICE_TOKEN. Kept as a map so a second
// internal caller with its own token/scope set can be added later without
// reshaping RequireInternalScope.
//
// INTENT_ENGINE_INTERNAL_SERVICE_TOKEN accepts a comma-separated list so a rotation can
// run the old and new token side by side — deploy the new value here first,
// roll the caller over to it, then drop the old one — instead of needing a
// single atomic cutover. Every listed token maps to the same principal.
func internalServiceTokens() map[string]InternalServicePrincipal {
	tokens := map[string]InternalServicePrincipal{}
	raw := strings.Split(os.Getenv("INTENT_ENGINE_INTERNAL_SERVICE_TOKEN"), ",")
	for _, v := range raw {
		if v = strings.TrimSpace(v); v != "" {
			tokens[v] = InternalServicePrincipal{
				ServiceID: "zord-console",
				Scopes:    []string{ScopeIntentReadCrossTenant},
			}
		}
	}
	return tokens
}

// RequireInternalScope protects an /internal/* route with a signed internal
// service token (R-02) instead of Kong's end-user JWT — these routes are
// deliberately excluded from Kong's route table (kubernetes/api-gateway),
// so the only thing standing between them and any pod on the cluster
// network today is that omission. This is fail-closed by design: unlike the
// pre-existing authorizeRelay() in internal/handlers/outbox_handler.go
// (which allows every caller through when RELAY_AUTH_TOKEN is unset), a
// missing INTENT_ENGINE_INTERNAL_SERVICE_TOKEN configuration here denies every request
// rather than silently disabling the check.
//
// A caller must present X-Internal-Service-Token (or, for the relay/
// scheduler principal, X-Relay-Token — see resolveInternalPrincipal)
// matching a configured token, and that token's principal must carry
// requiredScope:
//   - no token, or a token matching nothing configured -> 401 (unauthenticated)
//   - recognised token lacking requiredScope           -> 403 (forbidden)
//   - recognised token with requiredScope               -> next() runs
//
// End-user Authorization bearer JWTs are never consulted here — they simply
// aren't the credential type this route expects, so presenting one without
// X-Internal-Service-Token is indistinguishable from presenting nothing.
func RequireInternalScope(requiredScope string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		principal, present, ok := resolveInternalPrincipal(r)
		if !present {
			logInternalAccess(r, "", nil, requiredScope, http.StatusUnauthorized)
			writeAuthError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "missing internal service token")
			return
		}
		if !ok {
			logInternalAccess(r, "", nil, requiredScope, http.StatusUnauthorized)
			writeAuthError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "invalid internal service token")
			return
		}

		if !principal.HasScope(requiredScope) {
			logInternalAccess(r, principal.ServiceID, principal.Scopes, requiredScope, http.StatusForbidden)
			writeAuthError(w, http.StatusForbidden, "SCOPE_FORBIDDEN", "internal service token missing required scope")
			return
		}

		logInternalAccess(r, principal.ServiceID, principal.Scopes, requiredScope, http.StatusOK)
		next(w, r)
	}
}

// resolveInternalPrincipal authenticates an internal caller. It checks
// X-Internal-Service-Token first (console BFF), then X-Relay-Token
// (relay/scheduler, RELAY_AUTH_TOKEN, comma-separated for rotation).
// present reports whether any internal credential header was sent at all;
// ok reports whether it matched a configured token. Both configs are
// fail-closed: an unset env var matches nothing.
func resolveInternalPrincipal(r *http.Request) (principal InternalServicePrincipal, present bool, ok bool) {
	if token := strings.TrimSpace(r.Header.Get("X-Internal-Service-Token")); token != "" {
		p, found := internalServiceTokens()[token]
		return p, true, found
	}
	if token := strings.TrimSpace(r.Header.Get("X-Relay-Token")); token != "" {
		for _, candidate := range strings.Split(os.Getenv("RELAY_AUTH_TOKEN"), ",") {
			candidate = strings.TrimSpace(candidate)
			if candidate != "" && subtle.ConstantTimeCompare([]byte(candidate), []byte(token)) == 1 {
				return InternalServicePrincipal{
					ServiceID: "zord-relay",
					Scopes:    append([]string(nil), relayServiceScopes...),
				}, true, true
			}
		}
		return InternalServicePrincipal{}, true, false
	}
	return InternalServicePrincipal{}, false, false
}

// logInternalAccess records caller service, granted scopes, the scope this
// route required, the request target (query string) and outcome status, per
// R-02's audit requirement. request_id is best-effort: these routes bypass Kong (whose
// correlation-id plugin stamps X-Request-Id on gateway-routed traffic), so
// direct internal-network callers may not carry one.
func logInternalAccess(r *http.Request, serviceID string, scopes []string, requiredScope string, status int) {
	requestID := strings.TrimSpace(r.Header.Get("X-Request-Id"))
	if requestID == "" {
		requestID = "-"
	}
	log.Printf(
		"internal_access route=%s method=%s caller_service=%q scopes=%v required_scope=%q request_id=%s target=%q status=%d",
		r.URL.Path, r.Method, serviceID, scopes, requiredScope, requestID, r.URL.RawQuery, status,
	)
}
