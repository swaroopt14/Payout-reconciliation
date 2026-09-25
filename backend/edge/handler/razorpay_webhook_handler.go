package handler

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"zord-edge/db"
	"zord-edge/internal/secretbox"
	"zord-edge/internal/tenantsecret"
	"zord-edge/logger"
	"zord-edge/model"
	"zord-edge/services"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const maxWebhookBodyBytes = 1 << 20 // 1 MB

// HandleRazorpayWebhook processes incoming Razorpay webhook events.
//
// The handler only extracts HTTP fields. Signature verification, metadata
// parse, and durable persist live in RazorpayWebhookService.
//
// POST /v1/webhooks/razorpay/:connectorID
func (h *Handler) HandleRazorpayWebhook(c *gin.Context) {
	connectorIDStr := c.Param("connectorID")
	if connectorIDStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing connector ID"})
		return
	}

	connectorID, err := uuid.Parse(connectorIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid connector ID"})
		return
	}

	rawBody, err := io.ReadAll(io.LimitReader(c.Request.Body, maxWebhookBodyBytes+1))
	if err != nil {
		logger.Log.Error("razorpay webhook: failed to read body",
			slog.String("connector_id", connectorIDStr),
			slog.String("error", err.Error()),
		)
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "body too large"})
		return
	}
	if int64(len(rawBody)) > maxWebhookBodyBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "body exceeds 1MB limit"})
		return
	}

	eventID := c.GetHeader("x-razorpay-event-id")
	if eventID == "" {
		eventID = c.GetHeader("X-Razorpay-Event-Id")
	}
	if eventID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing x-razorpay-event-id header"})
		return
	}

	signature := c.GetHeader("X-Razorpay-Signature")
	if signature == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing X-Razorpay-Signature header"})
		return
	}

	tenantID, providerMode, webhookSecret, err := h.lookupRazorpayConnector(connectorID)
	if err == nil && webhookSecret == "" {
		err = tenantsecret.ErrNoTenantSecret
	}
	if err != nil {
		status, code := secretLookupStatus(err)
		if code == "" {
			c.JSON(http.StatusNotFound, gin.H{"error": "connector not found"})
			return
		}
		// Error text names the failure class only; secret values are never logged.
		logger.Log.Warn("razorpay webhook: rejected, tenant secret unavailable",
			slog.String("event_id", eventID),
			slog.String("connector_id", connectorIDStr),
			slog.String("tenant_id", tenantID.String()),
			slog.Int("http_status", status),
			slog.String("error", err.Error()),
		)
		c.JSON(status, gin.H{"error": gin.H{"code": code, "message": "webhook cannot be verified for this connector"}})
		return
	}

	traceID := uuid.Must(uuid.NewV7()).String()
	hash := sha256.Sum256(rawBody)
	bodyHash := hex.EncodeToString(hash[:])
	hashPrefix := bodyHash
	if len(hashPrefix) > 16 {
		hashPrefix = hashPrefix[:16]
	}

	logger.Log.Info("razorpay webhook: received",
		slog.String("event_id", eventID),
		slog.String("connector_id", connectorIDStr),
		slog.String("tenant_id", tenantID.String()),
		slog.Int("body_size", len(rawBody)),
		slog.String("body_hash", hashPrefix+"..."),
		slog.String("trace_id", traceID),
	)

	receive := h.ReceiveRazorpayWebhook
	if receive == nil {
		receive = services.NewRazorpayWebhookService().Receive
	}
	result, httpStatus, err := receive(c.Request.Context(), services.WebhookRequest{
		TenantID:      tenantID,
		ConnectorID:   connectorID,
		Provider:      "razorpay",
		ProviderMode:  providerMode,
		RawBody:       rawBody,
		EventID:       eventID,
		Signature:     signature,
		WebhookSecret: webhookSecret,
		TraceID:       traceID,
	})

	if err != nil {
		logger.Log.Warn("razorpay webhook: rejected",
			slog.String("event_id", eventID),
			slog.Int("http_status", httpStatus),
			slog.String("error", err.Error()),
		)
		switch httpStatus {
		case http.StatusUnauthorized:
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{
					"code":    "INVALID_WEBHOOK_SIGNATURE",
					"message": "Webhook signature verification failed",
				},
			})
		case http.StatusBadRequest:
			c.JSON(http.StatusBadRequest, gin.H{
				"error": gin.H{
					"code":    "INVALID_WEBHOOK_PAYLOAD",
					"message": "Webhook payload is invalid",
				},
			})
		default:
			if httpStatus < 400 {
				httpStatus = http.StatusInternalServerError
			}
			c.JSON(httpStatus, gin.H{
				"error":   "webhook_error",
				"message": "event not accepted",
			})
		}
		return
	}

	status := "accepted"
	switch {
	case result.Conflict:
		status = "payload_conflict"
	case result.Duplicate:
		status = "duplicate"
	case result.Status == model.WebhookStatusPayloadConflict:
		status = "payload_conflict"
	case result.Status == model.WebhookStatusDuplicate:
		status = "duplicate"
	}

	response := gin.H{
		"status":     status,
		"receipt_id": result.ReceiptID.String(),
		"trace_id":   traceID,
	}
	if result.Duplicate || result.Conflict {
		response["delivery_count"] = result.DeliveryCount
	}

	c.JSON(http.StatusOK, response)
}

