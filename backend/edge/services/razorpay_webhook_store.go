package services

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"zord-edge/db"
	"zord-edge/model"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

var (
	ErrWebhookPersist = errors.New("webhook receipt persist failed")
	ErrWebhookOutbox  = errors.New("webhook outbox insert failed")
)

type webhookPersistInput struct {
	TenantID     uuid.UUID
	ConnectorID  uuid.UUID
	Provider     string
	ProviderMode string
	EventID      string
	RawBody      []byte
	BodyHash     string
	Signature    string
	TraceID      string
	Metadata     model.WebhookMetadata
}

type webhookObservationStore interface {
	PersistWebhookObservation(ctx context.Context, in webhookPersistInput) (model.ReceiptResult, error)
}

func HashWebhookBody(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

type sqlWebhookStore struct{}

func (sqlWebhookStore) PersistWebhookObservation(ctx context.Context, in webhookPersistInput) (model.ReceiptResult, error) {
	if db.DB == nil {
		return model.ReceiptResult{}, fmt.Errorf("%w: database handle is nil", ErrWebhookPersist)
	}

	tx, err := db.DB.BeginTx(ctx, nil)
	if err != nil {
		return model.ReceiptResult{}, fmt.Errorf("%w: %v", ErrWebhookPersist, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	mode := in.ProviderMode
	if mode != "live" {
		mode = "test"
	}
	now := time.Now().UTC()

	var (
		existingID    uuid.UUID
		existingHash  string
		existingCount int
	)
	err = tx.QueryRowContext(ctx, `
		SELECT id, raw_body_hash, delivery_count
		FROM provider_webhook_receipts
		WHERE connector_id = $1 AND event_id = $2
		FOR UPDATE
	`, in.ConnectorID, in.EventID).Scan(&existingID, &existingHash, &existingCount)

	switch {
	case errors.Is(err, sql.ErrNoRows):
		result, err := insertReceiptAndOutbox(ctx, tx, in, mode, now)
		if err != nil {
			var pqErr *pq.Error
			if errors.As(err, &pqErr) && pqErr.Code == "23505" {
				if selErr := tx.QueryRowContext(ctx, `
					SELECT id, raw_body_hash, delivery_count
					FROM provider_webhook_receipts
					WHERE connector_id = $1 AND event_id = $2
					FOR UPDATE
				`, in.ConnectorID, in.EventID).Scan(&existingID, &existingHash, &existingCount); selErr != nil {
					return model.ReceiptResult{}, fmt.Errorf("%w: %v", ErrWebhookPersist, selErr)
				}
				return finishExistingReceipt(ctx, tx, existingID, existingHash, existingCount, in.BodyHash, now)
			}
			return model.ReceiptResult{}, err
		}
		if err = tx.Commit(); err != nil {
			return model.ReceiptResult{}, fmt.Errorf("%w: %v", ErrWebhookPersist, err)
		}
		committed = true
		return result, nil

	case err != nil:
		return model.ReceiptResult{}, fmt.Errorf("%w: %v", ErrWebhookPersist, err)
	}

	result, err := finishExistingReceipt(ctx, tx, existingID, existingHash, existingCount, in.BodyHash, now)
	if err != nil {
		return model.ReceiptResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return model.ReceiptResult{}, fmt.Errorf("%w: %v", ErrWebhookPersist, err)
	}
	committed = true
	return result, nil
}

func insertReceiptAndOutbox(ctx context.Context, tx *sql.Tx, in webhookPersistInput, mode string, now time.Time) (model.ReceiptResult, error) {
	receiptID := uuid.Must(uuid.NewV7())
	_, err := tx.ExecContext(ctx, `
		INSERT INTO provider_webhook_receipts (
			id, tenant_id, connector_id, provider, provider_mode,
			event_id, event_type, provider_entity_type, provider_entity_id,
			raw_body_hash, raw_body_size_bytes,
			signature_header, signature_valid,
			received_at, provider_created_at,
			ingestion_status, first_seen_trace_id, delivery_count,
			created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)
	`,
		receiptID, in.TenantID, in.ConnectorID, in.Provider, mode,
		in.EventID, in.Metadata.EventType, in.Metadata.EntityType, in.Metadata.EntityID,
		in.BodyHash, int64(len(in.RawBody)),
		in.Signature, true,
		now, in.Metadata.ProviderCreatedAt,
		model.WebhookStatusPersisted, in.TraceID, 1,
		now, now,
	)
	if err != nil {
		return model.ReceiptResult{}, fmt.Errorf("%w: %v", ErrWebhookPersist, err)
	}

	eventPayload := map[string]any{
		"event_name":           "provider.observation.received",
		"schema_version":       "v1",
		"tenant_id":            in.TenantID.String(),
		"connector_id":         in.ConnectorID.String(),
		"provider":             in.Provider,
		"provider_mode":        mode,
		"source_kind":          "webhook",
		"provider_event_id":    in.EventID,
		"provider_event_type":  in.Metadata.EventType,
		"provider_entity_type": in.Metadata.EntityType,
		"provider_entity_id":   in.Metadata.EntityID,
		"receipt_id":           receiptID.String(),
		"raw_body_hash":        in.BodyHash,
		"amount":               in.Metadata.AmountMinor,
		"currency":             in.Metadata.Currency,
		"status":               in.Metadata.Status,
		"order_id":             in.Metadata.OrderID,
		"captured":             in.Metadata.Captured,
		"fee":                  in.Metadata.FeeMinor,
		"tax":                  in.Metadata.TaxMinor,
		"received_at":          now.Format(time.RFC3339),
		"provider_created_at":  in.Metadata.ProviderCreatedAt,
		"trace_id":             in.TraceID,
	}
	addRouteReferences(eventPayload, in.Metadata)
	payloadJSON, _ := json.Marshal(eventPayload)

	_, err = tx.ExecContext(ctx, `
		INSERT INTO ingress_outbox (
			trace_id, envelope_id, tenant_id, artifact_id, artifact_version_id,
			object_ref, received_at, ingress_channel, source,
			idempotency_key, encrypted_payload, payload_hash,
			raw_row_hash, envelope_hash, envelope_signature,
			content_type, kms_key_version, encryption_key_id,
			topic, status, event_type, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,now(),now())
	`,
		in.TraceID,
		uuid.Must(uuid.NewV7()).String(),
		in.TenantID,
		uuid.Must(uuid.NewV7()).String(),
		uuid.Must(uuid.NewV7()).String(),
		"",
		now,
		"webhook:razorpay:"+in.ConnectorID.String(),
		"provider.observation.received",
		in.EventID,
		payloadJSON,
		in.BodyHash,
		in.BodyHash,
		in.BodyHash,
		"",
		"application/json",
		"",
		"",
		"payments.ledger.events.v1",
		"PENDING",
		"provider.observation.received",
	)
	if err != nil {
		return model.ReceiptResult{}, fmt.Errorf("%w: %v", ErrWebhookOutbox, err)
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE provider_webhook_receipts
		SET ingestion_status = $1, published_at = $2, updated_at = $2
		WHERE id = $3
	`, model.WebhookStatusPublished, now, receiptID)
	if err != nil {
		return model.ReceiptResult{}, fmt.Errorf("%w: %v", ErrWebhookPersist, err)
	}

	return model.ReceiptResult{
		ReceiptID:     receiptID,
		Status:        model.WebhookStatusPublished,
		Published:     true,
		DeliveryCount: 1,
	}, nil
}

func finishExistingReceipt(ctx context.Context, tx *sql.Tx, id uuid.UUID, storedHash string, count int, bodyHash string, now time.Time) (model.ReceiptResult, error) {
	if storedHash != bodyHash {
		_, err := tx.ExecContext(ctx, `
			UPDATE provider_webhook_receipts
			SET last_seen_at = $1, updated_at = $1
			WHERE id = $2
		`, now, id)
		if err != nil {
			return model.ReceiptResult{}, fmt.Errorf("%w: %v", ErrWebhookPersist, err)
		}
		return model.ReceiptResult{
			ReceiptID:     id,
			Status:        model.WebhookStatusPayloadConflict,
			Conflict:      true,
			DeliveryCount: count,
		}, nil
	}

	var nextCount int
	err := tx.QueryRowContext(ctx, `
		UPDATE provider_webhook_receipts
		SET delivery_count = delivery_count + 1,
		    last_seen_at = $1,
		    updated_at = $1
		WHERE id = $2
		RETURNING delivery_count
	`, now, id).Scan(&nextCount)
	if err != nil {
		return model.ReceiptResult{}, fmt.Errorf("%w: %v", ErrWebhookPersist, err)
	}

	return model.ReceiptResult{
		ReceiptID:     id,
		Status:        model.WebhookStatusDuplicate,
		Duplicate:     true,
		DeliveryCount: nextCount,
	}, nil
}

type memoryReceipt struct {
	ID            uuid.UUID
	TenantID      uuid.UUID
	ConnectorID   uuid.UUID
	EventID       string
	BodyHash      string
	DeliveryCount int
	Status        string
}

type MemoryOutboxEvent struct {
	EventID   string
	Connector uuid.UUID
	ReceiptID uuid.UUID
	BodyHash  string
	EventType string
}

// MemoryWebhookStore is an in-process observation store for unit tests.
type MemoryWebhookStore struct {
	mu          sync.Mutex
	receipts    map[string]memoryReceipt
	Outbox      []MemoryOutboxEvent
	FailPersist bool
	FailOutbox  bool
}

func NewMemoryWebhookStore() *MemoryWebhookStore {
	return &MemoryWebhookStore{receipts: map[string]memoryReceipt{}}
}

func memoryKey(connectorID uuid.UUID, eventID string) string {
	return connectorID.String() + "|" + eventID
}

func (m *MemoryWebhookStore) PersistWebhookObservation(_ context.Context, in webhookPersistInput) (model.ReceiptResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.FailPersist {
		return model.ReceiptResult{}, ErrWebhookPersist
	}
	if m.receipts == nil {
		m.receipts = map[string]memoryReceipt{}
	}

	key := memoryKey(in.ConnectorID, in.EventID)
	if existing, ok := m.receipts[key]; ok {
		if existing.BodyHash != in.BodyHash {
			return model.ReceiptResult{
				ReceiptID:     existing.ID,
				Status:        model.WebhookStatusPayloadConflict,
				Conflict:      true,
				DeliveryCount: existing.DeliveryCount,
			}, nil
		}
		existing.DeliveryCount++
		existing.Status = model.WebhookStatusDuplicate
		m.receipts[key] = existing
		return model.ReceiptResult{
			ReceiptID:     existing.ID,
			Status:        model.WebhookStatusDuplicate,
			Duplicate:     true,
			DeliveryCount: existing.DeliveryCount,
		}, nil
	}

	if m.FailOutbox {
		return model.ReceiptResult{}, ErrWebhookOutbox
	}

	id := uuid.Must(uuid.NewV7())
	m.receipts[key] = memoryReceipt{
		ID:            id,
		TenantID:      in.TenantID,
		ConnectorID:   in.ConnectorID,
		EventID:       in.EventID,
		BodyHash:      in.BodyHash,
		DeliveryCount: 1,
		Status:        model.WebhookStatusPublished,
	}
	m.Outbox = append(m.Outbox, MemoryOutboxEvent{
		EventID:   in.EventID,
		Connector: in.ConnectorID,
		ReceiptID: id,
		BodyHash:  in.BodyHash,
		EventType: in.Metadata.EventType,
	})
	return model.ReceiptResult{
		ReceiptID:     id,
		Status:        model.WebhookStatusPublished,
		Published:     true,
		DeliveryCount: 1,
	}, nil
}

func (m *MemoryWebhookStore) Receipt(connectorID uuid.UUID, eventID string) (memoryReceipt, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.receipts[memoryKey(connectorID, eventID)]
	return rec, ok
}

func (m *MemoryWebhookStore) ReceiptCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.receipts)
}

func (m *MemoryWebhookStore) OutboxCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.Outbox)
}

// addRouteReferences adds Route (transfer / reversal) reference ids to the
// provider.observation.received payload. Keys are only written when Razorpay
// supplied the value, so payment/refund/payout payloads are unchanged.
// provider_entity_type is already set from Metadata.EntityType ("transfer" /
// "reversal" for Route entities) — recon observe classifies on it only.
func addRouteReferences(p map[string]any, m model.WebhookMetadata) {
	set := func(k, v string) {
		if v != "" {
			p[k] = v
		}
	}
	set("transfer_id", m.TransferID)
	set("reverse_transfer_id", m.ReverseTransferID)
	set("payment_id", m.PaymentID)
	set("seller_id", m.SellerID)
}
