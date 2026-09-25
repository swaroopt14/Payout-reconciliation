package handler

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"database/sql"

	"zord-edge/internal/secretbox"
	"zord-edge/services"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Internal service-to-service connector routes (D52).
//
//	GET /internal/v1/tenants/:tenant_id/connectors/:connector_id/razorpay-credentials?mode=test|live
//	    recon only: Authorization: Bearer $RECON_CREDENTIALS_TOKEN. Relay's
//	    token is refused (401). Returns the tenant's decrypted Razorpay key.
//	GET /internal/v1/tenants/:tenant_id/connectors/resolve?provider=razorpay&connector_id=<slug>
//	    relay or recon: RELAY_AUTH_TOKEN (X-Relay-Token or Bearer) or
//	    RECON_CREDENTIALS_TOKEN (Bearer). Returns ids only, never secrets.
//
// Both require X-Service-Tenant-ID == :tenant_id (403), are tenant-scoped
// (another tenant's connector is 404), send Cache-Control: no-store and write
// one audit event per call with ids and outcome only.
const (
	EnvReconCredentialsToken = "RECON_CREDENTIALS_TOKEN"
	EnvRelayAuthToken        = "RELAY_AUTH_TOKEN"
	// EnvInternalTLSProxy=true lets live credentials be served when TLS is
	// terminated by a trusted proxy that sets X-Forwarded-Proto: https.
	EnvInternalTLSProxy = "INTERNAL_TLS_TERMINATED_BY_PROXY"

	HeaderServiceTenantID = "X-Service-Tenant-ID"

	callerRecon = "zord-recon"
	callerRelay = "zord-relay"
)

// ErrReconCredentialsTokenUnset / ...Shared: the credentials route fails
// closed (503) until RECON_CREDENTIALS_TOKEN is set and differs from
// RELAY_AUTH_TOKEN.
var (
	ErrReconCredentialsTokenUnset  = errors.New("RECON_CREDENTIALS_TOKEN is not set")
	ErrReconCredentialsTokenShared = errors.New("RECON_CREDENTIALS_TOKEN must differ from RELAY_AUTH_TOKEN")
)

// ReconCredentialsToken returns the configured token or why it is unusable.
func ReconCredentialsToken() (string, error) {
	tok := strings.TrimSpace(os.Getenv(EnvReconCredentialsToken))
	if tok == "" {
		return "", ErrReconCredentialsTokenUnset
	}
	if tok == strings.TrimSpace(os.Getenv(EnvRelayAuthToken)) {
		return "", ErrReconCredentialsTokenShared
	}
	return tok, nil
}

// InternalConnectorHandler serves the internal connector routes.
type InternalConnectorHandler struct {
	LookupCredentials services.CredentialLookupFunc
	Resolve           services.ConnectorResolveFunc
	Audit             services.ConnectorAuditFunc // nil → services.AuditConnectorEvent
}

// NewInternalConnectorHandler wires the SQL lookups.
func NewInternalConnectorHandler(q *sql.DB) *InternalConnectorHandler {
	return &InternalConnectorHandler{
		LookupCredentials: services.SQLCredentialLookup(q),
		Resolve:           services.SQLConnectorResolve(q),
	}
}

// RegisterInternalConnectorRoutes mounts both routes on r.
func RegisterInternalConnectorRoutes(r gin.IRoutes, h *InternalConnectorHandler) {
	r.GET("/internal/v1/tenants/:tenant_id/connectors/resolve", h.ResolveConnector)
	r.GET("/internal/v1/tenants/:tenant_id/connectors/:connector_id/razorpay-credentials", h.RazorpayCredentials)
}

func (h *InternalConnectorHandler) audit(c *gin.Context, ev services.ConnectorAuditEvent) {
	f := h.Audit
	if f == nil {
		f = services.AuditConnectorEvent
	}
	f(c.Request.Context(), ev)
}

func noStore(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
}

func bearer(r *http.Request) string {
	h := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

func tokenEq(got, want string) bool {
	return got != "" && want != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// tenantGate enforces X-Service-Tenant-ID == :tenant_id. ok=false means a
// response was written.
func tenantGate(c *gin.Context) (uuid.UUID, bool) {
	param := strings.TrimSpace(c.Param("tenant_id"))
	hdr := strings.TrimSpace(c.GetHeader(HeaderServiceTenantID))
	if hdr == "" || hdr != param {
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden", "code": "TENANT_MISMATCH", "message": "X-Service-Tenant-ID must equal the path tenant"})
		return uuid.Nil, false
	}
	t, err := uuid.Parse(param)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "code": "NOT_FOUND"})
		return uuid.Nil, false
	}
	return t, true
}

func requestIsTLS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	v := strings.ToLower(strings.TrimSpace(os.Getenv(EnvInternalTLSProxy)))
	if v == "true" || v == "1" {
		return strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https")
	}
	return false
}

