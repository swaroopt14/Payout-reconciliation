package canonicalizer

import (
	"strings"

	"zord-intent-engine/internal/models"
)

type MappingProfile struct {
	ProfileID    string            `json:"profile_id"`
	Version      string            `json:"version"`
	MappingRules map[string]string `json:"mapping_rules"`
	IsActive     bool              `json:"is_active"`
}

func CanonicalizeIntent(input models.ParsedIncomingIntent) models.ParsedIncomingIntent {

	out := input // copy

	// intent_type
	out.IntentType = strings.ToUpper(strings.TrimSpace(out.IntentType))

	// account_number
	out.AccountNumber = strings.TrimSpace(out.AccountNumber)
	out.Beneficiary.Name = strings.TrimSpace(out.Beneficiary.Name)

	// amount
	out.Amount.Value = normalizeAmountValue(out.Amount.Value)
	out.Amount.Currency = strings.ToUpper(strings.TrimSpace(out.Amount.Currency))

	// beneficiary.instrument.kind
	out.Beneficiary.Instrument.Kind =
		strings.ToUpper(strings.TrimSpace(out.Beneficiary.Instrument.Kind))

	// beneficiary country
	if out.Beneficiary.Country != "" {
		out.Beneficiary.Country =
			strings.ToUpper(strings.TrimSpace(out.Beneficiary.Country))
	}
	out.Remitter.Phone = strings.TrimSpace(out.Remitter.Phone)
	out.Remitter.Email = strings.ToLower(strings.TrimSpace(out.Remitter.Email))

	//  purpose_code
	out.PurposeCode = strings.ToUpper(strings.TrimSpace(out.PurposeCode))

	// idempotency_key
	out.IdempotencyKey = strings.TrimSpace(out.IdempotencyKey)

	// intended_execution_at
	out.IntendedExecutionAt = strings.TrimSpace(out.IntendedExecutionAt)

	// remitter, constraints, metadata → untouched
	return out
}

// normalizeAmountValue returns the amount as an exact decimal string. It
// only drops redundant leading zeros from the integer part and always keeps
// one digit before the decimal point ("0.50" stays "0.50", "00012.30" ->
// "12.30", ".50" -> "0.50"). The fractional part, including trailing zeros,
// is never touched, so no precision is gained or lost (D10). A leading sign
// is preserved so negative amounts still reach the governance checks.
func normalizeAmountValue(v string) string {
	v = strings.TrimSpace(v)
	sign := ""
	if strings.HasPrefix(v, "-") || strings.HasPrefix(v, "+") {
		sign, v = v[:1], v[1:]
	}
	intPart, frac, hasFrac := strings.Cut(v, ".")
	intPart = strings.TrimLeft(intPart, "0")
	if intPart == "" {
		intPart = "0"
	}
	if hasFrac {
		return sign + intPart + "." + frac
	}
	return sign + intPart
}
