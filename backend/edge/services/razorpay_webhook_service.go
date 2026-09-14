package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"zord-edge/db"
	"zord-edge/logger"
	"zord-edge/model"
	"zord-edge/validator"

	"github.com/google/uuid"
)

// RazorpayWebhookService handles Razorpay webhook processing.
type RazorpayWebhookService struct {
	store webhookObservationStore
}

// NewRazorpayWebhookService creates a service that persists to Postgres.
func NewRazorpayWebhookService() *RazorpayWebhookService {
	return &RazorpayWebhookService{store: sqlWebhookStore{}}
}

// NewRazorpayWebhookServiceWithStore creates a service with an injected store (tests).
func NewRazorpayWebhookServiceWithStore(store webhookObservationStore) *RazorpayWebhookService {
	if store == nil {
		store = sqlWebhookStore{}
	}
	return &RazorpayWebhookService{store: store}
}

// WebhookRequest holds the parsed input from the HTTP handler.
type WebhookRequest struct {
	TenantID      uuid.UUID
	ConnectorID   uuid.UUID
	Provider      string
	ProviderMode  string
	RawBody       []byte
	EventID       string
	Signature     string
	WebhookSecret string
	TraceID       string
}

// Receive processes a Razorpay webhook as a durable observation.
// Order: HMAC on raw bytes, then JSON metadata, then idempotent persist.
func (s *RazorpayWebhookService) Receive(ctx context.Context, req WebhookRequest) (model.ReceiptResult, int, error) {
	start := time.Now()
	provider := metricProvider(req.Provider)
	mode := metricMode(req.ProviderMode)
	status := "error"
	eventType := "unknown"

	razorpayWebhookReceivedTotal.WithLabelValues(provider, mode).Inc()
	defer func() {
		razorpayWebhookProcessingDuration.WithLabelValues(provider, mode, status).Observe(time.Since(start).Seconds())
	}()

	if err := validator.VerifyRazorpaySignature(req.RawBody, req.Signature, req.WebhookSecret); err != nil {
		status = "invalid_signature"
		razorpayWebhookRejectedTotal.WithLabelValues(provider, mode, status).Inc()
		logger.Log.Warn("razorpay webhook: invalid signature",
			slog.String("event_id", req.EventID),
			slog.String("provider", provider),
			slog.String("connector_id", req.ConnectorID.String()),
			slog.String("trace_id", req.TraceID),
		)
		return model.ReceiptResult{}, 401, fmt.Errorf("invalid signature")
	}

	metadata, err := s.ParseMetadata(req.RawBody)
	if err != nil {
		status = "malformed_payload"
		razorpayWebhookRejectedTotal.WithLabelValues(provider, mode, status).Inc()
		logger.Log.Warn("razorpay webhook: failed to parse event metadata",
			slog.String("event_id", req.EventID),
			slog.String("error", err.Error()),
			slog.String("trace_id", req.TraceID),
		)
		return model.ReceiptResult{}, 400, fmt.Errorf("invalid event payload")
	}
	eventType = metricEventType(metadata.EventType)

	bodyHash := HashWebhookBody(req.RawBody)
	result, err := s.store.PersistWebhookObservation(ctx, webhookPersistInput{
		TenantID:     req.TenantID,
		ConnectorID:  req.ConnectorID,
		Provider:     provider,
		ProviderMode: mode,
		EventID:      req.EventID,
		RawBody:      req.RawBody,
		BodyHash:     bodyHash,
		Signature:    req.Signature,
		TraceID:      req.TraceID,
		Metadata:     metadata,
	})
	if err != nil {
		if errors.Is(err, ErrWebhookOutbox) {
			status = "outbox_failure"
			razorpayWebhookOutboxFailureTotal.WithLabelValues(provider, mode).Inc()
		} else {
			status = "persist_failure"
			razorpayWebhookPersistFailureTotal.WithLabelValues(provider, mode).Inc()
		}
		razorpayWebhookRejectedTotal.WithLabelValues(provider, mode, status).Inc()
		logger.Log.Error("razorpay webhook: persist failed",
			slog.String("event_id", req.EventID),
			slog.String("trace_id", req.TraceID),
			slog.String("error", err.Error()),
		)
		return model.ReceiptResult{}, 500, err
	}

	if result.Conflict {
		status = "payload_conflict"
		razorpayWebhookRejectedTotal.WithLabelValues(provider, mode, status).Inc()
		logger.Log.Warn("razorpay webhook: payload conflict",
			slog.String("event_id", req.EventID),
			slog.String("receipt_id", result.ReceiptID.String()),
			slog.String("body_sha256", bodyHash),
			slog.String("trace_id", req.TraceID),
		)
		return result, 200, nil
	}

	if result.Duplicate {
		status = "duplicate"
		razorpayWebhookDuplicateTotal.WithLabelValues(provider, mode, eventType).Inc()
		logger.Log.Info("razorpay webhook: duplicate accepted",
			slog.String("provider", provider),
			slog.String("event_type", metadata.EventType),
			slog.String("event_id", req.EventID),
			slog.String("connector_id", req.ConnectorID.String()),
			slog.String("body_sha256", bodyHash),
			slog.String("status", "duplicate"),
			slog.String("receipt_id", result.ReceiptID.String()),
			slog.Int("delivery_count", result.DeliveryCount),
			slog.String("trace_id", req.TraceID),
		)
		return result, 200, nil
	}

	status = "accepted"
	razorpayWebhookAcceptedTotal.WithLabelValues(provider, mode, eventType).Inc()
	logger.Log.Info("razorpay webhook: accepted",
		slog.String("provider", provider),
		slog.String("event_type", metadata.EventType),
		slog.String("event_id", req.EventID),
		slog.String("connector_id", req.ConnectorID.String()),
		slog.String("entity_id", metadata.EntityID),
		slog.String("body_sha256", bodyHash),
		slog.String("status", "accepted"),
		slog.String("receipt_id", result.ReceiptID.String()),
		slog.String("trace_id", req.TraceID),
	)
	return result, 200, nil
}

