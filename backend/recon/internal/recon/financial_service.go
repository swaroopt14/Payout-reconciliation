package recon

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"zord-outcome-engine/models"

	"github.com/google/uuid"
)

// ErrRunInProgress is returned when another reconciliation run already holds
// the tenant+connector lock. Callers should treat this as success-for-scheduler
// / 409-for-operators, not as a failed close.
var ErrRunInProgress = errors.New("reconciliation already running for tenant")

type FinancialStore interface {
	ListCanonicalPayments(ctx context.Context, tenantID, connectorID string) ([]PaymentFact, error)
	GetCanonicalPayment(ctx context.Context, tenantID, connectorID, paymentID string) (PaymentFact, bool, error)
	ListObservationEvents(ctx context.Context, tenantID, connectorID, paymentID string) ([]ObservationFact, error)
	ListCanonicalPayouts(ctx context.Context, tenantID, connectorID string) ([]PayoutFact, error)
	GetCanonicalPayoutFact(ctx context.Context, tenantID, connectorID, payoutID string) (PayoutFact, bool, error)
	ListPayoutObservationFacts(ctx context.Context, tenantID, connectorID, payoutID string) ([]ObservationFact, error)
	ListSettlementLines(ctx context.Context, tenantID, connectorID string) ([]SettlementLine, error)
	ListBankTxns(ctx context.Context, tenantID, connectorID, accountID string) ([]BankTxn, error)
	ListSettlementBankDecisions(ctx context.Context, tenantID, connectorID string) ([]SettlementBankDecision, error)
	ListRefunds(ctx context.Context, tenantID, connectorID, paymentID string) ([]RefundFact, error)
	UpsertRefund(ctx context.Context, tenantID, connectorID string, r RefundFact) (RefundFact, error)
	TryLockTenantRun(ctx context.Context, tenantID, connectorID string) (unlock func(), err error)
	InsertReconciliationRun(ctx context.Context, run ReconciliationRun) (ReconciliationRun, error)
	CompleteReconciliationRun(ctx context.Context, run ReconciliationRun) error
	GetReconciliationRun(ctx context.Context, tenantID, runID string) (ReconciliationRun, error)
	GetLatestReconciliationRunByBatch(ctx context.Context, tenantID, connectorID, batchID string) (ReconciliationRun, bool, error)
	UpsertReconciliationResult(ctx context.Context, tenantID, connectorID, runID string, r FinancialResult) (FinancialResult, error)
	GetReconciliationResult(ctx context.Context, tenantID, connectorID, entityType, entityID string) (FinancialResult, bool, error)
	InsertReconciliationException(ctx context.Context, tenantID, connectorID, runID string, ex ReconciliationException) (ReconciliationException, error)
	ListReconciliationExceptions(ctx context.Context, tenantID, connectorID string) ([]ReconciliationException, error)
	ListReconciliationResults(ctx context.Context, tenantID, connectorID string) ([]FinancialResult, error)
	GetReconciliationException(ctx context.Context, tenantID, connectorID, id string) (ReconciliationException, bool, error)
	InsertInvestigation(ctx context.Context, rec InvestigationRecord) (InvestigationRecord, error)
	GetInvestigation(ctx context.Context, tenantID, connectorID, id string) (InvestigationRecord, bool, error)
	ListInvestigations(ctx context.Context, tenantID, connectorID string) ([]InvestigationRecord, error)
	InsertMatchOutbox(ctx context.Context, row models.OutboxRow) error
}

type FinancialService struct {
	Store FinancialStore
	now   func() time.Time
}

func NewFinancialService(store FinancialStore) *FinancialService {
	return &FinancialService{Store: store, now: func() time.Time { return time.Now().UTC() }}
}

type FinancialRunRequest struct {
	TenantID    string
	ConnectorID string
	AccountID   string
	BatchID     string
	PayoutIDs   []string
}

