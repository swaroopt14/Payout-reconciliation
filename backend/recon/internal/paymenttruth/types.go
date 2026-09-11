package paymenttruth

import (
	"time"

	"zord-outcome-engine/internal/recon"
)

const (
	IntentLinked   = "linked"
	IntentUnlinked = "unlinked"
)

// Observation is one immutable provider snapshot (webhook or API).
type Observation struct {
	TenantID         string
	ConnectorID      string
	Provider         string
	ProviderMode     string
	PaymentID        string
	OrderID          string
	AmountMinor      int64
	Currency         string
	Method           string
	ProviderStatus   string
	CanonicalStatus  string
	Captured         bool
	FeeMinor         int64
	TaxMinor         int64
	ProviderCreatedAt time.Time
	CapturedAt       time.Time
	ObservedAt       time.Time
	Source           string
	SourceEventID    string
	SourceHash       string
	RawReference     string
	IdentityHash     string
	ReceiptID        string
	WebhookMissing   bool
	Email            string
	Contact          string
}

// CanonicalPayment is the reduced current payment truth.
type CanonicalPayment struct {
	ID               string
	TenantID         string
	ConnectorID      string
	Provider         string
	PaymentID        string
	OrderID          string
	AmountMinor      int64
	Currency         string
	Method           string
	ProviderStatus   string
	CanonicalStatus  string
	Captured         bool
	FeeMinor         int64
	TaxMinor         int64
	ProviderCreatedAt time.Time
	CapturedAt       time.Time
	FirstObservedAt  time.Time
	LastObservedAt   time.Time
	Sources          []string
	IntentID         string
	IntentLink       string
	BatchID          string
}

func Rank(status string) int {
	switch status {
	case recon.PaymentCreated:
		return 1
	case recon.PaymentAuthorized:
		return 2
	case recon.PaymentCaptured:
		return 3
	case recon.PaymentPartiallyRefunded:
		return 4
	case recon.PaymentRefunded:
		return 5
	case recon.PaymentFailed:
		return 2
	default:
		return 0
	}
}

func IsFailed(status string) bool {
	return status == recon.PaymentFailed
}

func IsCapturedOrLater(status string) bool {
	switch status {
	case recon.PaymentCaptured, recon.PaymentPartiallyRefunded, recon.PaymentRefunded:
		return true
	default:
		return false
	}
}
