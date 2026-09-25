package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"zord-edge/internal/secretbox"
	"zord-edge/services"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// maxWebhookSecretBodyBytes bounds the request body (the secret is <= 256).
const maxWebhookSecretBodyBytes = 4 << 10

// WebhookSecretAdminRoles may set a connector webhook secret: the controlled
// CONNECTOR_ADMIN role (granted only via POST /v1/admin/roles/grant) and
// PLATFORM_ADMIN within its own tenant. CUSTOMER_ADMIN (every signup),
// PAYOUT_APPROVER and role-less tenant API keys are refused (403).
var WebhookSecretAdminRoles = []string{services.RoleConnectorAdmin, services.RolePlatformAdmin}

// WithAudit replaces the audit sink (tests). Returns h for chaining.
func (h *ConnectorHandler) WithAudit(f services.ConnectorAuditFunc) *ConnectorHandler {
	h.audit = f
	return h
}

// auditSecretSet records one webhook-secret set attempt: ids + result only.
func (h *ConnectorHandler) auditSecretSet(c *gin.Context, tenantID uuid.UUID, connectorID, result string) {
	f := h.audit
	if f == nil {
		f = services.AuditConnectorEvent
	}
	f(c.Request.Context(), services.ConnectorAuditEvent{
		Action:      "webhook_secret_set",
		TenantID:    tenantID,
		ConnectorID: connectorID,
		UserID:      c.GetString("user_id"),
		Result:      result,
	})
}

type setWebhookSecretRequest struct {
	WebhookSecret string `json:"webhook_secret"`
}

// SetWebhookSecretFunc writes an encrypted webhook secret for one of the
// tenant's connectors and returns the update time.
type SetWebhookSecretFunc func(ctx context.Context, tenantID, connectorID uuid.UUID, secret string) (time.Time, error)

// SetWebhookSecret handles PUT /v1/connectors/:connectorID/webhook-secret.
//
// tenant_id comes only from the auth context (never the body/query). The
// write is a tenant-scoped UPDATE, so another tenant's connector is 404
// (same as ConnectorHandler.TestConnector: existence is not leaked). The
// secret is sealed with secretbox before any SQL; the body and the secret are
// never logged or echoed.
func (h *ConnectorHandler) SetWebhookSecret(c *gin.Context) {
	tenantID, err := uuid.Parse(strings.TrimSpace(getTenantID(c)))
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized", "message": "tenant context required"})
		return
	}
	connectorID, err := uuid.Parse(strings.TrimSpace(c.Param("connectorID")))
	if err != nil {
		// Malformed id cannot be one of the tenant's connectors.
		h.auditSecretSet(c, tenantID, "", "not_found")
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "connector not found"})
		return
	}

	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, maxWebhookSecretBodyBytes+1))
	if err != nil || len(raw) > maxWebhookSecretBodyBytes {
		h.auditSecretSet(c, tenantID, connectorID.String(), "invalid")
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": "request body too large or unreadable"})
		return
	}
	var req setWebhookSecretRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		h.auditSecretSet(c, tenantID, connectorID.String(), "invalid")
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": "body must be JSON {\"webhook_secret\": string}"})
		return
	}
	if err := services.ValidateWebhookSecret(req.WebhookSecret); err != nil {
		h.auditSecretSet(c, tenantID, connectorID.String(), "invalid")
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_webhook_secret", "message": err.Error()})
		return
	}

	set := h.setWebhookSecret
	if set == nil {
		set = h.connectorSvc.SetWebhookSecret
	}
	updatedAt, err := set(c.Request.Context(), tenantID, connectorID, req.WebhookSecret)
	if err != nil {
		switch {
		case errors.Is(err, services.ErrConnectorNotFound):
			h.auditSecretSet(c, tenantID, connectorID.String(), "not_found")
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "connector not found"})
		case errors.Is(err, services.ErrWebhookSecretInvalid):
			h.auditSecretSet(c, tenantID, connectorID.String(), "invalid")
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_webhook_secret", "message": err.Error()})
		case errors.Is(err, secretbox.ErrMissingKey), errors.Is(err, secretbox.ErrInvalidKey):
			h.auditSecretSet(c, tenantID, connectorID.String(), "unavailable")
			slog.Error("webhook secret not stored: secrets key unavailable",
				slog.String("tenant_id", tenantID.String()),
				slog.String("connector_id", connectorID.String()),
				slog.String("error", err.Error()))
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "secret_storage_unavailable", "message": "connector secrets cannot be stored right now"})
		default:
			h.auditSecretSet(c, tenantID, connectorID.String(), "error")
			slog.Error("webhook secret not stored",
				slog.String("tenant_id", tenantID.String()),
				slog.String("connector_id", connectorID.String()),
				slog.String("error", err.Error()))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "failed to store webhook secret"})
		}
		return
	}

	h.auditSecretSet(c, tenantID, connectorID.String(), "ok")
	c.JSON(http.StatusOK, gin.H{
		"connector_id":       connectorID.String(),
		"webhook_secret_set": true,
		"updated_at":         updatedAt.UTC().Format(time.RFC3339),
	})
}