func (s *FinancialService) Run(ctx context.Context, req FinancialRunRequest) (ReconciliationRun, []FinancialResult, error) {
	unlock, err := s.Store.TryLockTenantRun(ctx, req.TenantID, req.ConnectorID)
	if err != nil {
		return ReconciliationRun{}, nil, err
	}
	defer unlock()

	now := s.now()
	run, err := s.Store.InsertReconciliationRun(ctx, ReconciliationRun{
		TenantID:    req.TenantID,
		ConnectorID: req.ConnectorID,
		AccountID:   req.AccountID,
		BatchID:     strings.TrimSpace(req.BatchID),
		Status:      "running",
		Counts:      map[string]int{},
		CreatedAt:   now,
	})
	if err != nil {
		return ReconciliationRun{}, nil, err
	}

	pays, err := s.Store.ListCanonicalPayments(ctx, req.TenantID, req.ConnectorID)
	if err != nil {
		return run, nil, err
	}
	lines, err := s.Store.ListSettlementLines(ctx, req.TenantID, req.ConnectorID)
	if err != nil {
		return run, nil, err
	}
	banks, err := s.Store.ListBankTxns(ctx, req.TenantID, req.ConnectorID, req.AccountID)
	if err != nil {
		return run, nil, err
	}
	decisions, err := s.Store.ListSettlementBankDecisions(ctx, req.TenantID, req.ConnectorID)
	if err != nil {
		return run, nil, err
	}
	allRefunds, err := s.Store.ListRefunds(ctx, req.TenantID, req.ConnectorID, "")
	if err != nil {
		return run, nil, err
	}

	wantPayout := payoutIDSet(req.PayoutIDs)
	scoped := len(wantPayout) > 0 || strings.TrimSpace(req.BatchID) != ""
	pays = scopePayments(pays, req.BatchID)

	linesByPay := indexSettlementByPayment(lines)
	lineByID := map[string]SettlementLine{}
	for _, l := range lines {
		if id := lineID(l); id != "" {
			lineByID[id] = l
		}
	}
	decisionsByLine := map[string][]SettlementBankDecision{}
	bankByID := map[string]BankTxn{}
	for _, b := range banks {
		bankByID[b.ID] = b
	}
	for _, d := range decisions {
		if d.SettlementLineID != "" {
			decisionsByLine[d.SettlementLineID] = append(decisionsByLine[d.SettlementLineID], d)
		}
	}

	results := make([]FinancialResult, 0, len(pays)+4)
	usedBanks := map[string]struct{}{}

	for _, pay := range pays {
		events, err := s.Store.ListObservationEvents(ctx, req.TenantID, req.ConnectorID, pay.PaymentID)
		if err != nil {
			return run, nil, err
		}
		payLines := linesByPay[pay.PaymentID]
		var payDecisions []SettlementBankDecision
		for _, l := range payLines {
			payDecisions = append(payDecisions, decisionsByLine[lineID(l)]...)
		}
		related := relatedBanks(pay, events, banks, payDecisions)
		refunds := refundsFor(allRefunds, pay.PaymentID)
		fr := ReconcilePayment(FinancialInput{
			Payment:    pay,
			Events:     events,
			Lines:      payLines,
			Decisions:  payDecisions,
			Banks:      related,
			Refunds:    refunds,
			Now:        now,
			StuckAfter: DefaultStuckAfter,
		})
		markUsedBanks(usedBanks, fr)
		results = append(results, fr)
	}

	payouts, err := s.Store.ListCanonicalPayouts(ctx, req.TenantID, req.ConnectorID)
	if err != nil {
		return run, nil, err
	}
	payouts = scopePayouts(payouts, req.BatchID, wantPayout)
	for _, po := range payouts {
		events, err := s.Store.ListPayoutObservationFacts(ctx, req.TenantID, req.ConnectorID, po.PayoutID)
		if err != nil {
			return run, nil, err
		}
		related := relatedPayoutBanks(po, events, banks)
		fr := ReconcilePayout(PayoutInput{
			Payout: po, Events: events, Banks: related, Now: now, StuckAfter: DefaultPayoutSLA,
		})
		markUsedBanks(usedBanks, fr)
		results = append(results, fr)
	}

	results = applySharedBankAmbiguity(results)

	if !scoped {
		for _, d := range decisions {
			if d.State != BankMatchOrphanBank {
				continue
			}
			b, ok := bankByID[d.BankObservationID]
			if !ok {
				continue
			}
			if _, used := usedBanks[b.ID]; used {
				continue
			}
			fr := OrphanBankResult(b)
			fr.EvidenceRefs.SettlementBankDecisionID = d.ID
			if fr.Exception != nil {
				fr.Exception.EvidenceRefs = fr.EvidenceRefs
				fr.Exception.EvidenceIDs = EvidenceIDList(fr.EvidenceRefs)
			}
			markUsedBanks(usedBanks, fr)
			results = append(results, fr)
		}
		for _, b := range UnusedCreditBanks(banks, usedBanks) {
			fr := OrphanBankResult(b)
			markUsedBanks(usedBanks, fr)
			results = append(results, fr)
		}
	}

	counts := map[string]int{}
	matched := 0
	exceptions := 0
	persisted := make([]FinancialResult, 0, len(results))
	for _, fr := range results {
		saved, err := s.Store.UpsertReconciliationResult(ctx, req.TenantID, req.ConnectorID, run.ID, fr)
		if err != nil {
			return run, nil, err
		}
		if saved.Exception != nil && NeedsInvestigation(saved) {
			ex := *saved.Exception
			ex.TenantID = req.TenantID
			ex.ConnectorID = req.ConnectorID
			ex.RunID = run.ID
			if _, err := s.Store.InsertReconciliationException(ctx, req.TenantID, req.ConnectorID, run.ID, ex); err != nil {
				return run, nil, err
			}
			exceptions++
		}
		if saved.Result == ResultMatched {
			matched++
		}
		counts[saved.Result]++
		if err := s.emitDecision(ctx, req.TenantID, req.ConnectorID, run.ID, saved); err != nil {
			return run, nil, err
		}
		persisted = append(persisted, saved)
	}

	run.Status = "completed"
	run.PaymentCount = len(pays) + len(payouts)
	run.MatchedCount = matched
	run.ExceptionCount = exceptions
	run.Counts = counts
	run.CompletedAt = s.now()
	ids := make([]string, 0, len(persisted))
	for _, fr := range persisted {
		ids = append(ids, fr.EntityID)
	}
	run.EntityIDs = ids
	if err := s.Store.CompleteReconciliationRun(ctx, run); err != nil {
		return run, persisted, err
	}
	return run, persisted, nil
}

