package canonicalizer

import (
	"strings"

	"zord-intent-engine/internal/jcs"
)

// BusinessIdempotencyHashInput holds the fields for the "preferred" formula
// — used when the customer supplies a reliable unique payout reference.
type BusinessIdempotencyHashInput struct {
	TenantID        string
	SourceSystem    string
	ClientPayoutRef string
	AmountMinor     int64
	Currency        string
}

// ComputeBusinessIdempotencyHash returns
// business_idempotency_hash = SHA-256(JCS_Canonicalize({hash_type, hash_version, ...preferred fields}))
func ComputeBusinessIdempotencyHash(in BusinessIdempotencyHashInput) (string, error) {
	fields := map[string]any{
		"hash_type":         "BUSINESS_IDEMPOTENCY",
		"hash_version":      "1",
		"tenant_id":         in.TenantID,
		"source_system":     strings.ToUpper(strings.TrimSpace(in.SourceSystem)),
		"client_payout_ref": strings.TrimSpace(in.ClientPayoutRef),
		"amount_minor":      in.AmountMinor,
		"currency":          strings.ToUpper(strings.TrimSpace(in.Currency)),
	}
	return jcs.CanonicalizeAndSHA256(fields)
}

// BusinessIdempotencyFallbackHashInput holds the fields for the "fallback"
// formula — used only when there is no reliable unique payment reference.
// This should normally produce a possible-duplicate warning, not an
// automatic hard rejection, since two legitimate payments may share the same
// beneficiary and amount.
type BusinessIdempotencyFallbackHashInput struct {
	TenantID               string
	BeneficiaryFingerprint string
	AmountMinor            int64
	Currency               string
	ExecutionDate          string
	InvoiceRef             string
	PurposeCode            string
}

// ComputeBusinessIdempotencyFallbackHash returns
// business_idempotency_fallback_hash = SHA-256(JCS_Canonicalize({hash_type, hash_version, ...fallback fields}))
func ComputeBusinessIdempotencyFallbackHash(in BusinessIdempotencyFallbackHashInput) (string, error) {
	fields := map[string]any{
		"hash_type":               "BUSINESS_IDEMPOTENCY_FALLBACK",
		"hash_version":            "1",
		"tenant_id":               in.TenantID,
		"beneficiary_fingerprint": in.BeneficiaryFingerprint,
		"amount_minor":            in.AmountMinor,
		"currency":                strings.ToUpper(strings.TrimSpace(in.Currency)),
		"execution_date":          in.ExecutionDate,
		"invoice_ref":             strings.TrimSpace(in.InvoiceRef),
		"purpose_code":            strings.ToUpper(strings.TrimSpace(in.PurposeCode)),
	}
	return jcs.CanonicalizeAndSHA256(fields)
}

// IsReliableClientPayoutRef reports whether ref is a real, usable payout
// reference for the preferred business-idempotency formula (as opposed to
// empty or the "NA" sentinel some sources send).
func IsReliableClientPayoutRef(ref string) bool {
	ref = strings.TrimSpace(ref)
	return ref != "" && ref != "NA"
}

// BusinessIdempotencyHashVersionV2 is the hash_version stamped into the v2
// preferred formula.
const BusinessIdempotencyHashVersionV2 = "2"

// ComputeBusinessIdempotencyHashV2 is the v2 preferred formula (Slice 8, D18):
// a payout is identified by its own business reference, never by its amount,
// so amount_minor is deliberately NOT hashed. An amount-corrected resubmission
// of the same client_payout_ref therefore lands on the same key and is caught
// as a duplicate — the caller must then compare amounts and flag a mismatch
// instead of merging silently (see services.resolveBusinessIdempotency).
//
//	business_idempotency_hash_v2 = SHA-256(JCS({hash_type, hash_version:"2",
//	    tenant_id, source_system, client_payout_ref, currency}))
func ComputeBusinessIdempotencyHashV2(in BusinessIdempotencyHashInput) (string, error) {
	fields := map[string]any{
		"hash_type":         "BUSINESS_IDEMPOTENCY",
		"hash_version":      BusinessIdempotencyHashVersionV2,
		"tenant_id":         in.TenantID,
		"source_system":     strings.ToUpper(strings.TrimSpace(in.SourceSystem)),
		"client_payout_ref": strings.TrimSpace(in.ClientPayoutRef),
		"currency":          strings.ToUpper(strings.TrimSpace(in.Currency)),
	}
	return jcs.CanonicalizeAndSHA256(fields)
}

// BusinessIdempotencyKeys carries both key generations during the v1 -> v2
// transition. V2 is the key stored going forward; V1 is the legacy key that
// rows written before the rollout carry. Dedup must look up BOTH. When there
// is no reliable reference the fallback formula (which keeps amount — two
// different payments to the same beneficiary are only told apart by amount)
// is unchanged, so V1 == V2 and nothing already stored changes meaning.
type BusinessIdempotencyKeys struct {
	V2         string
	V1         string
	HasRef     bool
	HashFormat string // "preferred" or "fallback"
}

// Candidates returns the distinct non-empty keys to check, V2 first.
func (k BusinessIdempotencyKeys) Candidates() []string {
	out := make([]string, 0, 2)
	if k.V2 != "" {
		out = append(out, k.V2)
	}
	if k.V1 != "" && k.V1 != k.V2 {
		out = append(out, k.V1)
	}
	return out
}

// Matches reports whether stored equals either generation of this key.
func (k BusinessIdempotencyKeys) Matches(stored string) bool {
	stored = strings.TrimSpace(stored)
	if stored == "" {
		return false
	}
	return stored == k.V2 || stored == k.V1
}

// ComputeBusinessIdempotencyKeys computes the v2 (stored) and v1 (legacy)
// keys. With a reliable reference: V2 excludes amount, V1 is the original
// amount-bearing preferred hash. Without one: both are the fallback hash.
func ComputeBusinessIdempotencyKeys(pref BusinessIdempotencyHashInput, fallback BusinessIdempotencyFallbackHashInput) (BusinessIdempotencyKeys, error) {
	if IsReliableClientPayoutRef(pref.ClientPayoutRef) {
		v2, err := ComputeBusinessIdempotencyHashV2(pref)
		if err != nil {
			return BusinessIdempotencyKeys{}, err
		}
		v1, err := ComputeBusinessIdempotencyHash(pref)
		if err != nil {
			return BusinessIdempotencyKeys{}, err
		}
		return BusinessIdempotencyKeys{V2: v2, V1: v1, HasRef: true, HashFormat: "preferred"}, nil
	}
	fb, err := ComputeBusinessIdempotencyFallbackHash(fallback)
	if err != nil {
		return BusinessIdempotencyKeys{}, err
	}
	return BusinessIdempotencyKeys{V2: fb, V1: fb, HasRef: false, HashFormat: "fallback"}, nil
}
