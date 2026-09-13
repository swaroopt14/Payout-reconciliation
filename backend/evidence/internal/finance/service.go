package finance

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Service struct {
	Store Store
	now   func() time.Time
}

func NewService(store Store) *Service {
	return &Service{Store: store, now: func() time.Time { return time.Now().UTC() }}
}

func (s *Service) IngestDecision(ctx context.Context, ev DecisionEvent) ([]Evidence, error) {
	if strings.TrimSpace(ev.TenantID) == "" || strings.TrimSpace(ev.EntityID) == "" {
		return nil, fmt.Errorf("tenant_id and entity_id are required")
	}
	if ev.EntityType == "" {
		ev.EntityType = "payment"
	}
	if ev.Currency == "" {
		ev.Currency = "INR"
	}
	now := s.now()
	corr := ev.RunID
	if corr == "" {
		corr = ev.EventID
	}

	var created []Evidence
	add := func(e Evidence, snap map[string]any) (Evidence, error) {
		if e.ID == "" {
			e.ID = "ev_" + uuid.Must(uuid.NewV7()).String()
		}
		e.CapturedAt = now
		if e.ObservedAt.IsZero() {
			e.ObservedAt = now
		}
		if e.CreatedAt.IsZero() {
			e.CreatedAt = now
		}
		if e.SourceHash == "" {
			e.SourceHash = SnapshotHash(snap)
		}
		st := Snapshot{
			ID: "snap_" + e.ID, EvidenceID: e.ID, SchemaVersion: "v1",
			Snapshot: snap, SnapshotHash: SnapshotHash(snap), CreatedAt: now,
		}
		got, _, err := s.Store.InsertEvidence(ctx, e, st)
		if err != nil {
			return Evidence{}, err
		}
		created = append(created, got)
		return got, nil
	}

	primaryType := TypePaymentRecord
	primarySrc := SrcRazorpayPayment
	if ev.EntityType == "payout" {
		primaryType = TypePayoutRecord
		primarySrc = SrcRazorpayPayout
	}
	sourceID := ev.EvidenceRefs.CanonicalPaymentID
	if sourceID == "" {
		sourceID = ev.EntityID
	}
	primary, err := add(Evidence{
		TenantID: ev.TenantID, EntityType: ev.EntityType, EntityID: ev.EntityID,
		EvidenceType: primaryType, SourceType: primarySrc, SourceID: sourceID,
		SourceReference: ev.EntityID, Role: RolePrimary, Authority: AuthAuthoritative,
	}, map[string]any{
		"entity_id": ev.EntityID, "status": ev.Status,
		"amount_minor": ev.ExpectedAmount, "currency": ev.Currency,
	})
	if err != nil {
		return nil, err
	}

	hashes := ev.EvidenceRefs.PayloadHashes
	for i, obsID := range ev.EvidenceRefs.ObservationEventIDs {
		h := ""
		if i < len(hashes) {
			h = hashes[i]
		}
		if _, err := add(Evidence{
			TenantID: ev.TenantID, EntityType: ev.EntityType, EntityID: ev.EntityID,
			EvidenceType: TypeWebhookEvent, SourceType: SrcRazorpayWebhook, SourceID: obsID,
			SourceHash: h, SourceReference: obsID, Role: RoleCorroborating, Authority: AuthAuthoritative,
		}, map[string]any{"source_event_id": obsID, "status": ev.Status}); err != nil {
			return nil, err
		}
	}

	if ev.EvidenceRefs.SettlementLineID != "" {
		setl, err := add(Evidence{
			TenantID: ev.TenantID, EntityType: ev.EntityType, EntityID: ev.EntityID,
			EvidenceType: TypeSettlementRecord, SourceType: SrcSettlement,
			SourceID: ev.EvidenceRefs.SettlementLineID, SourceReference: ev.EvidenceRefs.SettlementLineID,
			Role: RolePrimary, Authority: AuthAuthoritative,
		}, map[string]any{"settlement_line_id": ev.EvidenceRefs.SettlementLineID, "net_minor": ev.EvidenceRefs.SettlementNetMinor})
		if err != nil {
			return nil, err
		}
		_ = s.Store.InsertLink(ctx, Link{
			ID: "lnk_" + uuid.Must(uuid.NewV7()).String(), TenantID: ev.TenantID,
			EvidenceID: primary.ID, RelatedEvidenceID: setl.ID, Relationship: "SETTLED_BY", CreatedAt: now,
		})
	} else if ev.Reason == "captured_missing_settlement" || ev.Reason == "failed_with_bank_movement" || ev.Reason == "payout_missing_bank" {
		if _, err := add(Evidence{
			TenantID: ev.TenantID, EntityType: ev.EntityType, EntityID: ev.EntityID,
			EvidenceType: TypeAbsentSearch, SourceType: SrcSettlement, SourceID: ev.EntityID + ":settlement_absent",
			SourceReference: "settlement", Role: RoleDecisionEvidence, Authority: AuthDerived,
		}, map[string]any{"searched": "settlement", "found": false}); err != nil {
			return nil, err
		}
	}

	if ev.EvidenceRefs.BankObservationID != "" {
		bank, err := add(Evidence{
			TenantID: ev.TenantID, EntityType: ev.EntityType, EntityID: ev.EntityID,
			EvidenceType: TypeBankTransaction, SourceType: SrcBank,
			SourceID: ev.EvidenceRefs.BankObservationID, SourceReference: ev.EvidenceRefs.BankObservationID,
			Role: RoleCorroborating, Authority: AuthAuthoritative,
		}, map[string]any{"bank_observation_id": ev.EvidenceRefs.BankObservationID, "amount_minor": ev.ObservedAmount})
		if err != nil {
			return nil, err
		}
		_ = s.Store.InsertLink(ctx, Link{
			ID: "lnk_" + uuid.Must(uuid.NewV7()).String(), TenantID: ev.TenantID,
			EvidenceID: primary.ID, RelatedEvidenceID: bank.ID, Relationship: "BANK_MOVEMENT", CreatedAt: now,
		})
	} else if ev.Reason == "settlement_without_bank" || ev.Reason == "payout_missing_bank" {
		if _, err := add(Evidence{
			TenantID: ev.TenantID, EntityType: ev.EntityType, EntityID: ev.EntityID,
			EvidenceType: TypeAbsentSearch, SourceType: SrcBank, SourceID: ev.EntityID + ":bank_absent",
			SourceReference: "bank", Role: RoleDecisionEvidence, Authority: AuthDerived,
		}, map[string]any{"searched": "bank", "found": false}); err != nil {
			return nil, err
		}
	}

	if ev.Reason == "failed_with_bank_movement" || ev.Reason == "payout_failed_with_bank_movement" {
		if _, err := add(Evidence{
			TenantID: ev.TenantID, EntityType: ev.EntityType, EntityID: ev.EntityID,
			EvidenceType: TypeAbsentSearch, SourceType: SrcSettlement, SourceID: ev.EntityID + ":refund_absent",
			SourceReference: "refund", Role: RoleDecisionEvidence, Authority: AuthDerived,
		}, map[string]any{"searched": "refund", "found": false}); err != nil {
			return nil, err
		}
	}

	if _, err := add(Evidence{
		TenantID: ev.TenantID, EntityType: ev.EntityType, EntityID: ev.EntityID,
		EvidenceType: TypeReconciliationResult, SourceType: SrcReconciliation,
		SourceID: firstNonEmpty(ev.EventID, ev.RunID, ev.EntityID+":recon"),
		SourceReference: ev.Result, Role: RoleDecisionEvidence, Authority: AuthDerived,
	}, map[string]any{"result": ev.Result, "reason": ev.Reason, "status": ev.Status}); err != nil {
		return nil, err
	}

	cands := CandidatesFromDecision(ev)
	if existing, _ := s.Store.ListDecisions(ctx, ev.TenantID, ev.EntityType, ev.EntityID); len(existing) == 0 ||
		existing[len(existing)-1].Decision != ev.Result || existing[len(existing)-1].Reason != ev.Reason {
	if _, err := s.Store.InsertDecision(ctx, DecisionTrace{
		ID: "dec_" + uuid.Must(uuid.NewV7()).String(), TenantID: ev.TenantID,
		EntityType: ev.EntityType, EntityID: ev.EntityID, DecisionType: "reconciliation",
		Decision: ev.Result, Reason: ev.Reason, Rules: RulesFromDecision(ev),
		Candidates: cands, SelectedCandidate: selectedCandidate(cands), CreatedAt: now,
	}); err != nil {
		return nil, err
	}

	}
	if existing, _ := s.Store.ListCalculations(ctx, ev.TenantID, ev.EntityType, ev.EntityID); len(existing) == 0 ||
		existing[len(existing)-1].Variance != ev.VarianceAmount {
	if _, err := s.Store.InsertCalculation(ctx, CalculationTrace{
		ID: "calc_" + uuid.Must(uuid.NewV7()).String(), TenantID: ev.TenantID,
		EntityType: ev.EntityType, EntityID: ev.EntityID,
		Formula: "structured_variance_amount",
		Inputs: map[string]any{
			"expected": ev.ExpectedAmount, "observed": ev.ObservedAmount,
			"settlement_net": ev.EvidenceRefs.SettlementNetMinor,
		},
		Output: ev.VarianceAmount, Actual: ev.ObservedAmount, Variance: ev.VarianceAmount,
		Currency: ev.Currency, Precision: "minor", CreatedAt: now,
	}); err != nil {
		return nil, err
	}
	}

	ids := make([]string, 0, len(created))
	for _, e := range created {
		ids = append(ids, e.ID)
	}
	_ = s.Store.InsertAudit(ctx, AuditEvent{
		ID: "aud_" + uuid.Must(uuid.NewV7()).String(), TenantID: ev.TenantID,
		ActorType: ActorSystem, Action: ActionReconRun, EntityType: ev.EntityType, EntityID: ev.EntityID,
		EvidenceIDs: ids, CorrelationID: corr, AfterState: map[string]any{"result": ev.Result, "reason": ev.Reason},
		CreatedAt: now,
	})

	if ev.InvestigationID != "" {
		if _, err := s.SealInvestigation(ctx, ev); err != nil {
			return created, err
		}
	}
	return created, nil
}