func (s *FinancialService) GetPayment(ctx context.Context, tenantID, connectorID, paymentID string) (PaymentFact, FinancialResult, bool, error) {
	pay, ok, err := s.Store.GetCanonicalPayment(ctx, tenantID, connectorID, paymentID)
	if err != nil || !ok {
		return pay, FinancialResult{}, ok, err
	}
	fr, found, err := s.Store.GetReconciliationResult(ctx, tenantID, connectorID, EntityPayment, paymentID)
	if err != nil {
		return pay, FinancialResult{}, false, err
	}
	if !found {
		fr := FinancialResult{EntityType: EntityPayment, EntityID: paymentID, Status: pay.CanonicalStatus, Result: ResultUnresolved, Reason: "not_run", Rail: NormalizeRail(pay.Method)}
		AnnotateCashFlow(&fr)
		return pay, fr, true, nil
	}
	if fr.Rail == "" {
		fr.Rail = NormalizeRail(pay.Method)
	}
	if fr.EntityType == "" {
		fr.EntityType = EntityPayment
	}
	AnnotateCashFlow(&fr)
	return pay, fr, true, nil
}

func (s *FinancialService) GetPayout(ctx context.Context, tenantID, connectorID, payoutID string) (PayoutFact, FinancialResult, bool, error) {
	po, ok, err := s.Store.GetCanonicalPayoutFact(ctx, tenantID, connectorID, payoutID)
	if err != nil || !ok {
		return po, FinancialResult{}, ok, err
	}
	fr, found, err := s.Store.GetReconciliationResult(ctx, tenantID, connectorID, EntityPayout, payoutID)
	if err != nil {
		return po, FinancialResult{}, false, err
	}
	if !found {
		fr := FinancialResult{EntityType: EntityPayout, EntityID: payoutID, Status: po.ProviderStatus, Result: ResultUnresolved, Reason: "not_run", Rail: NormalizeRail(po.Mode)}
		AnnotateCashFlow(&fr)
		return po, fr, true, nil
	}
	if fr.Rail == "" {
		fr.Rail = NormalizeRail(po.Mode)
	}
	if fr.EntityType == "" {
		fr.EntityType = EntityPayout
	}
	AnnotateCashFlow(&fr)
	return po, fr, true, nil
}

