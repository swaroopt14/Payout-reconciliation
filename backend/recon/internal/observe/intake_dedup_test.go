package observe

import (
	"context"
	"testing"
	"time"

	"zord-outcome-engine/internal/payouttruth"
	"zord-outcome-engine/internal/poll"
	"zord-outcome-engine/internal/poll/providers/razorpay"
	"zord-outcome-engine/internal/recon"
)

// TestIntake_DuplicateProviderIDNoDoubleCount pins D18/D26: webhook and poll
// feed one de-duplicating intake keyed by the provider's own id under
// tenant_id + connector_id, so replays and dual delivery never double-count.
func TestIntake_DuplicateProviderIDNoDoubleCount(t *testing.T) {
	ctx := context.Background()
	store := poll.NewMemoryStore()
	sink := recon.NewMemoryFinancialStore()
	p := NewProcessor(store)
	p.Refunds = sink

	base := capturedEnvelope(t)
	tenant, connector := base.TenantID, base.ConnectorID

	refundEnv := func(eventID, refundID, status string, amount int64) Envelope {
		env := capturedEnvelope(t)
		env.ProviderEventID = eventID
		env.ProviderEventType = "refund." + status
		env.ProviderEntityType = "refund"
		env.ProviderEntityID = refundID
		env.PaymentID = "pay_dup_1"
		env.Amount = amount
		env.Status = status
		env.SellerID = "seller_A"
		return env
	}

	// --- Refunds: same refund id delivered twice (webhook retry with a new
	// event id), plus a failed refund. ---
	for _, ev := range []string{"evt_rf_1", "evt_rf_1_retry"} {
		if _, err := p.Apply(ctx, refundEnv(ev, "rfnd_dup", "processed", 1500)); err != nil {
			t.Fatal(err)
		}
	}
	// Same refund observed by the backfill pull.
	if _, err := sink.UpsertRefund(ctx, tenant, connector, recon.RefundFact{
		RefundID: "rfnd_dup", PaymentID: "pay_dup_1", AmountMinor: 1500, Currency: "INR",
		ProviderStatus: "processed", Source: "poll", SellerID: "seller_A",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Apply(ctx, refundEnv("evt_rf_fail", "rfnd_failed", "failed", 700)); err != nil {
		t.Fatal(err)
	}
	// Same refund id under ANOTHER tenant must stay a separate row.
	if _, err := sink.UpsertRefund(ctx, "99999999-9999-9999-9999-999999999999", connector, recon.RefundFact{
		RefundID: "rfnd_dup", PaymentID: "pay_other", AmountMinor: 999, ProviderStatus: "processed",
	}); err != nil {
		t.Fatal(err)
	}

	refunds, err := sink.ListRefunds(ctx, tenant, connector, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(refunds) != 2 {
		t.Fatalf("want 2 refund rows (rfnd_dup once + rfnd_failed), got %d: %+v", len(refunds), refunds)
	}
	var dupCount int
	for _, r := range refunds {
		if r.RefundID == "rfnd_dup" {
			dupCount++
			if r.AmountMinor != 1500 {
				t.Fatalf("rfnd_dup amount=%d", r.AmountMinor)
			}
		}
		if r.RefundID == "rfnd_failed" && r.ProviderStatus != "failed" {
			t.Fatalf("failed refund status=%q", r.ProviderStatus)
		}
	}
	if dupCount != 1 {
		t.Fatalf("rfnd_dup counted %d times", dupCount)
	}
	pat, err := sink.ListMarketplaceSellerPatterns(ctx, tenant, connector)
	if err != nil {
		t.Fatal(err)
	}
	if len(pat.Sellers) != 1 || pat.Sellers[0].RefundCount != 1 || pat.Sellers[0].RefundSumMinor != 1500 {
		t.Fatalf("duplicate or failed refund leaked into totals: %+v", pat.Sellers)
	}

	// --- Settlement: a duplicate refund line (same settlement_id + entity_id)
	// from a second poll page is a duplicate, not a second line. ---
	line := poll.SettlementLineObservation{
		TenantID: tenant, ConnectorID: connector, Provider: "razorpay", ProviderMode: "test", Source: "poll",
		Item: razorpay.NeutralSettlementLine{
			SettlementID: "setl_1", EntityID: "rfnd_dup", LineType: "refund", PaymentID: "pay_dup_1",
			RefundID: "rfnd_dup", AmountMinor: 1500, DebitMinor: 1500, Currency: "INR", PayloadHash: "h-line",
		},
	}
	if res, err := store.UpsertSettlementLine(ctx, line); err != nil || res != poll.UpsertInserted {
		t.Fatalf("first line res=%v err=%v", res, err)
	}
	if res, err := store.UpsertSettlementLine(ctx, line); err != nil || res != poll.UpsertDuplicate {
		t.Fatalf("duplicate refund line res=%v err=%v", res, err)
	}
	if len(store.Settlements) != 1 {
		t.Fatalf("settlement lines=%d, want 1", len(store.Settlements))
	}

	// --- Payouts: webhook twice (replay) + poll for the same payout id. ---
	pe := payoutEnvelope(t)
	if _, err := p.Apply(ctx, pe); err != nil {
		t.Fatal(err)
	}
	if res, err := p.Apply(ctx, pe); err != nil || res.Kind != ResultDuplicate {
		t.Fatalf("webhook replay res=%+v err=%v", res, err)
	}
	obs, err := payouttruth.MapNeutral(tenant, connector, "razorpay", "poll", "", razorpay.NeutralPayout{
		PayoutID: pe.ProviderEntityID, AmountMinor: pe.Amount, Currency: "INR", Status: "processed",
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := payouttruth.NewProcessor(store).Process(ctx, obs); err != nil {
		t.Fatal(err)
	}
	if len(store.CanonicalPayouts) != 1 {
		t.Fatalf("canonical payouts=%d, want 1", len(store.CanonicalPayouts))
	}
	for _, pay := range store.CanonicalPayouts {
		if pay.AmountMinor != pe.Amount {
			t.Fatalf("payout amount=%d want %d", pay.AmountMinor, pe.Amount)
		}
	}
}