// HandleRazorpayWebhookStatus returns receipt status.
// GET /v1/webhooks/razorpay/receipt/:receiptID
func (h *Handler) HandleRazorpayWebhookStatus(c *gin.Context) {
	tenantIDStr := strings.TrimSpace(c.GetString("tenant_id"))
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	receiptIDStr := c.Param("receiptID")
	receiptID, err := uuid.Parse(receiptIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid receipt ID"})
		return
	}

	svc := services.NewRazorpayWebhookService()
	receipt, err := svc.GetReceipt(c.Request.Context(), receiptID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			c.JSON(http.StatusNotFound, gin.H{"error": "receipt not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		}
		return
	}
	if receipt.TenantID != tenantID {
		c.JSON(http.StatusNotFound, gin.H{"error": "receipt not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"receipt_id":           receipt.ID.String(),
		"provider":             receipt.Provider,
		"provider_mode":        receipt.ProviderMode,
		"event_id":             receipt.EventID,
		"event_type":           receipt.EventType,
		"provider_entity_type": receipt.ProviderEntityType,
		"provider_entity_id":   receipt.ProviderEntityID,
		"raw_body_hash":        receipt.RawBodyHash,
		"signature_valid":      receipt.SignatureValid,
		"ingestion_status":     receipt.IngestionStatus,
		"delivery_count":       receipt.DeliveryCount,
		"received_at":          receipt.ReceivedAt,
	})
}

// HandleRazorpayWebhookList returns recent receipts for a connector.
// GET /v1/webhooks/razorpay/receipts/:connectorID
func (h *Handler) HandleRazorpayWebhookList(c *gin.Context) {
	tenantIDStr := strings.TrimSpace(c.GetString("tenant_id"))
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	connectorIDStr := c.Param("connectorID")
	connectorID, err := uuid.Parse(connectorIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid connector ID"})
		return
	}

	svc := services.NewRazorpayWebhookService()
	receipts, err := svc.ListReceiptsByConnector(tenantID, connectorID, 50)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"connector_id": connectorIDStr,
		"count":        len(receipts),
		"receipts":     receipts,
	})
}

func (h *Handler) lookupRazorpayConnector(connectorID uuid.UUID) (uuid.UUID, string, string, error) {
	if h.LookupRazorpayConnector != nil {
		return h.LookupRazorpayConnector(connectorID)
	}
	return lookupRazorpayConnectorSQL(connectorID)
}

func lookupRazorpayConnectorSQL(connectorID uuid.UUID) (uuid.UUID, string, string, error) {
	var (
		tenantID  uuid.UUID
		mode      sql.NullString
		secret    sql.NullString
		secretRef sql.NullString
	)
	err := db.DB.QueryRow(
		`SELECT tenant_id, provider_mode, secret, webhook_secret_ref
		 FROM connectors WHERE id = $1 AND active = true`,
		connectorID,
	).Scan(&tenantID, &mode, &secret, &secretRef)
	if err != nil {
		return uuid.Nil, "", "", err
	}

	providerMode := mode.String
	if providerMode == "" {
		providerMode = "test"
	}

	// D32: the tenant's own secret only. connectors.secret is enc:v1:
	// (decrypted here), else a per-tenant env ref. No RAZORPAY_* fallback.
	webhookSecret, err := tenantsecret.ResolveWebhookSecret(secret.String, secretRef.String)
	if err != nil {
		return tenantID, providerMode, "", err
	}
	return tenantID, providerMode, webhookSecret, nil
}

// secretLookupStatus maps a secret-resolution failure to an HTTP status.
// No tenant secret (or a shared ref) is 401: the webhook is not verifiable
// and is rejected without processing. A missing/invalid encryption key or a
// corrupt stored value is a server misconfiguration: 503, so Razorpay retries
// once an operator fixes it, and nothing is processed meanwhile.
func secretLookupStatus(err error) (int, string) {
	switch {
	case errors.Is(err, tenantsecret.ErrNoTenantSecret), errors.Is(err, tenantsecret.ErrSharedSecretRef):
		return http.StatusUnauthorized, "NO_TENANT_WEBHOOK_SECRET"
	case errors.Is(err, secretbox.ErrMissingKey), errors.Is(err, secretbox.ErrInvalidKey),
		errors.Is(err, secretbox.ErrLegacyPlaintext), errors.Is(err, secretbox.ErrDecrypt),
		errors.Is(err, secretbox.ErrMalformed):
		return http.StatusServiceUnavailable, "WEBHOOK_SECRET_UNAVAILABLE"
	}
	return http.StatusNotFound, ""
}

// HandleWebhookReceiptIndex is GET /internal/webhooks/receipts/index
func (h *Handler) HandleWebhookReceiptIndex(c *gin.Context) {
	if !authorizeRelay(c.Request) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	tenantID, err := uuid.Parse(strings.TrimSpace(c.Query("tenant_id")))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid tenant_id"})
		return
	}
	ctxTenant := strings.TrimSpace(c.GetHeader("X-Relay-Tenant-ID"))
	if ctxTenant == "" || !strings.EqualFold(ctxTenant, tenantID.String()) {
		c.JSON(http.StatusForbidden, gin.H{"error": "tenant_mismatch"})
		return
	}
	connectorID, err := uuid.Parse(strings.TrimSpace(c.Query("connector_id")))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid connector_id"})
		return
	}
	from, err := time.Parse(time.RFC3339, strings.TrimSpace(c.Query("from")))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid from"})
		return
	}
	to, err := time.Parse(time.RFC3339, strings.TrimSpace(c.Query("to")))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid to"})
		return
	}
	if !to.After(from) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "to must be after from"})
		return
	}
	svc := services.NewRazorpayWebhookService()
	rows, err := svc.IndexReceipts(c.Request.Context(), tenantID, connectorID, from.UTC(), to.UTC())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	if rows == nil {
		rows = []services.ReceiptIndexRow{}
	}
	c.JSON(http.StatusOK, gin.H{
		"tenant_id":    tenantID.String(),
		"connector_id": connectorID.String(),
		"count":        len(rows),
		"receipts":     rows,
	})
}