func (s *FinancialService) PayoutTimeline(ctx context.Context, tenantID, connectorID, payoutID string) (EntityTimeline, bool, error) {
	po, ok, err := s.Store.GetCanonicalPayoutFact(ctx, tenantID, connectorID, payoutID)
	if err != nil || !ok {
		return EntityTimeline{}, ok, err
	}
	events, err := s.Store.ListPayoutObservationFacts(ctx, tenantID, connectorID, po.PayoutID)
	if err != nil {
		return EntityTimeline{}, false, err
	}
	return s.buildTimeline(ctx, tenantID, connectorID, timelineFacts{
		EntityType:        EntityPayout,
		EntityID:          po.PayoutID,
		ProviderStatus:    po.ProviderStatus,
		AmountMinor:       po.AmountMinor,
		Currency:          po.Currency,
		UTR:               po.UTR,
		Mode:              po.Mode,
		ProviderCreatedAt: po.ProviderCreatedAt,
		FirstObservedAt:   po.FirstObservedAt,
		Events:            events,
	})
}

func (s *FinancialService) PaymentTimeline(ctx context.Context, tenantID, connectorID, paymentID string) (EntityTimeline, bool, error) {
	pay, ok, err := s.Store.GetCanonicalPayment(ctx, tenantID, connectorID, paymentID)
	if err != nil || !ok {
		return EntityTimeline{}, ok, err
	}
	events, err := s.Store.ListObservationEvents(ctx, tenantID, connectorID, pay.PaymentID)
	if err != nil {
		return EntityTimeline{}, false, err
	}
	return s.buildTimeline(ctx, tenantID, connectorID, timelineFacts{
		EntityType:        EntityPayment,
		EntityID:          pay.PaymentID,
		ProviderStatus:    pay.ProviderStatus,
		AmountMinor:       pay.AmountMinor,
		Currency:          pay.Currency,
		Mode:              pay.Method,
		ProviderCreatedAt: pay.ProviderCreatedAt,
		FirstObservedAt:   pay.FirstObservedAt,
		Events:            events,
	})
}

func (s *FinancialService) buildTimeline(ctx context.Context, tenantID, connectorID string, facts timelineFacts) (EntityTimeline, bool, error) {
	lines, err := s.Store.ListSettlementLines(ctx, tenantID, connectorID)
	if err != nil {
		return EntityTimeline{}, false, err
	}
	banks, err := s.Store.ListBankTxns(ctx, tenantID, connectorID, "")
	if err != nil {
		return EntityTimeline{}, false, err
	}
	fr, found, err := s.Store.GetReconciliationResult(ctx, tenantID, connectorID, facts.EntityType, facts.EntityID)
	if err != nil {
		return EntityTimeline{}, false, err
	}
	facts.Lines = lines
	facts.Banks = banks
	if found && fr.Reason != "not_run" {
		if fr.Rail == "" {
			fr.Rail = NormalizeRail(facts.Mode)
		}
		if fr.EntityType == "" {
			fr.EntityType = facts.EntityType
		}
		AnnotateCashFlow(&fr)
		facts.Result = &fr
	}
	return BuildEntityTimeline(facts), true, nil
}

