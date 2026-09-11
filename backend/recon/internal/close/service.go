package close

import (
	"context"
	"sort"
	"time"

	"zord-outcome-engine/internal/recon"

	"github.com/google/uuid"
)

type Service struct {
	Financial *recon.FinancialService
	Store     *Store
	FinStore  recon.FinancialStore
	now       func() time.Time
}

func NewService(fin *recon.FinancialService, store *Store, finStore recon.FinancialStore) *Service {
	return &Service{Financial: fin, Store: store, FinStore: finStore, now: func() time.Time { return time.Now().UTC() }}
}

func (s *Service) Run(ctx context.Context, req RunRequest) (Report, error) {
	start := s.now()
	closeID := uuid.Must(uuid.NewV7()).String()
	if req.MaxInvestigate <= 0 {
		req.MaxInvestigate = 20
	}

	run, _, err := s.Financial.Run(ctx, recon.FinancialRunRequest{
		TenantID: req.TenantID, ConnectorID: req.ConnectorID, AccountID: req.AccountID,
	})
	if err != nil {
		return Report{}, err
	}

	summary, err := s.Financial.FinanceSummary(ctx, req.TenantID, req.ConnectorID)
	if err != nil {
		return Report{}, err
	}
	exceptions, err := s.FinStore.ListReconciliationExceptions(ctx, req.TenantID, req.ConnectorID)
	if err != nil {
		return Report{}, err
	}
	results, err := s.FinStore.ListReconciliationResults(ctx, req.TenantID, req.ConnectorID)
	if err != nil {
		return Report{}, err
	}
	lines, err := s.FinStore.ListSettlementLines(ctx, req.TenantID, req.ConnectorID)
	if err != nil {
		return Report{}, err
	}

	sort.Slice(exceptions, func(i, j int) bool {
		return exceptions[i].VarianceAmount > exceptions[j].VarianceAmount
	})
	investigated := 0
	for i := 0; i < len(exceptions) && i < req.MaxInvestigate; i++ {
		if _, err := s.Financial.Investigate(ctx, req.TenantID, req.ConnectorID, exceptions[i].ID, ""); err == nil {
			investigated++
		}
	}

	truth, _ := s.Store.ListGroundTruth(ctx, req.TenantID, req.ConnectorID, req.BatchID)
	acc := ComputeAccuracy(truth, results)
	cash := recon.CashPosition(results, lines, exceptions)

	matchRate := 0.0
	if summary.ScoredCount > 0 {
		matchRate = float64(summary.MatchedCount) / float64(summary.ScoredCount)
	}
	duration := s.now().Sub(start)
	throughput := 0.0
	if duration > 0 && summary.ScoredCount > 0 {
		throughput = float64(summary.ScoredCount) / duration.Seconds()
	}

	exList := make([]ExceptionItem, 0, len(exceptions))
	for _, ex := range exceptions {
		exList = append(exList, ExceptionItem{
			EntityID: ex.EntityID, EntityType: ex.EntityType, Reason: ex.Reason,
			Result: ex.ReconciliationResult, Variance: ex.VarianceAmount, Certainty: "UNKNOWN",
		})
	}

	rep := Report{
		CloseRunID:              closeID,
		TenantID:                  req.TenantID,
		ConnectorID:               req.ConnectorID,
		BatchID:                 req.BatchID,
		ReconRunID:              run.ID,
		Records:                 summary.ScoredCount,
		Matched:                 summary.MatchedCount,
		Exceptions:              len(exceptions),
		MatchRate:               matchRate,
		Investigated:            investigated,
		ResolvedByInvestigation: 0,
		StillUnresolved:         len(exceptions),
		UnresolvedExposureMinor: summary.ExposureMinor,
		FalseResolutions:        0,
		ThroughputPerS:          throughput,
		DurationMS:              duration.Milliseconds(),
		Currency:                "INR",
		ExceptionList:           exList,
		Accuracy:                acc,
		CashPosition: map[string]any{
			"gross_captured_minor":        cash.GrossCapturedMinor,
			"settlement_expected_net_minor": cash.SettlementExpectedNetMinor,
			"settlement_derived_net_minor":  cash.SettlementDerivedNetMinor,
			"bank_credited_proven_minor":  cash.BankCreditedProvenMinor,
			"in_flight_minor":             cash.InFlightMinor,
			"unresolved_exposure_minor":   cash.UnresolvedExposureMinor,
		},
		StartedAt:   start,
		CompletedAt: s.now(),
	}
	if err := s.Store.SaveCloseRun(ctx, rep); err != nil {
		return rep, err
	}
	return rep, nil
}

func (s *Service) Get(ctx context.Context, tenantID, id string) (Report, error) {
	return s.Store.GetCloseRun(ctx, tenantID, id)
}

func (s *Service) Accuracy(ctx context.Context, tenantID, id string) (AccuracyReport, error) {
	rep, err := s.Store.GetCloseRun(ctx, tenantID, id)
	if err != nil {
		return AccuracyReport{}, err
	}
	return rep.Accuracy, nil
}