func (s *Service) SealInvestigation(ctx context.Context, ev DecisionEvent) (Pack, error) {
	if ev.InvestigationID == "" {
		ev.InvestigationID = "inv_" + ev.EntityID
	}
	now := s.now()
	list, err := s.Store.ListEvidence(ctx, ev.TenantID, ev.EntityType, ev.EntityID)
	if err != nil {
		return Pack{}, err
	}
	if err := validateSealRefs(ev, list); err != nil {
		return Pack{}, err
	}
	if ev.CitedEvidenceIDs != nil {
		allowed := map[string]struct{}{}
		for _, e := range list {
			allowed[e.ID] = struct{}{}
		}
		for _, id := range ev.CitedEvidenceIDs {
			if !strings.HasPrefix(id, "ev_") {
				continue
			}
			if _, ok := allowed[id]; !ok {
				return Pack{}, fmt.Errorf("fabricated_evidence_id")
			}
		}
	}
	for _, e := range list {
		_ = s.Store.AttachInvestigation(ctx, InvestigationLink{
			InvestigationID: ev.InvestigationID, EvidenceID: e.ID, Role: e.Role, CreatedAt: now,
		})
	}

	certainty := FindingCertainty(ev)
	root := ev.RootCause
	if root == "" {
		root = "UNKNOWN"
	}
	if (strings.EqualFold(ev.FindingCertainty, CertaintyProven) || strings.Contains(strings.ToLower(root), "proven")) &&
		(ev.Reason == "failed_with_bank_movement" || ev.Reason == "payout_failed_with_bank_movement") {
		root = "UNKNOWN"
		certainty = CertaintyUnknown
	}

	calcs, _ := s.Store.ListCalculations(ctx, ev.TenantID, ev.EntityType, ev.EntityID)
	impact := ev.VarianceAmount
	if len(calcs) > 0 {
		impact = calcs[len(calcs)-1].Variance
	}

	decs, _ := s.Store.ListDecisions(ctx, ev.TenantID, ev.EntityType, ev.EntityID)
	audit, _ := s.Store.ListAudit(ctx, ev.TenantID, ev.EntityType, ev.EntityID)
	absent := absentTypes(list)
	src := make([]map[string]any, 0, len(list))
	ids := make([]string, 0, len(list))
	for _, e := range list {
		ids = append(ids, e.ID)
		src = append(src, map[string]any{
			"evidence_id": e.ID, "evidence_type": e.EvidenceType, "source_type": e.SourceType,
			"source_id": e.SourceID, "source_hash": e.SourceHash, "role": e.Role, "authority": e.Authority,
		})
	}
	var matching any
	if len(decs) > 0 {
		matching = decs[len(decs)-1]
	}
	doc := map[string]any{
		"entity": map[string]any{"type": ev.EntityType, "id": ev.EntityID},
		"financial_position": map[string]any{
			"status": ev.Status, "reconciliation": ev.Result, "reason": ev.Reason,
			"exposure_minor": impact, "bank_credit_proven": ev.BankCreditProven,
		},
		"source_evidence": src,
		"absent":          absent,
		"calculations":    calcs,
		"matching_decision": matching,
		"investigation": map[string]any{
			"investigation_id": ev.InvestigationID,
			"root_cause":       root,
			"certainty":        certainty,
			"confidence":       ev.VarianceAmount,
			"recommendation":   ev.Recommendation,
			"financial_impact": impact,
			"confidence_basis": ConfidenceBasis(ev),
		},
		"audit_trail": audit,
		"integrity":   map[string]any{"algorithm": "SHA-256", "status": IntegrityValid},
	}
	// confidence in investigation should not be variance; use last decision confidence via metadata
	inv := doc["investigation"].(map[string]any)
	inv["confidence"] = findingConfidence(ev)
	doc["investigation"] = inv
	pack := Pack{
		ID: "fpack_" + uuid.Must(uuid.NewV7()).String(), TenantID: ev.TenantID,
		InvestigationID: ev.InvestigationID, EntityType: ev.EntityType, EntityID: ev.EntityID,
		Document: doc, PackHash: PackHash(doc), CreatedAt: now,
	}
	saved, err := s.Store.UpsertPack(ctx, pack)
	if err != nil {
		return Pack{}, err
	}
	_ = s.Store.InsertAudit(ctx, AuditEvent{
		ID: "aud_" + uuid.Must(uuid.NewV7()).String(), TenantID: ev.TenantID,
		ActorType: ActorSystem, Action: ActionPackSealed, EntityType: ev.EntityType, EntityID: ev.EntityID,
		EvidenceIDs: ids, CorrelationID: ev.InvestigationID, CreatedAt: now,
	})
	return saved, nil
}

