package canonicalizer

import (
	"testing"

	"zord-intent-engine/internal/models"

	"github.com/shopspring/decimal"
)

func TestCanonicalizer_KeepsLeadingZeroInAmount(t *testing.T) {
	cases := map[string]string{
		"0.50":      "0.50",
		" 0.01 ":    "0.01",
		".50":       "0.50",
		"000.05":    "0.05",
		"00012.30":  "12.30",
		"100000.10": "100000.10",
		"1":         "1",
		"0":         "0",
		"000":       "0",
		"":          "0",
		"-0.50":     "-0.50",
		"1.500":     "1.500", // trailing zeros are never touched
	}
	for in, want := range cases {
		var p models.ParsedIncomingIntent
		p.Amount.Value = in
		got := CanonicalizeIntent(p).Amount.Value
		if got != want {
			t.Errorf("amount %q -> %q, want %q", in, got, want)
		}
	}

	// The canonical string is exact: its paise value equals the raw input's,
	// so every hash that is built from amount_minor (business idempotency
	// v1/v2, row hash, registry) is unchanged by this fix — including the
	// sub-rupee case the old code rendered as ".50".
	for _, raw := range []string{"0.50", "0.01", "1.00", "250.50", "100000.10"} {
		oldForm := raw
		if raw[0] == '0' && len(raw) > 1 && raw[1] == '.' {
			oldForm = raw[1:] // what the previous TrimLeft("0") produced
		}
		newMinor := decimal.RequireFromString(normalizeAmountValue(raw)).Mul(decimal.NewFromInt(100)).IntPart()
		oldMinor := decimal.RequireFromString(oldForm).Mul(decimal.NewFromInt(100)).IntPart()
		if newMinor != oldMinor {
			t.Fatalf("%s: paise changed %d -> %d", raw, oldMinor, newMinor)
		}
		in := BusinessIdempotencyHashInput{TenantID: "t", SourceSystem: "ERP", ClientPayoutRef: "R", AmountMinor: newMinor, Currency: "INR"}
		oldIn := in
		oldIn.AmountMinor = oldMinor
		a, _ := ComputeBusinessIdempotencyHash(in)
		b, _ := ComputeBusinessIdempotencyHash(oldIn)
		if a != b {
			t.Fatalf("%s: v1 hash changed", raw)
		}
	}
}
