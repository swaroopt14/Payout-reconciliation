package recon

import (
	"context"
	"testing"
	"time"

	"zord-outcome-engine/internal/poll/providers/razorpay"
	"zord-outcome-engine/models"
)

func TestPAYO001_ProcessedExactDebitMatched(t *testing.T) {
	got := ReconcilePayout(PayoutInput{
		Payout: PayoutFact{
			ID: "cpout1", PayoutID: "pout_001", ProviderStatus: razorpay.PayoutProcessed,
			AmountMinor: 25000, Currency: "INR", UTR: "UTRPO1",
		},
		Banks: []BankTxn{{ID: "bdebit1", UTR: "UTRPO1", DebitMinor: 25000, CreditDebit: "DEBIT", Currency: "INR"}},
	})
	if got.Result != ResultMatched {
		t.Fatalf("result=%s reason=%s", got.Result, got.Reason)
	}
	if got.Status != razorpay.PayoutProcessed {
		t.Fatalf("status mutated: %s", got.Status)
	}
	if got.Exception != nil {
		t.Fatalf("unexpected exception %+v", got.Exception)
	}
	if got.EvidenceRefs.BankObservationID != "bdebit1" {
		t.Fatalf("evidence %+v", got.EvidenceRefs)
	}
}

func TestPAYO002_FailedNoBankMatchedStatusFailed(t *testing.T) {
	got := ReconcilePayout(PayoutInput{
		Payout: PayoutFact{
			PayoutID: "pout_002", ProviderStatus: razorpay.PayoutFailed, AmountMinor: 5000,
		},
	})
	if got.Result != ResultMatched {
		t.Fatalf("result=%s", got.Result)
	}
	if got.Exception != nil {
		t.Fatal("no exception for failed-no-movement")
	}
	if got.Status != razorpay.PayoutFailed {
		t.Fatalf("status mutated: %s", got.Status)
	}
	if got.BankCreditProven {
		t.Fatal("failed MATCHED must not claim bank credit")
	}
}

func TestPAYO003_FailedWithBankMovementUnresolved(t *testing.T) {
	got := ReconcilePayout(PayoutInput{
		Payout: PayoutFact{
			PayoutID: "pout_003", ProviderStatus: razorpay.PayoutFailed, AmountMinor: 1000,
		},
		Banks: []BankTxn{{ID: "bdebit", DebitMinor: 1000, CreditDebit: "DEBIT"}},
	})
	if got.Result != ResultUnresolved {
		t.Fatalf("result=%s", got.Result)
	}
	if got.Exception == nil || got.Exception.Reason != "payout_failed_with_bank_movement" {
		t.Fatalf("exception=%+v", got.Exception)
	}
	if got.VarianceAmount != 1000 || got.Exception.VarianceAmount != 1000 {
		t.Fatalf("impact=%d exception=%d", got.VarianceAmount, got.Exception.VarianceAmount)
	}
	if got.Status != razorpay.PayoutFailed {
		t.Fatalf("status mutated: %s", got.Status)
	}
}

func TestPAYO004_ProcessingPastSLAUnresolvedStatusUnchanged(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	got := ReconcilePayout(PayoutInput{
		Payout: PayoutFact{
			PayoutID: "pout_004", ProviderStatus: razorpay.PayoutProcessing,
			AmountMinor: 25000000, ProviderCreatedAt: now.Add(-30 * time.Minute),
		},
		Now: now, StuckAfter: DefaultPayoutSLA,
	})
	if got.Result != ResultUnresolved {
		t.Fatalf("result=%s", got.Result)
	}
	if got.Reason != "payout_open_past_sla" {
		t.Fatalf("reason=%s", got.Reason)
	}
	if got.Status != razorpay.PayoutProcessing {
		t.Fatalf("must not rename status, got %s", got.Status)
	}
	if got.Status == "STUCK" || got.Status == "SLA_BREACH" {
		t.Fatal("status must stay Razorpay processing")
	}
}

func TestCancelledNoBankMatched(t *testing.T) {
	got := ReconcilePayout(PayoutInput{
		Payout: PayoutFact{PayoutID: "pout_c", ProviderStatus: razorpay.PayoutCancelled, AmountMinor: 10},
	})
	if got.Result != ResultMatched || got.Exception != nil || got.Status != razorpay.PayoutCancelled {
		t.Fatalf("%+v", got)
	}
}

func TestProcessedMissingBankUnresolved(t *testing.T) {
	got := ReconcilePayout(PayoutInput{
		Payout: PayoutFact{PayoutID: "pout_m", ProviderStatus: razorpay.PayoutProcessed, AmountMinor: 99},
	})
	if got.Result != ResultUnresolved || got.Reason != "payout_missing_bank" {
		t.Fatalf("%s %s", got.Result, got.Reason)
	}
}

func TestFinancialRunIncludesPayoutsAndEmitsDecision(t *testing.T) {
	store := NewMemoryFinancialStore()
	store.Payouts = []PayoutFact{{
		ID: "cp", PayoutID: "pout_run", ProviderStatus: razorpay.PayoutFailed, AmountMinor: 1,
	}}
	svc := NewFinancialService(store)
	run, results, err := svc.Run(context.Background(), FinancialRunRequest{
		TenantID: "11111111-1111-1111-1111-111111111111", ConnectorID: "c",
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.PaymentCount != 1 {
		t.Fatalf("count=%d", run.PaymentCount)
	}
	if len(results) != 1 || results[0].EntityType != EntityPayout || results[0].Result != ResultMatched {
		t.Fatalf("%+v", results)
	}
	if len(store.Outbox) == 0 || store.Outbox[0].EventType != models.EventTypeReconDecisionV1 {
		t.Fatalf("outbox=%+v", store.Outbox)
	}
}

func TestFinancialRunScopedToPayoutIDsSkipsPayments(t *testing.T) {
	store := NewMemoryFinancialStore()
	store.Payments = []PaymentFact{{
		PaymentID: "pay_keep", CanonicalStatus: PaymentCaptured, ProviderStatus: "captured",
		Captured: true, AmountMinor: 10000, Currency: "INR",
	}}
	store.Payouts = []PayoutFact{
		{PayoutID: "pout_in", ProviderStatus: razorpay.PayoutFailed, AmountMinor: 1},
		{PayoutID: "pout_out", ProviderStatus: razorpay.PayoutFailed, AmountMinor: 2},
	}
	svc := NewFinancialService(store)
	run, results, err := svc.Run(context.Background(), FinancialRunRequest{
		TenantID: "11111111-1111-1111-1111-111111111111", ConnectorID: "c",
		BatchID: "BATCH-001", PayoutIDs: []string{"pout_in"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.BatchID != "BATCH-001" {
		t.Fatalf("batch=%s", run.BatchID)
	}
	if len(results) != 1 || results[0].EntityID != "pout_in" {
		t.Fatalf("%+v", results)
	}
	closeDoc := BatchCloseFromResults(run.BatchID, run.ID, results, 1)
	if closeDoc.Records != 1 || closeDoc.Unlinked != 0 {
		t.Fatalf("%+v", closeDoc)
	}
	got, ok, err := svc.BatchClose(context.Background(), "11111111-1111-1111-1111-111111111111", "c", "BATCH-001")
	if err != nil || !ok || got.RunID != run.ID {
		t.Fatalf("close=%+v ok=%v err=%v", got, ok, err)
	}
}
