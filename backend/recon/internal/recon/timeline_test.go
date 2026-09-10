package recon

import (
	"testing"
	"time"
)

func TestBuildEntityTimelineUsesWebhookObservedAt(t *testing.T) {
	created := time.Date(2026, 4, 1, 9, 59, 50, 0, time.UTC)
	t1 := time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 4, 1, 10, 0, 12, 0, time.UTC)
	t3 := time.Date(2026, 4, 1, 10, 1, 4, 0, time.UTC)
	settled := time.Date(2026, 4, 1, 11, 0, 0, 0, time.UTC)
	valueDate := time.Date(2026, 4, 1, 11, 30, 0, 0, time.UTC)
	reconAt := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)

	out := BuildEntityTimeline(timelineFacts{
		EntityType:        EntityPayout,
		EntityID:          "pout_1",
		ProviderStatus:    "processed",
		AmountMinor:       10000,
		Currency:          "INR",
		UTR:               "HDFC123",
		Mode:              "IMPS",
		ProviderCreatedAt: created,
		FirstObservedAt:   t1,
		Events: []ObservationFact{
			{Source: "webhook", ProviderStatus: "queued", SourceEventID: "evt_q", ObservedAt: t1},
			{Source: "webhook", ProviderStatus: "processing", SourceEventID: "evt_p", ObservedAt: t2},
			{Source: "webhook", ProviderStatus: "processed", SourceEventID: "evt_d", RawReference: "HDFC123", ObservedAt: t3},
		},
		Lines: []SettlementLine{{
			ID: "sl1", PaymentID: "pout_1", SettlementID: "setl_1", CreditMinor: 9728, SettledAt: settled, UTR: "HDFC123",
		}},
		Banks: []BankTxn{{ID: "bk1", UTR: "HDFC123", CreditMinor: 9728, ValueDate: valueDate}},
		Result: &FinancialResult{
			RunID: "run_1", Result: ResultMatched, Reason: "payout_processed_bank",
			ExpectedAmount: 10000, ObservedAmount: 9728, CreatedAt: reconAt,
			EvidenceRefs: EvidenceRefs{BankObservationID: "bk1", SettlementLineID: "sl1"},
		},
	})

	if len(out.Steps) != 14 {
		t.Fatalf("steps=%d", len(out.Steps))
	}
	if out.Reconciliation == nil || out.Reconciliation.Result != ResultMatched {
		t.Fatalf("reconciliation=%v", out.Reconciliation)
	}

	assertStep := func(seq int, kind string, captured bool, at *time.Time, source string) {
		t.Helper()
		st := out.Steps[seq-1]
		if st.Kind != kind {
			t.Fatalf("seq %d kind=%s want %s", seq, st.Kind, kind)
		}
		if st.Captured != captured {
			t.Fatalf("seq %d captured=%v", seq, st.Captured)
		}
		if captured {
			if st.CapturedAt == nil || !st.CapturedAt.Equal(*at) {
				t.Fatalf("seq %d captured_at=%v want %v", seq, st.CapturedAt, at)
			}
			if source != "" && st.Source != source {
				t.Fatalf("seq %d source=%s want %s", seq, st.Source, source)
			}
		} else if st.CapturedAt != nil {
			t.Fatalf("seq %d missing step has captured_at=%v", seq, st.CapturedAt)
		}
	}

	assertStep(1, TimelineMerchantInstruction, false, nil, "")
	assertStep(2, TimelineRazorpayCreated, true, &t1, "webhook")
	assertStep(3, TimelineValidation, false, nil, "")
	assertStep(4, TimelineGovernance, false, nil, "")
	assertStep(5, TimelineRouting, false, nil, "")
	assertStep(6, TimelineSubmitted, true, &t1, "webhook")
	assertStep(7, TimelineProviderResponse, true, &t1, "webhook")
	assertStep(8, TimelineStatusUpdates, true, &t3, "webhook")
	assertStep(9, TimelineUTR, true, &t3, "webhook")
	assertStep(10, TimelineSettlement, true, &settled, "settlement_file")
	assertStep(11, TimelineBankCash, true, &valueDate, "bank_csv")
	assertStep(12, TimelineReconRun, true, &reconAt, "reconciliation")
	assertStep(13, TimelineReconDecision, true, &reconAt, "reconciliation")
	assertStep(14, TimelineEvidence, true, &reconAt, "reconciliation")

	if out.Steps[1].ProviderAt == nil || !out.Steps[1].ProviderAt.Equal(created) {
		t.Fatalf("provider_at=%v", out.Steps[1].ProviderAt)
	}
	if len(out.Steps[7].Events) != 3 {
		t.Fatalf("status events=%d", len(out.Steps[7].Events))
	}
	if !out.Steps[7].Events[1].CapturedAt.Equal(t2) {
		t.Fatalf("status event 2 = %v", out.Steps[7].Events[1].CapturedAt)
	}
}

func TestBuildEntityTimelineMissingStepsStayUncaptured(t *testing.T) {
	t1 := time.Date(2026, 4, 2, 8, 0, 0, 0, time.UTC)
	out := BuildEntityTimeline(timelineFacts{
		EntityType:     EntityPayout,
		EntityID:       "pout_fail",
		ProviderStatus: "failed",
		Events: []ObservationFact{
			{Source: "webhook", ProviderStatus: "failed", SourceEventID: "evt_f", ObservedAt: t1},
		},
	})
	if out.ReconRun || out.Reconciliation != nil {
		t.Fatalf("recon should be null before a run")
	}
	for _, seq := range []int{1, 3, 4, 5, 10, 11, 12, 13, 14} {
		st := out.Steps[seq-1]
		if st.Captured {
			t.Fatalf("seq %d should not be captured", seq)
		}
		if st.CapturedAt != nil {
			t.Fatalf("seq %d invented time %v", seq, st.CapturedAt)
		}
	}
	if !out.Steps[1].CapturedAt.Equal(t1) {
		t.Fatalf("created time=%v", out.Steps[1].CapturedAt)
	}
}

func TestBuildEntityTimelineAbsentBankUsesReconTime(t *testing.T) {
	reconAt := time.Date(2026, 4, 3, 9, 0, 0, 0, time.UTC)
	out := BuildEntityTimeline(timelineFacts{
		EntityType:     EntityPayout,
		EntityID:       "pout_gap",
		ProviderStatus: "processed",
		UTR:            "UTR1",
		Result: &FinancialResult{
			Result: ResultUnresolved, Reason: "payout_missing_bank", CreatedAt: reconAt,
		},
	})
	st := out.Steps[10]
	if !st.Captured || st.CapturedAt == nil || !st.CapturedAt.Equal(reconAt) {
		t.Fatalf("absent bank=%+v", st)
	}
	found, _ := st.Detail["found"].(bool)
	if found {
		t.Fatalf("found should be false")
	}
}