// ParseMetadata extracts event type, entity type, entity ID, and created_at from the raw body.
func (s *RazorpayWebhookService) ParseMetadata(rawBody []byte) (model.WebhookMetadata, error) {
	var event model.RazorpayWebhookEvent
	if err := json.Unmarshal(rawBody, &event); err != nil {
		return model.WebhookMetadata{}, fmt.Errorf("failed to parse Razorpay event: %w", err)
	}

	meta := model.WebhookMetadata{
		EventType: event.Event,
	}

	if event.CreatedAt > 0 {
		t := time.Unix(event.CreatedAt, 0).UTC()
		meta.ProviderCreatedAt = &t
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(event.Payload, &payload); err == nil {
		keys := make([]string, 0, len(payload))
		if _, ok := payload["payment"]; ok {
			keys = append(keys, "payment")
		}
		for entityType := range payload {
			if entityType != "payment" {
				keys = append(keys, entityType)
			}
		}
		for _, entityType := range keys {
			entityRaw := payload[entityType]
			var wrapper struct {
				Entity struct {
					ID       string `json:"id"`
					Entity   string `json:"entity"`
					Amount   int64  `json:"amount"`
					Currency string `json:"currency"`
					Status   string `json:"status"`
					OrderID  string `json:"order_id"`
					Captured bool   `json:"captured"`
					Fee      int64  `json:"fee"`
					Tax      int64  `json:"tax"`
				} `json:"entity"`
				ID string `json:"id"`
			}
			if err := json.Unmarshal(entityRaw, &wrapper); err != nil {
				continue
			}
			id := wrapper.Entity.ID
			if id == "" {
				id = wrapper.ID
			}
			if id == "" {
				continue
			}
			meta.EntityType = entityType
			meta.EntityID = id
			meta.AmountMinor = wrapper.Entity.Amount
			meta.Currency = wrapper.Entity.Currency
			meta.Status = wrapper.Entity.Status
			meta.OrderID = wrapper.Entity.OrderID
			meta.Captured = wrapper.Entity.Captured
			meta.FeeMinor = wrapper.Entity.Fee
			meta.TaxMinor = wrapper.Entity.Tax
			break
		}
	}

	return meta, nil
}

// GetReceipt retrieves a receipt by ID.
func (s *RazorpayWebhookService) GetReceipt(ctx context.Context, receiptID uuid.UUID) (*model.ProviderWebhookReceipt, error) {
	var r model.ProviderWebhookReceipt
	err := db.DB.QueryRowContext(ctx, `
		SELECT id, tenant_id, connector_id, provider, provider_mode,
		       event_id, event_type, provider_entity_type, provider_entity_id,
		       raw_body_hash, raw_body_size_bytes,
		       signature_valid, received_at, ingestion_status,
		       delivery_count, created_at
		FROM provider_webhook_receipts
		WHERE id = $1
	`, receiptID).Scan(
		&r.ID, &r.TenantID, &r.ConnectorID, &r.Provider, &r.ProviderMode,
		&r.EventID, &r.EventType, &r.ProviderEntityType, &r.ProviderEntityID,
		&r.RawBodyHash, &r.RawBodySizeBytes,
		&r.SignatureValid, &r.ReceivedAt, &r.IngestionStatus,
		&r.DeliveryCount, &r.CreatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, errors.New("receipt not found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get receipt: %w", err)
	}
	return &r, nil
}

// ListReceiptsByConnector returns receipts for a connector.
func (s *RazorpayWebhookService) ListReceiptsByConnector(tenantID, connectorID uuid.UUID, limit int) ([]model.ProviderWebhookReceipt, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := db.DB.Query(`
		SELECT id, tenant_id, connector_id, provider, provider_mode,
		       event_id, event_type, provider_entity_type, provider_entity_id,
		       raw_body_hash, raw_body_size_bytes,
		       signature_valid, received_at, ingestion_status,
		       delivery_count, created_at
		FROM provider_webhook_receipts
		WHERE tenant_id = $1 AND connector_id = $2
		ORDER BY received_at DESC
		LIMIT $3
	`, tenantID, connectorID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var receipts []model.ProviderWebhookReceipt
	for rows.Next() {
		var r model.ProviderWebhookReceipt
		if err := rows.Scan(
			&r.ID, &r.TenantID, &r.ConnectorID, &r.Provider, &r.ProviderMode,
			&r.EventID, &r.EventType, &r.ProviderEntityType, &r.ProviderEntityID,
			&r.RawBodyHash, &r.RawBodySizeBytes,
			&r.SignatureValid, &r.ReceivedAt, &r.IngestionStatus,
			&r.DeliveryCount, &r.CreatedAt,
		); err != nil {
			return nil, err
		}
		receipts = append(receipts, r)
	}
	return receipts, nil
}

type ReceiptIndexRow struct {
	ProviderEntityID string    `json:"provider_entity_id"`
	EventID          string    `json:"event_id"`
	EventType        string    `json:"event_type"`
	ReceivedAt       time.Time `json:"received_at"`
	RawBodyHash      string    `json:"raw_body_hash"`
}

func (s *RazorpayWebhookService) IndexReceipts(ctx context.Context, tenantID, connectorID uuid.UUID, from, to time.Time) ([]ReceiptIndexRow, error) {
	rows, err := db.DB.QueryContext(ctx, `
		SELECT COALESCE(provider_entity_id,''), event_id, COALESCE(event_type,''), received_at, raw_body_hash
		FROM provider_webhook_receipts
		WHERE tenant_id = $1 AND connector_id = $2
		  AND (
		    (received_at >= $3 AND received_at < $4)
		    OR (provider_created_at >= $3 AND provider_created_at < $4)
		  )
		  AND provider_entity_id IS NOT NULL AND provider_entity_id <> ''
	`, tenantID, connectorID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReceiptIndexRow
	for rows.Next() {
		var r ReceiptIndexRow
		if err := rows.Scan(&r.ProviderEntityID, &r.EventID, &r.EventType, &r.ReceivedAt, &r.RawBodyHash); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