func (s *Service) Verify(ctx context.Context, tenantID, evidenceID string) (VerifyResult, error) {
	ev, snap, ok, err := s.Store.GetEvidence(ctx, tenantID, evidenceID)
	if err != nil {
		return VerifyResult{}, err
	}
	if !ok {
		return VerifyResult{EvidenceID: evidenceID, Integrity: IntegrityUnknown}, nil
	}
	current := SnapshotHash(snap.Snapshot)
	status := IntegrityValid
	if current != snap.SnapshotHash {
		status = IntegrityInvalid
	}
	_ = ev
	return VerifyResult{EvidenceID: evidenceID, Integrity: status, StoredHash: snap.SnapshotHash, CurrentHash: current}, nil
}

func (s *Service) GetEntity(ctx context.Context, tenantID, entityType, entityID string) ([]Evidence, error) {
	return s.Store.ListEvidence(ctx, tenantID, entityType, entityID)
}

func (s *Service) GetEvidence(ctx context.Context, tenantID, evidenceID string) (Evidence, Snapshot, bool, error) {
	return s.Store.GetEvidence(ctx, tenantID, evidenceID)
}

func (s *Service) GetPack(ctx context.Context, tenantID, investigationID string) (Pack, bool, error) {
	return s.Store.GetPackByInvestigation(ctx, tenantID, investigationID)
}