func (s *FinancialService) Investigate(ctx context.Context, tenantID, connectorID, exceptionID, entityID string) (InvestigationRecord, error) {
	var ex ReconciliationException
	var ok bool
	var err error
	if exceptionID != "" {
		ex, ok, err = s.Store.GetReconciliationException(ctx, tenantID, connectorID, exceptionID)
		if err != nil {
			return InvestigationRecord{}, err
		}
		if !ok {
			return InvestigationRecord{}, errNotFound
		}
	} else {
		list, err := s.Store.ListReconciliationExceptions(ctx, tenantID, connectorID)
		if err != nil {
			return InvestigationRecord{}, err
		}
		for _, item := range list {
			if item.EntityID == entityID {
				ex = item
				ok = true
				break
			}
		}
		if !ok {
			return InvestigationRecord{}, errNotFound
		}
	}
	rec := DeterministicInvestigation(ex)
	rec.TenantID = tenantID
	rec.ConnectorID = connectorID
	rec.ExceptionID = ex.ID
	rec.FinancialImpact = ex.VarianceAmount
	rec.Confidence = ex.Confidence
	rec.EvidenceIDs = append([]string{}, ex.EvidenceIDs...)
	saved, err := s.Store.InsertInvestigation(ctx, rec)
	if err != nil {
		return saved, err
	}
	if err := s.emitInvestigation(ctx, tenantID, connectorID, saved, ex); err != nil {
		return saved, err
	}
	return saved, nil
}

func (s *FinancialService) FinanceSummary(ctx context.Context, tenantID, connectorID string) (FinanceSummary, error) {
	out := FinanceSummary{
		EntityCounts: map[string]int{},
		ResultCounts: map[string]int{},
		Currency:     "INR",
	}
	results, err := s.Store.ListReconciliationResults(ctx, tenantID, connectorID)
	if err != nil {
		return out, err
	}
	for _, r := range results {
		if r.EntityType != "" {
			out.EntityCounts[r.EntityType]++
		}
		if r.Result != "" {
			out.ResultCounts[r.Result]++
		}
	}
	out.MatchedCount = out.ResultCounts[ResultMatched]
	for _, k := range []string{ResultMatched, ResultAmbiguous, ResultUnresolved, ResultConflicted, ResultVariance, ResultOrphan} {
		out.ScoredCount += out.ResultCounts[k]
	}
	exceptions, err := s.Store.ListReconciliationExceptions(ctx, tenantID, connectorID)
	if err != nil {
		return out, err
	}
	byReason := map[string]*ReasonExposure{}
	for _, ex := range exceptions {
		out.ExposureMinor += ex.VarianceAmount
		key := ex.Reason
		if key == "" {
			key = "unspecified"
		}
		if _, ok := byReason[key]; !ok {
			byReason[key] = &ReasonExposure{Reason: key}
		}
		byReason[key].Count++
		byReason[key].ExposureMinor += ex.VarianceAmount
	}
	for _, v := range byReason {
		out.ExposureByReason = append(out.ExposureByReason, *v)
	}
	sort.Slice(out.ExposureByReason, func(i, j int) bool {
		if out.ExposureByReason[i].ExposureMinor == out.ExposureByReason[j].ExposureMinor {
			return out.ExposureByReason[i].Reason < out.ExposureByReason[j].Reason
		}
		return out.ExposureByReason[i].ExposureMinor > out.ExposureByReason[j].ExposureMinor
	})
	return out, nil
}

func (s *FinancialService) CashPosition(ctx context.Context, tenantID, connectorID string) (CashSnapshot, error) {
	results, err := s.Store.ListReconciliationResults(ctx, tenantID, connectorID)
	if err != nil {
		return CashSnapshot{}, err
	}
	lines, err := s.Store.ListSettlementLines(ctx, tenantID, connectorID)
	if err != nil {
		return CashSnapshot{}, err
	}
	exceptions, err := s.Store.ListReconciliationExceptions(ctx, tenantID, connectorID)
	if err != nil {
		return CashSnapshot{}, err
	}
	return CashPosition(results, lines, exceptions), nil
}

func (s *FinancialService) TaxBreakdown(ctx context.Context, tenantID, connectorID, paymentID string) (TaxBreakdown, error) {
	pay, fr, ok, err := s.GetPayment(ctx, tenantID, connectorID, paymentID)
	if err != nil {
		return TaxBreakdown{}, err
	}
	if !ok {
		return TaxBreakdown{}, errNotFound
	}
	lines, err := s.Store.ListSettlementLines(ctx, tenantID, connectorID)
	if err != nil {
		return TaxBreakdown{}, err
	}
	var payLines []SettlementLine
	for _, l := range lines {
		if l.PaymentID == paymentID || l.EntityID == paymentID {
			payLines = append(payLines, l)
		}
	}
	return TaxBreakdownFor(pay, payLines, fr), nil
}

