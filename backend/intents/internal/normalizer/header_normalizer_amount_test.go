package normalizer_test

import (
	"encoding/json"
	"strings"
	"testing"

	"zord-intent-engine/internal/models"
	"zord-intent-engine/internal/normalizer"
	"zord-intent-engine/internal/validator"
)

const subPaiseMsg = "more than two decimal places"

func normalizeAndParse(t *testing.T, raw string) models.ParsedIncomingIntent {
	t.Helper()
	res, err := normalizer.Normalize([]byte(raw), nil)
	if err != nil {
		t.Fatalf("Normalize(%s): %v", raw, err)
	}
	var parsed models.ParsedIncomingIntent
	if err := json.Unmarshal(res.NormalizedJSON, &parsed); err != nil {
		t.Fatalf("unmarshal normalized JSON %s: %v", res.NormalizedJSON, err)
	}
	return parsed
}

// TestHeaderNormalizer_SubPaiseNotRounded guards D10/L3 on the ingest path:
// the header normalizer used ParseFloat + math.Round, which silently turned
// 1.005 into a different amount. It must now stay exact so the semantic
// validator rejects it, while 100.50 stays exactly 100.50.
func TestHeaderNormalizer_SubPaiseNotRounded(t *testing.T) {
	rejected := []string{
		`{"payout amount": "1.005", "ref": "r1"}`,
		`{"payout amount": 1.005, "ref": "r1"}`, // JSON number, not string
		`{"txn amount": "₹1,000.005", "ref": "r1"}`,
	}
	for _, raw := range rejected {
		parsed := normalizeAndParse(t, raw)
		if parsed.Amount.Value == "1.01" || parsed.Amount.Value == "1.00" || parsed.Amount.Value == "1000.01" || parsed.Amount.Value == "1000.00" {
			t.Fatalf("%s: amount was rounded to %q", raw, parsed.Amount.Value)
		}
		err := validator.SemanticValidate(parsed)
		if err == nil || !strings.Contains(err.Error(), subPaiseMsg) {
			t.Fatalf("%s: SemanticValidate = %v, want sub-paise rejection (value %q)", raw, err, parsed.Amount.Value)
		}
	}

	exact := map[string]string{
		`{"payout amount": "100.50", "ref": "r1"}`:      "100.50",
		`{"payout amount": 100.5, "ref": "r1"}`:         "100.50",
		`{"payout amount": "1,00,000.10", "ref": "r1"}`: "100000.10",
		`{"payout amount": "Rs. 500", "ref": "r1"}`:     "500.00",
		`{"payout amount": "(1000)", "ref": "r1"}`:      "-1000.00",
		`{"payout amount": "0.01", "ref": "r1"}`:        "0.01",
	}
	for raw, want := range exact {
		parsed := normalizeAndParse(t, raw)
		if parsed.Amount.Value != want {
			t.Fatalf("%s: amount = %q, want %q", raw, parsed.Amount.Value, want)
		}
		if err := validator.SemanticValidate(parsed); err != nil && strings.Contains(err.Error(), subPaiseMsg) {
			t.Fatalf("%s: exact amount wrongly rejected as sub-paise: %v", raw, err)
		}
	}
}