func (s *Service) GetAudit(ctx context.Context, tenantID, entityType, entityID string) ([]AuditEvent, error) {
	return s.Store.ListAudit(ctx, tenantID, entityType, entityID)
}

func (s *Service) GetDecisions(ctx context.Context, tenantID, entityType, entityID string) ([]DecisionTrace, error) {
	return s.Store.ListDecisions(ctx, tenantID, entityType, entityID)
}

func (s *Service) GetCalculations(ctx context.Context, tenantID, entityType, entityID string) ([]CalculationTrace, error) {
	return s.Store.ListCalculations(ctx, tenantID, entityType, entityID)
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func selectedCandidate(cands []Candidate) string {
	for _, c := range cands {
		if c.Selected {
			return c.Candidate
		}
	}
	return ""
}

func validateSealRefs(ev DecisionEvent, list []Evidence) error {
	has := func(sourceID, evidenceType string) bool {
		sourceID = strings.TrimSpace(sourceID)
		if sourceID == "" {
			return true
		}
		for _, e := range list {
			if e.SourceID == sourceID && e.EvidenceType == evidenceType {
				return true
			}
		}
		return false
	}
	if !has(ev.EvidenceRefs.SettlementLineID, TypeSettlementRecord) {
		return fmt.Errorf("unresolvable_settlement_ref")
	}
	if !has(ev.EvidenceRefs.BankObservationID, TypeBankTransaction) {
		return fmt.Errorf("unresolvable_bank_ref")
	}
	return nil
}

func absentTypes(list []Evidence) []string {
	have := map[string]bool{}
	for _, e := range list {
		if e.EvidenceType == TypeAbsentSearch {
			if e.SourceReference != "" {
				have["ABSENT_"+strings.ToUpper(e.SourceReference)] = true
			}
			continue
		}
		have[e.EvidenceType] = true
	}
	var missing []string
	for _, t := range []string{TypeSettlementRecord, TypeRefundLine} {
		if !have[t] {
			missing = append(missing, t)
		}
	}
	return missing
}

func findingConfidence(ev DecisionEvent) float64 {
	switch ev.Result {
	case "MATCHED":
		return 0.96
	case "AMBIGUOUS":
		return 0.55
	case "UNRESOLVED":
		return 0.82
	default:
		return 0.7
	}
}