func (s *FinancialService) CashSchedule(ctx context.Context, tenantID, connectorID string, days int) (CashSchedule, error) {
	results, err := s.Store.ListReconciliationResults(ctx, tenantID, connectorID)
	if err != nil {
		return CashSchedule{}, err
	}
	lines, err := s.Store.ListSettlementLines(ctx, tenantID, connectorID)
	if err != nil {
		return CashSchedule{}, err
	}
	payouts, err := s.Store.ListCanonicalPayouts(ctx, tenantID, connectorID)
	if err != nil {
		return CashSchedule{}, err
	}
	return BuildCashSchedule(results, lines, payouts, s.now(), days), nil
}

func (s *FinancialService) Ledger(ctx context.Context, tenantID, connectorID, paymentID string) (Ledger, error) {
	pay, fr, ok, err := s.GetPayment(ctx, tenantID, connectorID, paymentID)
	if err != nil {
		return Ledger{}, err
	}
	if !ok {
		return Ledger{}, errNotFound
	}
	lines, err := s.Store.ListSettlementLines(ctx, tenantID, connectorID)
	if err != nil {
		return Ledger{}, err
	}
	var payLines []SettlementLine
	for _, l := range lines {
		if l.PaymentID == paymentID || l.EntityID == paymentID {
			payLines = append(payLines, l)
		}
	}
	refunds, err := s.Store.ListRefunds(ctx, tenantID, connectorID, paymentID)
	if err != nil {
		return Ledger{}, err
	}
	return LedgerForPayment(pay, payLines, fr, refunds), nil
}

func (s *FinancialService) ListRefunds(ctx context.Context, tenantID, connectorID, paymentID string) ([]RefundFact, error) {
	return s.Store.ListRefunds(ctx, tenantID, connectorID, paymentID)
}

func refundsFor(all []RefundFact, paymentID string) []RefundFact {
	var out []RefundFact
	for _, r := range all {
		if r.PaymentID == paymentID {
			out = append(out, r)
		}
	}
	return out
}

func (s *FinancialService) emitDecision(ctx context.Context, tenantID, connectorID, runID string, fr FinancialResult) error {
	tid, err := uuid.Parse(tenantID)
	if err != nil {
		tid = uuid.Nil
	}
	eventID := uuid.Must(uuid.NewV7())
	payload, _ := json.Marshal(map[string]any{
		"event_type":         models.EventTypeReconDecisionV1,
		"event_id":           eventID.String(),
		"event_version":      models.EventVersionV1,
		"schema_version":     models.SchemaVersionV1,
		"rule_version":       FinancialRuleVersion,
		"tenant_id":          tenantID,
		"connector_id":       connectorID,
		"run_id":             runID,
		"entity_type":        fr.EntityType,
		"entity_id":          fr.EntityID,
		"status":             fr.Status,
		"result":             fr.Result,
		"expected_amount":    fr.ExpectedAmount,
		"observed_amount":    fr.ObservedAmount,
		"variance_amount":    fr.VarianceAmount,
		"bank_credit_proven": fr.BankCreditProven,
		"reason":             fr.Reason,
		"currency":           "INR",
		"candidate_ids":      fr.CandidateIDs,
		"evidence_refs":      fr.EvidenceRefs,
		"exception":          exceptionPayload(fr),
	})
	agg := uuid.Must(uuid.NewV7())
	if parsed, err := uuid.Parse(fr.EvidenceRefs.CanonicalPaymentID); err == nil {
		agg = parsed
	}
	return s.Store.InsertMatchOutbox(ctx, models.OutboxRow{
		EventID:       eventID,
		TenantID:      tid,
		AggregateType: "reconciliation_result",
		AggregateID:   agg,
		EventType:     models.EventTypeReconDecisionV1,
		Payload:       payload,
		CreatedAt:     s.now(),
	})
}

