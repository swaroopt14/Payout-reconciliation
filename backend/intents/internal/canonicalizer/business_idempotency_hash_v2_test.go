package canonicalizer

import "testing"

func prefInput(amount int64) BusinessIdempotencyHashInput {
	return BusinessIdempotencyHashInput{
		TenantID:        "11111111-1111-1111-1111-111111111111",
		SourceSystem:    "erp",
		ClientPayoutRef: "PAYOUT-REF-001",
		AmountMinor:     amount,
		Currency:        "inr",
	}
}

func fallbackInput(amount int64) BusinessIdempotencyFallbackHashInput {
	return BusinessIdempotencyFallbackHashInput{
		TenantID:               "11111111-1111-1111-1111-111111111111",
		BeneficiaryFingerprint: "fp-abc",
		AmountMinor:            amount,
		Currency:               "INR",
		ExecutionDate:          "2026-09-25",
		PurposeCode:            "vendor",
	}
}

func TestBusinessIdempotencyHashV2_IgnoresAmountWithRef(t *testing.T) {
	a, err := ComputeBusinessIdempotencyHashV2(prefInput(100)) // ₹1.00
	if err != nil {
		t.Fatal(err)
	}
	b, err := ComputeBusinessIdempotencyHashV2(prefInput(10000010)) // ₹1,00,000.10
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("v2 key must ignore amount when a reference exists: %s != %s", a, b)
	}

	// A different reference is a different payout.
	other := prefInput(100)
	other.ClientPayoutRef = "PAYOUT-REF-002"
	c, _ := ComputeBusinessIdempotencyHashV2(other)
	if c == a {
		t.Fatal("different client_payout_ref must give a different v2 key")
	}

	// v1 still carries amount, and v1 != v2 (distinct hash_version).
	v1a, _ := ComputeBusinessIdempotencyHash(prefInput(100))
	v1b, _ := ComputeBusinessIdempotencyHash(prefInput(10000010))
	if v1a == v1b {
		t.Fatal("v1 key must still include amount (legacy rows)")
	}
	if v1a == a {
		t.Fatal("v1 and v2 keys must not collide")
	}

	keys, err := ComputeBusinessIdempotencyKeys(prefInput(100), fallbackInput(100))
	if err != nil {
		t.Fatal(err)
	}
	if !keys.HasRef || keys.V2 != a || keys.V1 != v1a || len(keys.Candidates()) != 2 {
		t.Fatalf("unexpected keys %+v", keys)
	}
}

func TestBusinessIdempotencyHash_NoRefKeepsAmount(t *testing.T) {
	for _, ref := range []string{"", "  ", "NA"} {
		p1 := prefInput(100)
		p1.ClientPayoutRef = ref
		p2 := prefInput(200)
		p2.ClientPayoutRef = ref
		k1, err := ComputeBusinessIdempotencyKeys(p1, fallbackInput(100))
		if err != nil {
			t.Fatal(err)
		}
		k2, err := ComputeBusinessIdempotencyKeys(p2, fallbackInput(200))
		if err != nil {
			t.Fatal(err)
		}
		if k1.HasRef || k1.HashFormat != "fallback" {
			t.Fatalf("ref %q must use fallback: %+v", ref, k1)
		}
		if k1.V2 == k2.V2 {
			t.Fatalf("ref %q: fallback key must keep amount (two payments to one beneficiary differ only by amount)", ref)
		}
		// Fallback is unchanged across versions so stored fallback keys keep matching.
		want, _ := ComputeBusinessIdempotencyFallbackHash(fallbackInput(100))
		if k1.V1 != k1.V2 || k1.V2 != want {
			t.Fatalf("ref %q: fallback v1/v2 must be identical to the existing fallback hash", ref)
		}
		if len(k1.Candidates()) != 1 {
			t.Fatalf("identical v1/v2 must be looked up once, got %v", k1.Candidates())
		}
	}
}
