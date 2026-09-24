package observe

import (
	"encoding/json"
	"strings"
	"time"
)

const (
	EventObservationReceived = "provider.observation.received"
	SourceWebhook            = "webhook"
)

// Envelope is the provider observation emitted by zord-edge after webhook receipt.
// It is not a canonical payment and must not contain secrets or full PSP payloads.
type Envelope struct {
	EventName          string     `json:"event_name"`
	EventType          string     `json:"event_type"`
	SchemaVersion      string     `json:"schema_version"`
	TenantID           string     `json:"tenant_id"`
	ConnectorID        string     `json:"connector_id"`
	Provider           string     `json:"provider"`
	ProviderMode       string     `json:"provider_mode"`
	SourceKind         string     `json:"source_kind"`
	ProviderEventID    string     `json:"provider_event_id"`
	ProviderEventType  string     `json:"provider_event_type"`
	ProviderEntityType string     `json:"provider_entity_type"`
	ProviderEntityID   string     `json:"provider_entity_id"`
	ReceiptID          string     `json:"receipt_id"`
	RawBodyHash        string     `json:"raw_body_hash"`
	Amount             int64      `json:"amount"`
	Currency           string     `json:"currency"`
	Status             string     `json:"status"`
	OrderID            string     `json:"order_id"`
	Captured           bool       `json:"captured"`
	Fee                int64      `json:"fee"`
	Tax                int64      `json:"tax"`
	ReceivedAt         string     `json:"received_at"`
	ProviderCreatedAt  *time.Time `json:"provider_created_at"`
	TraceID            string     `json:"trace_id"`
	PaymentID          string     `json:"payment_id"`
	// SellerID is optional; set only when Connectors/Edge supply it. Never invent.
	SellerID           string     `json:"seller_id,omitempty"`
}

func ParseEnvelope(raw []byte) (Envelope, error) {
	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return Envelope{}, err
	}
	return env, nil
}

func (e Envelope) ObservationName() string {
	if strings.TrimSpace(e.EventName) != "" {
		return e.EventName
	}
	return e.EventType
}

func (e Envelope) IsProviderObservation() bool {
	return e.ObservationName() == EventObservationReceived
}