func (s *FinancialService) emitInvestigation(ctx context.Context, tenantID, connectorID string, rec InvestigationRecord, ex ReconciliationException) error {
	tid, err := uuid.Parse(tenantID)
	if err != nil {
		tid = uuid.Nil
	}
	root := rec.RootCause
	certainty := "LIKELY"
	if ex.Reason == "failed_with_bank_movement" || ex.Reason == "payout_failed_with_bank_movement" {
		root = "UNKNOWN"
		certainty = "UNKNOWN"
	}
	eventID := uuid.Must(uuid.NewV7())
	payload, _ := json.Marshal(map[string]any{
		"event_type":         models.EventTypeInvestigationCompletedV1,
		"event_id":           eventID.String(),
		"event_version":      models.EventVersionV1,
		"schema_version":     models.SchemaVersionV1,
		"tenant_id":          tenantID,
		"connector_id":       connectorID,
		"entity_type":        rec.EntityType,
		"entity_id":          rec.EntityID,
		"status":             ex.Status,
		"result":             ex.ReconciliationResult,
		"reason":             ex.Reason,
		"expected_amount":    ex.ExpectedAmount,
		"observed_amount":    ex.ObservedAmount,
		"variance_amount":    rec.FinancialImpact,
		"currency":           "INR",
		"candidate_ids":      ex.CandidateIDs,
		"evidence_refs":      ex.EvidenceRefs,
		"investigation_id":   rec.ID,
		"root_cause":         root,
		"recommendation":     rec.Recommendation,
		"finding_certainty":  certainty,
		"cited_evidence_ids": rec.EvidenceIDs,
	})
	agg := uuid.Must(uuid.NewV7())
	if parsed, err := uuid.Parse(rec.ID); err == nil {
		agg = parsed
	}
	return s.Store.InsertMatchOutbox(ctx, models.OutboxRow{
		EventID:       eventID,
		TenantID:      tid,
		AggregateType: "investigation",
		AggregateID:   agg,
		EventType:     models.EventTypeInvestigationCompletedV1,
		Payload:       payload,
		CreatedAt:     s.now(),
	})
}

func exceptionPayload(fr FinancialResult) map[string]any {
	if fr.Exception == nil {
		return nil
	}
	return map[string]any{
		"reason":          fr.Exception.Reason,
		"variance_amount": fr.Exception.VarianceAmount,
		"candidate_ids":   fr.Exception.CandidateIDs,
	}
}

func relatedBanks(pay PaymentFact, events []ObservationFact, banks []BankTxn, decisions []SettlementBankDecision) []BankTxn {
	ids := map[string]struct{}{}
	utrs := map[string]struct{}{}
	for _, d := range decisions {
		if d.BankObservationID != "" {
			ids[d.BankObservationID] = struct{}{}
		}
		for _, c := range d.Candidates {
			ids[c] = struct{}{}
		}
	}
	for _, ev := range events {
		if u := normalizeUTR(ev.RawReference); u != "" {
			utrs[u] = struct{}{}
		}
	}
	var out []BankTxn
	seen := map[string]struct{}{}
	for _, b := range banks {
		_, byID := ids[b.ID]
		_, byUTR := utrs[normalizeUTR(b.UTR)]
		if !byUTR && b.UTRRaw != "" {
			_, byUTR = utrs[normalizeUTR(b.UTRRaw)]
		}
		if !byID && !byUTR {
			continue
		}
		if _, ok := seen[b.ID]; ok {
			continue
		}
		seen[b.ID] = struct{}{}
		out = append(out, b)
	}
	_ = pay
	return out
}

