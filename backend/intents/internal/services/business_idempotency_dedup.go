package services

import (
	"context"
	"log"
	"strings"
	"time"

	"zord-intent-engine/internal/canonicalizer"
	"zord-intent-engine/internal/models"

	"github.com/shopspring/decimal"
)

// AnomalyAmountChangedSameRef is added to ValidationAnomalies when an intent
// reuses a business reference that is already registered with a DIFFERENT
// amount. Dedup (hash v2) groups the two by reference; this anomaly makes
// sure the changed amount is surfaced (FLAGGED governance, duplicate
// decision row) instead of being merged silently. It is an intent-side
// anomaly code, not a recon verdict (D7).
const AnomalyAmountChangedSameRef = "AMOUNT_CHANGED_SAME_REF"

// idempotencyRegistryChecker is the narrow slice of CanonicalIntentRepository
// the business-idempotency dedup needs.
type idempotencyRegistryChecker interface {
	CheckIdempotencyRegistry(ctx context.Context, tenantID string, key string) (*models.BusinessIdempotencyEntry, error)
}

// businessIdempotencyMatch is the outcome of checking both key generations.
type businessIdempotencyMatch struct {
	Entry          *models.BusinessIdempotencyEntry
	MatchedKey     string
	MatchedVersion string // "v2" or "v1"; "" when no match
	// AmountMismatch is true when the match is on a business reference and
	// the registered amount (or currency) differs from the incoming one.
	AmountMismatch bool
}

// resolveBusinessIdempotency checks the registry for the v2 key first, then
// the legacy v1 key (rows written before the v2 rollout), scoped by tenant.
// On a reference match with a different amount it logs a mismatch and sets
// AmountMismatch so the caller flags the intent rather than dropping it.
func resolveBusinessIdempotency(
	ctx context.Context,
	repo idempotencyRegistryChecker,
	tenantID string,
	keys canonicalizer.BusinessIdempotencyKeys,
	amountMinor int64,
	currency string,
) (businessIdempotencyMatch, error) {
	for _, key := range keys.Candidates() {
		entry, err := repo.CheckIdempotencyRegistry(ctx, tenantID, key)
		if err != nil {
			return businessIdempotencyMatch{}, err
		}
		if entry == nil {
			continue
		}
		version := "v2"
		if key != keys.V2 {
			version = "v1"
		}
		m := businessIdempotencyMatch{Entry: entry, MatchedKey: key, MatchedVersion: version}
		if keys.HasRef && (entry.AmountMinor != amountMinor ||
			!strings.EqualFold(strings.TrimSpace(entry.CurrencyCode), strings.TrimSpace(currency))) {
			m.AmountMismatch = true
			log.Printf(
				"business_idempotency.amount_mismatch tenant=%s key_version=%s registered_intent=%s registered_amount_minor=%d registered_currency=%s incoming_amount_minor=%d incoming_currency=%s",
				tenantID, version, entry.IntentID, entry.AmountMinor, entry.CurrencyCode, amountMinor, currency,
			)
		}
		return m, nil
	}
	return businessIdempotencyMatch{}, nil
}

// computeBusinessIdempotencyKeys computes the v2 key (stored going forward)
// and the legacy v1 key (checked during the transition). See
// canonicalizer.ComputeBusinessIdempotencyKeys.
func (s *IntentService) computeBusinessIdempotencyKeys(
	tenantID string,
	sourceSystem string,
	clientPayoutRef string,
	fingerPrint string,
	amount decimal.Decimal,
	currency string,
	intendedExecutionAt string,
	purposeCode string,
) canonicalizer.BusinessIdempotencyKeys {
	amountMinor := amount.Mul(decimal.NewFromInt(100)).IntPart()

	executionDate := ""
	if t, err := time.Parse(time.RFC3339, strings.TrimSpace(intendedExecutionAt)); err == nil {
		executionDate = t.UTC().Format("2006-01-02")
	}

	keys, err := canonicalizer.ComputeBusinessIdempotencyKeys(
		canonicalizer.BusinessIdempotencyHashInput{
			TenantID:        tenantID,
			SourceSystem:    sourceSystem,
			ClientPayoutRef: clientPayoutRef,
			AmountMinor:     amountMinor,
			Currency:        currency,
		},
		canonicalizer.BusinessIdempotencyFallbackHashInput{
			TenantID:               tenantID,
			BeneficiaryFingerprint: fingerPrint,
			AmountMinor:            amountMinor,
			Currency:               currency,
			ExecutionDate:          executionDate,
			InvoiceRef:             "",
			PurposeCode:            purposeCode,
		},
	)
	if err != nil {
		log.Printf("⚠️ Failed to compute business idempotency keys for tenant %s: %v", tenantID, err)
		return canonicalizer.BusinessIdempotencyKeys{}
	}
	return keys
}

// legacyBusinessKey returns the v1 key only when it differs from the stored
// v2 key, so recon can accept a redelivery carrying either generation.
func legacyBusinessKey(k canonicalizer.BusinessIdempotencyKeys) string {
	if k.V1 == "" || k.V1 == k.V2 {
		return ""
	}
	return k.V1
}
