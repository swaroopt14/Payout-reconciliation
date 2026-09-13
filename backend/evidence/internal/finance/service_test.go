package finance

import (
	"context"
	"strings"
	"testing"
)

func failedBankEvent() DecisionEvent {
	return DecisionEvent{
		EventID: "evt_1", TenantID: "tenant-a", RunID: "run_1",
		EntityType: "payment", EntityID: "pay_123", Status: "failed",
		Result: "UNRESOLVED", Reason: "failed_with_bank_movement",
		ExpectedAmount: 10000, ObservedAmount: 10000, VarianceAmount: 10000, Currency: "INR",
		EvidenceRefs: EvidenceRefs{
			CanonicalPaymentID: "cp_1", ObservationEventIDs: []string{"wh_failed"},
			PayloadHashes: []string{"sha256:wh"}, BankObservationID: "bank_1",
			PaymentAmountMinor: 10000,
		},
		CandidateIDs: []string{"bank_1", "bank_other"},
	}
}

func TestIngestCreatesSourceEvidenceAndAbsentSearch(t *testing.T) {
	svc := NewService(NewMemoryStore())
	list, err := svc.IngestDecision(context.Background(), failedBankEvent())
	if err != nil {
		t.Fatal(err)
	}
	types := map[string]int{}
	for _, e := range list {
		types[e.EvidenceType]++
		if e.SourceHash == "" || e.SourceType == "" || e.ObservedAt.IsZero() {
			t.Fatalf("incomplete provenance %+v", e)
		}
	}
	if types[TypePaymentRecord] != 1 || types[TypeBankTransaction] != 1 || types[TypeWebhookEvent] != 1 {
		t.Fatalf("types=%v", types)
	}
	if types[TypeAbsentSearch] == 0 {
		t.Fatal("expected absent settlement/refund search evidence")
	}
	if types[TypeSettlementRecord] != 0 {
		t.Fatal("must not invent settlement")
	}
}

func TestIngestIdempotent(t *testing.T) {
	svc := NewService(NewMemoryStore())
	ev := failedBankEvent()
	a, err := svc.IngestDecision(context.Background(), ev)
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.IngestDecision(context.Background(), ev)
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != len(b) {
		t.Fatalf("len a=%d b=%d", len(a), len(b))
	}
	decs, _ := svc.GetDecisions(context.Background(), ev.TenantID, ev.EntityType, ev.EntityID)
	if len(decs) != 1 {
		t.Fatalf("decisions=%d", len(decs))
	}
}