func relatedPayoutBanks(p PayoutFact, events []ObservationFact, banks []BankTxn) []BankTxn {
	utrs := map[string]struct{}{}
	if u := normalizeUTR(p.UTR); u != "" {
		utrs[u] = struct{}{}
	}
	for _, ev := range events {
		if u := normalizeUTR(ev.RawReference); u != "" {
			utrs[u] = struct{}{}
		}
	}
	var out []BankTxn
	for _, b := range banks {
		bu := normalizeUTR(b.UTR)
		if bu == "" {
			bu = normalizeUTR(b.UTRRaw)
		}
		if _, ok := utrs[bu]; ok {
			out = append(out, b)
			continue
		}
		if p.AmountMinor > 0 && b.DebitMinor == p.AmountMinor && (b.Currency == "" || p.Currency == "" || strings.EqualFold(b.Currency, p.Currency)) {
			out = append(out, b)
		}
	}
	return out
}

func markUsedBanks(used map[string]struct{}, fr FinancialResult) {
	if fr.EvidenceRefs.BankObservationID != "" {
		used[fr.EvidenceRefs.BankObservationID] = struct{}{}
	}
	for _, id := range fr.CandidateIDs {
		used[id] = struct{}{}
	}
}

func applySharedBankAmbiguity(results []FinancialResult) []FinancialResult {
	bankToIdx := map[string][]int{}
	for i, r := range results {
		if r.EntityType != EntityPayment {
			continue
		}
		ids := map[string]struct{}{}
		if r.EvidenceRefs.BankObservationID != "" {
			ids[r.EvidenceRefs.BankObservationID] = struct{}{}
		}
		for _, c := range r.CandidateIDs {
			ids[c] = struct{}{}
		}
		for id := range ids {
			bankToIdx[id] = append(bankToIdx[id], i)
		}
	}
	flipped := map[int][]string{}
	for bankID, idxs := range bankToIdx {
		pays := map[string]struct{}{}
		for _, i := range idxs {
			pays[results[i].EntityID] = struct{}{}
		}
		if len(pays) < 2 {
			continue
		}
		var cands []string
		for p := range pays {
			cands = append(cands, p)
		}
		cands = append(cands, bankID)
		for _, i := range idxs {
			flipped[i] = cands
		}
	}
	for i, cands := range flipped {
		results[i] = MarkAmbiguous(results[i], cands)
	}
	return results
}

func normalizeUTR(s string) string {
	return strings.ToUpper(strings.TrimSpace(s))
}

func scopePayments(pays []PaymentFact, batchID string) []PaymentFact {
	batchID = strings.TrimSpace(batchID)
	if batchID == "" {
		return pays
	}
	out := make([]PaymentFact, 0, len(pays))
	for _, p := range pays {
		if strings.EqualFold(strings.TrimSpace(p.BatchID), batchID) {
			out = append(out, p)
		}
	}
	return out
}

func scopePayouts(payouts []PayoutFact, batchID string, want map[string]struct{}) []PayoutFact {
	batchID = strings.TrimSpace(batchID)
	if len(want) == 0 && batchID == "" {
		return payouts
	}
	out := make([]PayoutFact, 0, len(payouts))
	for _, po := range payouts {
		if len(want) > 0 {
			if _, ok := want[po.PayoutID]; !ok {
				continue
			}
		} else if !strings.EqualFold(strings.TrimSpace(po.BatchID), batchID) {
			continue
		}
		out = append(out, po)
	}
	return out
}

func payoutIDSet(ids []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" {
			out[id] = struct{}{}
		}
	}
	return out
}

func (s *FinancialService) BatchClose(ctx context.Context, tenantID, connectorID, batchID string) (BatchClose, bool, error) {
	batchID = strings.TrimSpace(batchID)
	if batchID == "" {
		return BatchClose{}, false, nil
	}
	run, ok, err := s.Store.GetLatestReconciliationRunByBatch(ctx, tenantID, connectorID, batchID)
	if err != nil || !ok {
		return BatchClose{}, ok, err
	}
	results, err := s.Store.ListReconciliationResults(ctx, tenantID, connectorID)
	if err != nil {
		return BatchClose{}, false, err
	}
	want := payoutIDSet(run.EntityIDs)
	filtered := make([]FinancialResult, 0, len(want))
	for _, r := range results {
		if _, ok := want[r.EntityID]; ok {
			filtered = append(filtered, r)
		}
	}
	return BatchCloseFromResults(batchID, run.ID, filtered, len(run.EntityIDs)), true, nil
}
