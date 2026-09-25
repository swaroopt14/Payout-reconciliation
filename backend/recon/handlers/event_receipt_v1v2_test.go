package handlers

import (
	"database/sql"
	"encoding/json"
	"testing"

	"zord-outcome-engine/models"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func strPtr(s string) *string { return &s }

func TestEventReceipt_V1V2KeyCompare(t *testing.T) {
	const v1 = "v1-legacy-amount-bearing-key"
	const v2 = "v2-reference-only-key"
	tenant := uuid.MustParse("11111111-1111-1111-1111-111111111111")

	stored := func(bizKey string) storedIntentRow {
		return storedIntentRow{
			tenantID:  tenant,
			bizKey:    sql.NullString{String: bizKey, Valid: bizKey != ""},
			payoutRef: sql.NullString{String: "REF-1", Valid: true},
			amount:    decimal.RequireFromString("1.00"),
			currency:  "INR",
			canonHash: "h", gov: "VALID",
		}
	}
	incoming := func(key, legacy *string, amount string) models.CanonicalIntent {
		return models.CanonicalIntent{
			TenantID:                 tenant,
			ClientPayoutRef:          strPtr("REF-1"),
			BusinessIdempotencyKey:   key,
			BusinessIdempotencyKeyV1: legacy,
			Amount:                   decimal.RequireFromString(amount),
			CurrencyCode:             "INR",
			CanonicalHash:            "h", GovernanceState: "VALID",
		}
	}

	cases := []struct {
		name   string
		row    storedIntentRow
		in     models.CanonicalIntent
		wantEq bool
	}{
		{"stored v1, incoming v1 only (old producer)", stored(v1), incoming(strPtr(v1), nil, "1.00"), true},
		{"stored v2, incoming v2", stored(v2), incoming(strPtr(v2), strPtr(v1), "1.00"), true},
		{"stored v1, incoming v2 + legacy v1", stored(v1), incoming(strPtr(v2), strPtr(v1), "1.00"), true},
		{"stored v1, incoming v2 with a different legacy", stored(v1), incoming(strPtr(v2), strPtr("other"), "1.00"), false},
		{"stored v2, incoming v1 only cannot be proven equal", stored(v2), incoming(strPtr(v1), nil, "1.00"), false},
		{"stored empty, incoming empty", stored(""), incoming(nil, nil, "1.00"), true},
		{"stored empty, incoming legacy only is not a match", stored(""), incoming(strPtr(v2), strPtr(v1), "1.00"), false},
		// Key equality never hides a changed amount: same key, different amount conflicts.
		{"stored v1, v2+legacy match but amount changed", stored(v1), incoming(strPtr(v2), strPtr(v1), "1.01"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := storedIntentMatches(tc.row, tc.in); got != tc.wantEq {
				t.Fatalf("storedIntentMatches=%v want %v", got, tc.wantEq)
			}
		})
	}

	t.Run("payload carries legacy v1 key to the compare", func(t *testing.T) {
		raw := []byte(`{"intent_id":"22222222-2222-2222-2222-222222222222","tenant_id":"11111111-1111-1111-1111-111111111111",
			"amount":"1.00","currency":"INR","client_payout_ref":"REF-1",
			"business_idempotency_key":"` + v2 + `","business_idempotency_key_v1":"` + v1 + `",
			"canonical_hash":"h","governance_state":"VALID"}`)
		var p models.IntentPayload
		if err := json.Unmarshal(raw, &p); err != nil {
			t.Fatal(err)
		}
		ci, err := canonicalIntentFromPayload(p, "")
		if err != nil {
			t.Fatal(err)
		}
		if ci.BusinessIdempotencyKeyV1 == nil || *ci.BusinessIdempotencyKeyV1 != v1 {
			t.Fatalf("legacy key not carried: %+v", ci.BusinessIdempotencyKeyV1)
		}
		ci.CanonicalHash, ci.GovernanceState = "h", "VALID"
		if !storedIntentMatches(stored(v1), ci) {
			t.Fatal("stored v1 must accept a v2 redelivery carrying the same legacy key")
		}
	})
}
