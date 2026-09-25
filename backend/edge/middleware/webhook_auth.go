package middleware

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"zord-edge/db"
	"zord-edge/internal/tenantsecret"
	"zord-edge/logger"

	"github.com/gin-gonic/gin"
)

// ConnectorBinding represents a registered connector from DB
type ConnectorBinding struct {
	TenantID    string
	ConnectorID string
	Provider    string
	Secret      string
}

// LookupConnectorBinding fetches connector from DB
func LookupConnectorBinding(provider, connectorID string) (*ConnectorBinding, error) {
	var binding ConnectorBinding

	query := `
		SELECT tenant_id, connector_id, provider, secret
		FROM connectors
		WHERE provider = $1
		  AND connector_id = $2
		  AND active = true
		LIMIT 1
	`

	var stored sql.NullString
	err := db.DB.QueryRow(query, provider, connectorID).Scan(
		&binding.TenantID,
		&binding.ConnectorID,
		&binding.Provider,
		&stored,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("connector not found")
		}
		return nil, err
	}
	// D32: connectors.secret is stored encrypted (enc:v1:). Decrypt; a missing
	// secret, missing key or legacy plaintext fails closed (no global fallback).
	plain, err := tenantsecret.ResolveWebhookSecret(stored.String, "")
	if err != nil {
		return nil, err
	}
	binding.Secret = plain
	return &binding, nil
}

func VerifyWebhookSignature() gin.HandlerFunc {
	return func(c *gin.Context) {
		provider := c.Param("provider")
		connectorID := c.Param("connectorID")

		if provider == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Missing provider in path"})
			c.Abort()
			return
		}

		if connectorID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing connectorID in path"})
			c.Abort()
			return
		}
		contentType := c.GetHeader("Content-Type")
		if !strings.HasPrefix(strings.ToLower(contentType), "application/json") {
			c.JSON(http.StatusUnsupportedMediaType, gin.H{"error": "Content-Type must be application/json"})
			c.Abort()
			return
		}

		// Lookup connector
		binding, err := LookupConnectorBinding(provider, connectorID)
		if err != nil {
			logger.Log.Error("webhook connector lookup failed",
				slog.String("provider", provider),
				slog.String("connector_id", connectorID),
				slog.String("error", err.Error()))
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			c.Abort()
			return
		}

		signatureHeader := c.GetHeader("X-PSP-Signature")
		timestampHeader := c.GetHeader("X-PSP-Timestamp")
		eventID := c.GetHeader("X-PSP-Event-Id")

		if signatureHeader == "" || timestampHeader == "" || eventID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Missing required PSP headers"})
			c.Abort()
			return
		}

		ts, err := strconv.ParseInt(timestampHeader, 10, 64)
		diff := time.Now().Unix() - ts
		if err != nil || diff > 300 || diff < -300 {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "timestamp out of range"})
			c.Abort()
			return
		}

		sigTimestamp, sigV1, err := parsePSPSignature(signatureHeader)
		if err != nil || sigTimestamp != timestampHeader {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature format"})
			c.Abort()
			return
		}

		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read request body"})
			c.Abort()
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewBuffer(body))
		if !verifySignatureV1(body, binding.Secret, timestampHeader, sigV1) {
			logger.Log.Warn("webhook signature mismatch",
				slog.String("provider", provider),
				slog.String("connector_id", connectorID),
				slog.String("event_id", eventID))
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid signature"})
			c.Abort()
			return
		}

		// Set verified context
		c.Set("tenant_id", binding.TenantID)
		c.Set("connector_id", binding.ConnectorID)
		c.Set("psp_provider", provider)
		c.Set("psp_event_id", eventID)
		c.Set("raw_payload", body)

		c.Next()
	}
}

func parsePSPSignature(header string) (timestamp string, v1 string, err error) {
	parts := strings.Split(header, ",")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if strings.HasPrefix(p, "t=") {
			timestamp = strings.TrimPrefix(p, "t=")
		}
		if strings.HasPrefix(p, "v1=") {
			v1 = strings.TrimPrefix(p, "v1=")
		}
	}
	if timestamp == "" || v1 == "" {
		return "", "", errors.New("missing t or v1")
	}
	if _, err := strconv.ParseInt(timestamp, 10, 64); err != nil {
		return "", "", err
	}
	if _, err := hex.DecodeString(v1); err != nil {
		return "", "", err
	}
	return timestamp, strings.ToLower(v1), nil
}

func verifySignatureV1(payload []byte, secret, timestamp, signatureV1 string) bool {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(timestamp))
	h.Write([]byte("."))
	h.Write(payload)
	expected := hex.EncodeToString(h.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(strings.ToLower(signatureV1)))
}
