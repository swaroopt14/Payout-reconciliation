package services

import (
	"context"
	"testing"

	"zord-intent-engine/internal/canonicalizer"
	"zord-intent-engine/internal/models"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type fakeRegistry struct {
	rows    map[string]*models.BusinessIdempotencyEntry // tenant|key
	lookups []string
}

func (f *fakeRegistry) put(tenant, key string, amountMinor int64) uuid.UUID {
	id := uuid.New()
	f.rows[tenant+"|"+key] = &models.BusinessIdempotencyEntry{
		TenantID: uuid.MustParse(tenant), BusinessIdempotencyKey: key, IntentID: id,
		AmountMinor: amountMinor, CurrencyCode: "INR", DuplicateReasonCode: "NONE",
	}
	return id
}

func (f *fakeRegistry) CheckIdempotencyRegistry(_ context.Context, tenantID, key string) (*models.BusinessIdempotencyEntry, error) {
	f.lookups = append(f.lookups, key)
	return f.rows[tenantID+"|"+key], nil
}

const (
	dedupTenant      = "11111111-1111-1111-1111-111111111111"
	dedupOtherTenant = "99999999-9999-9999-9999-999999999999"
)

func keysFor(ref string, rupees string) canonicalizer.BusinessIdempotencyKeys {
	return (&IntentService{}).computeBusinessIdempotencyKeys(
		dedupTenant, "ERP", ref, "fp-1", decimal.RequireFromString(rupees), "INR",
		"2026-09-25T10:00:00Z", "VENDOR",
	)
}

func TestDedup_AcceptsV1AndV2Keys(t *testing.T) {
	ctx := context.Background()

	t.Run("legacy v1 row still dedups", func(t *testing.T) {
		reg := &fakeRegistry{rows: map[string]*models.BusinessIdempotencyEntry{}}
		k := keysFor("REF-1", "1.00")
		legacyID := reg.put(dedupTenant, k.V1, 100) // written before the v2 rollout
		m, err := resolveBusinessIdempotency(ctx, reg, dedupTenant, k, 100, "INR")
		if err != nil {
			t.Fatal(err)
		}
		if m.Entry == nil || m.Entry.IntentID != legacyID || m.MatchedVersion != "v1" || m.AmountMismatch {
			t.Fatalf("expected v1 match without mismatch, got %+v", m)
		}
		if len(reg.lookups) != 2 || reg.lookups[0] != k.V2 {
			t.Fatalf("v2 must be checked first, then v1: %v", reg.lookups)
		}
	})

	t.Run("v2 row dedups and flags an amount change on the same ref", func(t *testing.T) {
		reg := &fakeRegistry{rows: map[string]*models.BusinessIdempotencyEntry{}}
		first := keysFor("REF-2", "1.00")
		reg.put(dedupTenant, first.V2, 100)
		corrected := keysFor("REF-2", "100000.10") // amount-corrected resubmission
		if corrected.V2 != first.V2 {
			t.Fatal("same ref must give the same v2 key regardless of amount")
		}
		m, err := resolveBusinessIdempotency(ctx, reg, dedupTenant, corrected, 10000010, "INR")
		if err != nil {
			t.Fatal(err)
		}
		if m.Entry == nil || m.MatchedVersion != "v2" {
			t.Fatalf("expected v2 match, got %+v", m)
		}
		if !m.AmountMismatch {
			t.Fatal("changed amount on the same reference must be flagged, not merged silently")
		}
	})

	t.Run("same amount same ref is a plain duplicate", func(t *testing.T) {
		reg := &fakeRegistry{rows: map[string]*models.BusinessIdempotencyEntry{}}
		k := keysFor("REF-3", "250.50")
		reg.put(dedupTenant, k.V2, 25050)
		m, _ := resolveBusinessIdempotency(ctx, reg, dedupTenant, k, 25050, "INR")
		if m.Entry == nil || m.AmountMismatch {
			t.Fatalf("expected clean duplicate, got %+v", m)
		}
	})

	t.Run("tenant scoped", func(t *testing.T) {
		reg := &fakeRegistry{rows: map[string]*models.BusinessIdempotencyEntry{}}
		k := keysFor("REF-4", "1.00")
		reg.put(dedupOtherTenant, k.V2, 100)
		m, _ := resolveBusinessIdempotency(ctx, reg, dedupTenant, k, 100, "INR")
		if m.Entry != nil {
			t.Fatal("another tenant's registry row must never dedup this tenant")
		}
	})

	t.Run("no reference uses fallback once and keeps amount", func(t *testing.T) {
		reg := &fakeRegistry{rows: map[string]*models.BusinessIdempotencyEntry{}}
		k100 := keysFor("", "1.00")
		reg.put(dedupTenant, k100.V2, 100)
		k200 := keysFor("", "2.00")
		m, _ := resolveBusinessIdempotency(ctx, reg, dedupTenant, k200, 200, "INR")
		if m.Entry != nil {
			t.Fatal("fallback key keeps amount: a different amount is a different payment")
		}
		if len(reg.lookups) != 1 {
			t.Fatalf("identical v1/v2 fallback must be looked up once, got %v", reg.lookups)
		}
		if legacyBusinessKey(k200) != "" {
			t.Fatal("fallback has no separate legacy key to carry")
		}
	})

	t.Run("stored key is v2 and legacy v1 is carried for recon", func(t *testing.T) {
		k := keysFor("REF-5", "1.00")
		svcKey := (&IntentService{}).computeBusinessIdempotencyKey(
			dedupTenant, "ERP", "REF-5", "fp-1", decimal.RequireFromString("1.00"), "INR",
			"2026-09-25T10:00:00Z", "VENDOR")
		if svcKey != k.V2 {
			t.Fatal("the single-key helper must return the v2 (stored) key")
		}
		if legacyBusinessKey(k) != k.V1 || k.V1 == k.V2 {
			t.Fatal("legacy v1 must be carried when it differs from v2")
		}
	})
}
