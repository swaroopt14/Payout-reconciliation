package model

import (
	"time"

	"github.com/google/uuid"
)

// ConnectorStatus represents the current state of a connector.
type ConnectorStatus string

const (
	ConnectorStatusActive   ConnectorStatus = "active"
	ConnectorStatusDisabled ConnectorStatus = "disabled"
	ConnectorStatusError    ConnectorStatus = "error"
	ConnectorStatusPending  ConnectorStatus = "pending"
)

// Connector represents a payment provider connection for a tenant.
type Connector struct {
	ID                  uuid.UUID     `json:"id" db:"id"`
	TenantID            uuid.UUID     `json:"tenant_id" db:"tenant_id"`
	Provider            string        `json:"provider" db:"provider"`
	ConnectorID         string        `json:"connector_id" db:"connector_id"`
	SecretRef           *string       `json:"secret_ref,omitempty" db:"secret_ref"`
	Secret              *string       `json:"-" db:"secret"` // never serialized to JSON
	Active              bool          `json:"active" db:"active"`
	ProviderMode        string        `json:"provider_mode" db:"provider_mode"`
	ApiKeyRef           *string       `json:"api_key_ref,omitempty" db:"api_key_ref"`
	ApiSecretRef        *string       `json:"-" db:"api_secret_ref"` // enc:v1: sealed tenant key secret; never serialized
	WebhookSecretRef    *string       `json:"webhook_secret_ref,omitempty" db:"webhook_secret_ref"`
	ProviderAccountID   *string       `json:"provider_account_id,omitempty" db:"provider_account_id"`
	LastHealthCheckAt   *time.Time    `json:"last_health_check_at,omitempty" db:"last_health_check_at"`
	LastHealthStatus    *string       `json:"last_health_status,omitempty" db:"last_health_status"`
	LastHealthErrorCode *string       `json:"last_health_error_code,omitempty" db:"last_health_error_code"`
	CreatedAt           time.Time     `json:"created_at" db:"created_at"`
	UpdatedAt           time.Time     `json:"updated_at" db:"updated_at"`
}

// ConnectorCreateRequest is the API request to create a Razorpay connector.
type ConnectorCreateRequest struct {
	Mode   string `json:"mode" binding:"required,oneof=test live"`
	KeyID  string `json:"key_id" binding:"required"`
	KeySecret string `json:"key_secret" binding:"required"`
}

// ConnectorTestRequest triggers a connection test.
type ConnectorTestRequest struct {
	ConnectorID string `json:"connector_id" binding:"required"`
}

// ConnectorStatusResponse is the safe API response — never includes secrets.
type ConnectorStatusResponse struct {
	ID                  uuid.UUID  `json:"id"`
	Provider            string     `json:"provider"`
	ProviderMode        string     `json:"provider_mode"`
	Status              string     `json:"status"`
	LastHealthCheckAt   *time.Time `json:"last_health_check_at,omitempty"`
	LastHealthStatus    *string    `json:"last_health_status,omitempty"`
	LastHealthErrorCode *string    `json:"last_health_error_code,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
}
