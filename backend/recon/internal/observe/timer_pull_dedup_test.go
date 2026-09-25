package observe

// D26 / L1 guard: the payout timer pull (recon backfill job, resource
// "payouts") feeds the SAME payout-truth intake as payout webhooks, and a
// payout seen by both is counted once — keyed by the provider payout id.

import (
	"context"
	"testing"
	"time"

	"zord-outcome-engine/internal/poll"
	"zord-outcome-engine/internal/poll/providers/razorpay"
	"zord-outcome-engine/models"
)

type payoutTimerProvider struct {
	payouts []razorpay.NeutralPayout
}

func (p *payoutTimerProvider) ListPaymentsPage(context.Context, time.Time, time.Time, int, int) (razorpay.NeutralPage[razorpay.NeutralPayment], error) {
	return razorpay.NeutralPage[razorpay.NeutralPayment]{}, nil
}

func (p *payoutTimerProvider) ListSettlementDay(context.Context, razorpay.CivilDate, int, int) (razorpay.NeutralPage[razorpay.NeutralSettlementLine], error) {
	return razorpay.NeutralPage[razorpay.NeutralSettlementLine]{}, nil
}

func (p *payoutTimerProvider) ListPayoutsPage(_ context.Context, _, _ time.Time, skip, _ int) (razorpay.NeutralPage[razorpay.NeutralPayout], error) {
	if skip >= len(p.payouts) {
		return razorpay.NeutralPage[razorpay.NeutralPayout]{Meta: razorpay.ResponseMeta{Status: 200, Path: "/payouts"}}, nil
	}
	return razorpay.NeutralPage[razorpay.NeutralPayout]{
		Items: p.payouts[skip:],
		Meta:  razorpay.ResponseMeta{Status: 200, Path: "/payouts", Hash: "sha256:payouts"},
	}, nil
}

type timerCreds struct{}

func (timerCreds) Resolve(context.Context, string, string, string) (razorpay.Config, error) {
	cfg := razorpay.DefaultConfig()
	cfg.KeyID = "rzp_test_placeholder"
	cfg.KeySecret = "test-only"
	return cfg, nil
}

func runPayoutTimerPull(t *testing.T, store *poll.MemoryStore, items ...razorpay.NeutralPayout) poll.BackfillSummary {
	t.Helper()
	svc := poll.NewBackfillService(store, nil, timerCreds{}, func(razorpay.Config) (poll.BackfillProvider, error) {
		return &payoutTimerProvider{payouts: items}, nil
	})
	now := time.Now().UTC()
	job, err := svc.CreateJob(context.Background(), poll.CreateBackfillRequest{
		TenantID:     "11111111-1111-1111-1111-111111111111",
		ConnectorID:  "22222222-2222-2222-2222-222222222222",
		Mode:         "test",
		ResourceType: poll.ResourcePayouts,
		WindowFrom:   now.Add(-2 * time.Hour),
		WindowTo:     now.Add(-time.Minute),
		TriggerType:  poll.TriggerAirflow,
	})
	if err != nil {
		t.Fatalf("create payouts job: %v", err)
	}
	sum, err := svc.RunPayouts(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("run payouts job: %v", err)
	}
	return sum
}

func apiPayout(id, status string, amount int64) razorpay.NeutralPayout {
	return razorpay.NeutralFromPayout(razorpay.PayoutResponse{
		ID: id, Amount: amount, Currency: "INR", Status: status, Mode: "IMPS", UTR: "UTR123",
		CreatedAt: time.Now().Add(-30 * time.Minute).Unix(),
	})
}

func canonicalPayoutEvents(store *poll.MemoryStore) int {
	n := 0
	for _, row := range store.Outbox {
		if row.EventType == models.EventTypePayoutCanonicalUpdatedV1 {
			n++
		}
	}
	return n
}

func TestPayoutWebhookThenTimerPullCountsOnce(t *testing.T) {
	store := poll.NewMemoryStore()
	// 1) Webhook intake.
	res, err := NewProcessor(store).Apply(context.Background(), payoutEnvelope(t))
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind != ResultInserted {
		t.Fatalf("webhook kind=%s", res.Kind)
	}
	// 2) Timer pull returns the SAME provider payout id (same status/amount).
	sum := runPayoutTimerPull(t, store, apiPayout("pout_test_123", "processed", 25000000))
	if len(store.CanonicalPayouts) != 1 {
		t.Fatalf("payout counted %d times, want 1", len(store.CanonicalPayouts))
	}
	if sum.InsertedCount != 0 {
		t.Fatalf("timer pull inserted %d new payouts for an id the webhook already saw", sum.InsertedCount)
	}
	if got := canonicalPayoutEvents(store); got != 1 {
		t.Fatalf("canonical payout outbox events=%d want 1", got)
	}
	// Both sources are kept as observation history on the one payout.
	evs, _ := store.ListPayoutObservationEvents(context.Background(),
		"11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222", "pout_test_123")
	if len(evs) != 2 {
		t.Fatalf("observation history=%d want 2 (webhook + api_backfill)", len(evs))
	}
}

func TestPayoutTimerPullThenWebhookCountsOnce(t *testing.T) {
	store := poll.NewMemoryStore()
	sum := runPayoutTimerPull(t, store, apiPayout("pout_test_123", "processed", 25000000))
	if sum.InsertedCount != 1 {
		t.Fatalf("timer inserted=%d want 1", sum.InsertedCount)
	}
	res, err := NewProcessor(store).Apply(context.Background(), payoutEnvelope(t))
	if err != nil {
		t.Fatal(err)
	}
	if res.Kind == ResultInserted {
		t.Fatal("webhook re-inserted a payout the timer already counted")
	}
	if len(store.CanonicalPayouts) != 1 {
		t.Fatalf("canonical payouts=%d want 1", len(store.CanonicalPayouts))
	}
}

// L1: the de-dup key is the payout id, never the amount. A corrected amount on
// the same payout id updates the one row instead of creating a second copy.
func TestPayoutTimerPullSameIDChangedAmountStaysOneRow(t *testing.T) {
	store := poll.NewMemoryStore()
	runPayoutTimerPull(t, store, apiPayout("pout_amt_1", "processing", 10000))
	sum := runPayoutTimerPull(t, store, apiPayout("pout_amt_1", "processing", 10100))
	if len(store.CanonicalPayouts) != 1 {
		t.Fatalf("canonical payouts=%d want 1", len(store.CanonicalPayouts))
	}
	if sum.InsertedCount != 0 {
		t.Fatalf("amount change inserted a second payout")
	}
}

func TestPayoutTimerPullRerunIsDuplicate(t *testing.T) {
	store := poll.NewMemoryStore()
	p := apiPayout("pout_rerun", "processed", 5000)
	runPayoutTimerPull(t, store, p)
	sum := runPayoutTimerPull(t, store, p)
	if len(store.CanonicalPayouts) != 1 || sum.InsertedCount != 0 || sum.SkippedDuplicateCount != 1 {
		t.Fatalf("rerun: canonical=%d inserted=%d duplicate=%d", len(store.CanonicalPayouts), sum.InsertedCount, sum.SkippedDuplicateCount)
	}
	evs, _ := store.ListPayoutObservationEvents(context.Background(),
		"11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222", "pout_rerun")
	if len(evs) != 1 {
		t.Fatalf("unchanged re-pull added history: %d events", len(evs))
	}
}