// RazorpayCredentials serves a tenant's decrypted Razorpay key to recon.
func (h *InternalConnectorHandler) RazorpayCredentials(c *gin.Context) {
	noStore(c)
	tenantParam, _ := uuid.Parse(strings.TrimSpace(c.Param("tenant_id")))
	connParam := strings.TrimSpace(c.Param("connector_id"))
	mode := strings.ToLower(strings.TrimSpace(c.Query("mode")))
	ev := services.ConnectorAuditEvent{Action: "credential_access", TenantID: tenantParam, ConnectorID: connParam, Mode: mode, Caller: callerRecon}

	want, err := ReconCredentialsToken()
	if err != nil {
		ev.Result = "unavailable"
		h.audit(c, ev)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "unavailable", "code": "CREDENTIALS_ENDPOINT_DISABLED"})
		return
	}
	if !tokenEq(bearer(c.Request), want) {
		ev.Result, ev.Caller = "unauthorized", ""
		h.audit(c, ev)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	tenantID, ok := tenantGate(c)
	if !ok {
		ev.Result = "forbidden"
		if c.Writer.Status() == http.StatusNotFound {
			ev.Result = "not_found"
		}
		h.audit(c, ev)
		return
	}
	if mode != "test" && mode != "live" {
		ev.Result = "invalid"
		h.audit(c, ev)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "code": "INVALID_MODE", "message": "mode must be test or live"})
		return
	}
	if mode == "live" && !requestIsTLS(c.Request) {
		ev.Result = "forbidden"
		h.audit(c, ev)
		c.JSON(http.StatusForbidden, gin.H{"error": "forbidden", "code": "LIVE_CREDENTIALS_REQUIRE_TLS"})
		return
	}
	notFound := func() {
		ev.Result = "not_found"
		h.audit(c, ev)
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "code": "NO_TENANT_CREDENTIALS"})
	}
	connectorID, err := uuid.Parse(connParam)
	if err != nil {
		notFound()
		return
	}
	row, err := h.LookupCredentials(c.Request.Context(), tenantID, connectorID, mode)
	if errors.Is(err, services.ErrConnectorNotFound) {
		notFound()
		return
	}
	if err != nil {
		ev.Result = "error"
		h.audit(c, ev)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "unavailable", "code": "CREDENTIALS_UNAVAILABLE"})
		return
	}
	keyID := strings.TrimSpace(row.APIKeyRef)
	// env: refs, legacy plaintext and missing values are not tenant
	// credentials: the tenant must re-enter keys (no platform fallback).
	if keyID == "" || strings.HasPrefix(keyID, "env:") || !secretbox.IsEncrypted(row.APISecretRef) {
		notFound()
		return
	}
	keySecret, err := secretbox.Decrypt(row.APISecretRef)
	if err != nil || keySecret == "" {
		ev.Result = "unavailable"
		h.audit(c, ev)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "unavailable", "code": "CREDENTIALS_UNAVAILABLE"})
		return
	}
	ev.Result = "ok"
	h.audit(c, ev)
	c.JSON(http.StatusOK, gin.H{
		"key_id":     keyID,
		"key_secret": keySecret,
		"mode":       mode,
		"version":    row.UpdatedAt.UTC().Format(time.RFC3339Nano),
	})
}

// ResolveConnector maps (tenant, provider, slug) to connectors.id for relay.
func (h *InternalConnectorHandler) ResolveConnector(c *gin.Context) {
	noStore(c)
	tenantParam, _ := uuid.Parse(strings.TrimSpace(c.Param("tenant_id")))
	provider := strings.ToLower(strings.TrimSpace(c.Query("provider")))
	slug := strings.TrimSpace(c.Query("connector_id"))
	ev := services.ConnectorAuditEvent{Action: "connector_resolve", TenantID: tenantParam, ConnectorID: slug}

	relayTok := strings.TrimSpace(os.Getenv(EnvRelayAuthToken))
	reconTok, reconErr := ReconCredentialsToken()
	b := bearer(c.Request)
	switch {
	case tokenEq(strings.TrimSpace(c.GetHeader("X-Relay-Token")), relayTok), tokenEq(b, relayTok):
		ev.Caller = callerRelay
	case reconErr == nil && tokenEq(b, reconTok):
		ev.Caller = callerRecon
	default:
		ev.Result = "unauthorized"
		h.audit(c, ev)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	tenantID, ok := tenantGate(c)
	if !ok {
		ev.Result = "forbidden"
		if c.Writer.Status() == http.StatusNotFound {
			ev.Result = "not_found"
		}
		h.audit(c, ev)
		return
	}
	if provider == "" || slug == "" {
		ev.Result = "invalid"
		h.audit(c, ev)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": "provider and connector_id are required"})
		return
	}
	res, err := h.Resolve(c.Request.Context(), tenantID, provider, slug)
	if errors.Is(err, services.ErrConnectorNotFound) || (err == nil && !res.Active) {
		ev.Result = "not_found"
		h.audit(c, ev)
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "code": "CONNECTOR_NOT_FOUND"})
		return
	}
	if err != nil {
		ev.Result = "error"
		h.audit(c, ev)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "unavailable"})
		return
	}
	ev.Result, ev.ConnectorID, ev.Mode = "ok", res.ID.String(), res.Mode
	h.audit(c, ev)
	c.JSON(http.StatusOK, gin.H{
		"id":           res.ID.String(),
		"provider":     res.Provider,
		"connector_id": res.ConnectorID,
		"mode":         res.Mode,
		"active":       res.Active,
	})
}