func TestDecisionKeepsRejectedCandidates(t *testing.T) {
	svc := NewService(NewMemoryStore())
	ev := failedBankEvent()
	if _, err := svc.IngestDecision(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	decs, _ := svc.GetDecisions(context.Background(), ev.TenantID, ev.EntityType, ev.EntityID)
	if len(decs) != 1 {
		t.Fatal(decs)
	}
	var rejected, selected int
	for _, c := range decs[0].Candidates {
		if c.Selected {
			selected++
		} else {
			rejected++
		}
	}
	if selected != 1 || rejected == 0 {
		t.Fatalf("selected=%d rejected=%d %+v", selected, rejected, decs[0].Candidates)
	}
	again, _ := svc.GetDecisions(context.Background(), ev.TenantID, ev.EntityType, ev.EntityID)
	if again[0].Decision != decs[0].Decision || again[0].Reason != decs[0].Reason {
		t.Fatal("decision not reproducible")
	}
}

func TestCalculationCopiesStructuredVariance(t *testing.T) {
	svc := NewService(NewMemoryStore())
	ev := failedBankEvent()
	if _, err := svc.IngestDecision(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	calcs, _ := svc.GetCalculations(context.Background(), ev.TenantID, ev.EntityType, ev.EntityID)
	if len(calcs) != 1 || calcs[0].Variance != 10000 || calcs[0].Output != 10000 {
		t.Fatalf("%+v", calcs)
	}
}

func TestIntegrityValidAndTamperedInvalid(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store)
	ev := failedBankEvent()
	list, err := svc.IngestDecision(context.Background(), ev)
	if err != nil {
		t.Fatal(err)
	}
	var pay Evidence
	for _, e := range list {
		if e.EvidenceType == TypePaymentRecord {
			pay = e
		}
	}
	res, err := svc.Verify(context.Background(), ev.TenantID, pay.ID)
	if err != nil || res.Integrity != IntegrityValid {
		t.Fatalf("%+v %v", res, err)
	}
	store.TamperSnapshot(pay.ID, map[string]any{"status": "captured", "amount_minor": 1})
	res, err = svc.Verify(context.Background(), ev.TenantID, pay.ID)
	if err != nil || res.Integrity != IntegrityInvalid {
		t.Fatalf("expected INVALID %+v %v", res, err)
	}
	missing, err := svc.Verify(context.Background(), ev.TenantID, "ev_nope")
	if err != nil || missing.Integrity != IntegrityUnknown {
		t.Fatalf("%+v %v", missing, err)
	}
}

func TestTenantIsolation(t *testing.T) {
	svc := NewService(NewMemoryStore())
	ev := failedBankEvent()
	list, _ := svc.IngestDecision(context.Background(), ev)
	_, _, ok, err := svc.GetEvidence(context.Background(), "tenant-b", list[0].ID)
	if err != nil || ok {
		t.Fatal("tenant B must not read tenant A evidence")
	}
	other, _ := svc.GetEntity(context.Background(), "tenant-b", "payment", "pay_123")
	if len(other) != 0 {
		t.Fatal(other)
	}
}

func TestPackUnknownRootCauseAndNoStatusChange(t *testing.T) {
	svc := NewService(NewMemoryStore())
	ev := failedBankEvent()
	ev.InvestigationID = "inv_123"
	ev.RootCause = "bank settlement failure proven"
	ev.FindingCertainty = CertaintyProven
	ev.Recommendation = "REQUEST_REVIEW"
	if _, err := svc.IngestDecision(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	pack, ok, err := svc.GetPack(context.Background(), ev.TenantID, "inv_123")
	if err != nil || !ok {
		t.Fatalf("pack ok=%v err=%v", ok, err)
	}
	inv, _ := pack.Document["investigation"].(map[string]any)
	if inv["root_cause"] != "UNKNOWN" || inv["certainty"] != CertaintyUnknown {
		t.Fatalf("must not turn UNKNOWN into PROVEN: %+v", inv)
	}
	pos, _ := pack.Document["financial_position"].(map[string]any)
	if pos["status"] != "failed" {
		t.Fatalf("status mutated %+v", pos)
	}
	if pos["status"] == "STUCK" {
		t.Fatal("status renamed")
	}
	if inv["financial_impact"] != int64(10000) && inv["financial_impact"] != float64(10000) {
		t.Fatalf("impact=%v", inv["financial_impact"])
	}
}

func TestPayoutEvidence(t *testing.T) {
	svc := NewService(NewMemoryStore())
	ev := DecisionEvent{
		TenantID: "tenant-a", EntityType: "payout", EntityID: "pout_1", Status: "processed",
		Result: "UNRESOLVED", Reason: "payout_missing_bank", ExpectedAmount: 25000, Currency: "INR",
		EvidenceRefs: EvidenceRefs{CanonicalPaymentID: "cpout"},
	}
	list, err := svc.IngestDecision(context.Background(), ev)
	if err != nil {
		t.Fatal(err)
	}
	var payout bool
	for _, e := range list {
		if e.EvidenceType == TypePayoutRecord {
			payout = true
		}
	}
	if !payout {
		t.Fatal("expected payout evidence")
	}
}

func TestSealRejectsUnresolvableSettlementRef(t *testing.T) {
	svc := NewService(NewMemoryStore())
	ev := failedBankEvent()
	if _, err := svc.IngestDecision(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	ev.InvestigationID = "inv_bad_setl"
	ev.EvidenceRefs.SettlementLineID = "sl_ghost"
	_, err := svc.SealInvestigation(context.Background(), ev)
	if err == nil || !strings.Contains(err.Error(), "unresolvable_settlement_ref") {
		t.Fatalf("err=%v", err)
	}
}

func TestSealRejectsUnresolvableBankRef(t *testing.T) {
	svc := NewService(NewMemoryStore())
	ev := DecisionEvent{
		TenantID: "tenant-a", EntityType: "payment", EntityID: "pay_nobank",
		Status: "captured", Result: "UNRESOLVED", Reason: "captured_missing_settlement",
		ExpectedAmount: 1000, Currency: "INR",
		EvidenceRefs: EvidenceRefs{CanonicalPaymentID: "cp_x"},
	}
	if _, err := svc.IngestDecision(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	ev.InvestigationID = "inv_bad_bank"
	ev.EvidenceRefs.BankObservationID = "bank_ghost"
	_, err := svc.SealInvestigation(context.Background(), ev)
	if err == nil || !strings.Contains(err.Error(), "unresolvable_bank_ref") {
		t.Fatalf("err=%v", err)
	}
}

func TestFabricatedEvidenceIDRejected(t *testing.T) {
	svc := NewService(NewMemoryStore())
	ev := failedBankEvent()
	if _, err := svc.IngestDecision(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	ev.InvestigationID = "inv_x"
	ev.CitedEvidenceIDs = []string{"ev_invented"}
	_, err := svc.SealInvestigation(context.Background(), ev)
	if err == nil || !strings.Contains(err.Error(), "fabricated_evidence_id") {
		t.Fatalf("err=%v", err)
	}
}

func TestSnapshotHashStable(t *testing.T) {
	a := SnapshotHash(map[string]any{"amount_minor": int64(1), "status": "failed"})
	b := SnapshotHash(map[string]any{"status": "failed", "amount_minor": int64(1)})
	if a != b || !strings.HasPrefix(a, "sha256:") {
		t.Fatalf("%s %s", a, b)
	}
}
