package recon

import (
	"context"
	"strings"
	"sync"
	"time"

	"zord-outcome-engine/models"

	"github.com/google/uuid"
)

type MemoryFinancialStore struct {
	mu             sync.Mutex
	Payments       []PaymentFact
	Events         map[string][]ObservationFact
	Payouts        []PayoutFact
	PayoutEvents   map[string][]ObservationFact
	Lines          []SettlementLine
	Banks          []BankTxn
	Decisions      []SettlementBankDecision
	Runs           []ReconciliationRun
	Results        []FinancialResult
	Exceptions     []ReconciliationException
	Investigations []InvestigationRecord
	Outbox         []models.OutboxRow
	Refunds        []RefundFact
	refundScopes   map[string]string // refund_id -> tenant|connector for isolation
	TransferEdges  []MarketplaceTransferEdge
	edgeScopes     map[string]string // transfer_id -> tenant|connector for isolation
	MerchantBooks  []MerchantBookFact
	heldRuns       map[string]struct{}
}

func NewMemoryFinancialStore() *MemoryFinancialStore {
	return &MemoryFinancialStore{Events: map[string][]ObservationFact{}, PayoutEvents: map[string][]ObservationFact{}}
}

func (m *MemoryFinancialStore) ListCanonicalPayouts(context.Context, string, string) ([]PayoutFact, error) {
	return append([]PayoutFact{}, m.Payouts...), nil
}

func (m *MemoryFinancialStore) GetCanonicalPayoutFact(_ context.Context, _, _, payoutID string) (PayoutFact, bool, error) {
	for _, p := range m.Payouts {
		if p.PayoutID == payoutID {
			return p, true, nil
		}
	}
	return PayoutFact{}, false, nil
}

func (m *MemoryFinancialStore) ListPayoutObservationFacts(_ context.Context, _, _, payoutID string) ([]ObservationFact, error) {
	return append([]ObservationFact{}, m.PayoutEvents[payoutID]...), nil
}

func (m *MemoryFinancialStore) ListCanonicalPayments(context.Context, string, string) ([]PaymentFact, error) {
	return append([]PaymentFact{}, m.Payments...), nil
}

func (m *MemoryFinancialStore) GetCanonicalPayment(_ context.Context, _, _, paymentID string) (PaymentFact, bool, error) {
	for _, p := range m.Payments {
		if p.PaymentID == paymentID {
			return p, true, nil
		}
	}
	return PaymentFact{}, false, nil
}

func (m *MemoryFinancialStore) ListObservationEvents(_ context.Context, _, _, paymentID string) ([]ObservationFact, error) {
	return append([]ObservationFact{}, m.Events[paymentID]...), nil
}

func (m *MemoryFinancialStore) ListSettlementLines(context.Context, string, string) ([]SettlementLine, error) {
	return append([]SettlementLine{}, m.Lines...), nil
}

func (m *MemoryFinancialStore) ListBankTxns(_ context.Context, _, _, accountID string) ([]BankTxn, error) {
	if accountID == "" {
		return append([]BankTxn{}, m.Banks...), nil
	}
	var out []BankTxn
	for _, b := range m.Banks {
		if b.AccountID == "" || b.AccountID == accountID {
			out = append(out, b)
		}
	}
	return out, nil
}

func (m *MemoryFinancialStore) ListSettlementBankDecisions(context.Context, string, string) ([]SettlementBankDecision, error) {
	return append([]SettlementBankDecision{}, m.Decisions...), nil
}

func (m *MemoryFinancialStore) TryLockTenantRun(_ context.Context, tenantID, connectorID string) (func(), error) {
	key := strings.ToLower(strings.TrimSpace(tenantID)) + "|" + strings.ToLower(strings.TrimSpace(connectorID))
	m.mu.Lock()
	if m.heldRuns == nil {
		m.heldRuns = map[string]struct{}{}
	}
	if _, taken := m.heldRuns[key]; taken {
		m.mu.Unlock()
		return nil, ErrRunInProgress
	}
	m.heldRuns[key] = struct{}{}
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		delete(m.heldRuns, key)
		m.mu.Unlock()
	}, nil
}

func (m *MemoryFinancialStore) InsertReconciliationRun(_ context.Context, run ReconciliationRun) (ReconciliationRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if run.ID == "" {
		run.ID = uuid.Must(uuid.NewV7()).String()
	}
	m.Runs = append(m.Runs, run)
	return run, nil
}

func (m *MemoryFinancialStore) CompleteReconciliationRun(_ context.Context, run ReconciliationRun) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.Runs {
		if m.Runs[i].ID == run.ID {
			m.Runs[i] = run
			return nil
		}
	}
	m.Runs = append(m.Runs, run)
	return nil
}

func (m *MemoryFinancialStore) GetReconciliationRun(_ context.Context, _, runID string) (ReconciliationRun, error) {
	for _, r := range m.Runs {
		if r.ID == runID {
			return r, nil
		}
	}
	return ReconciliationRun{}, errNotFound
}

func (m *MemoryFinancialStore) GetLatestReconciliationRunByBatch(_ context.Context, _, _, batchID string) (ReconciliationRun, bool, error) {
	var best ReconciliationRun
	found := false
	for _, r := range m.Runs {
		if r.BatchID != batchID || batchID == "" {
			continue
		}
		if !found || r.CreatedAt.After(best.CreatedAt) {
			best = r
			found = true
		}
	}
	return best, found, nil
}

func (m *MemoryFinancialStore) UpsertReconciliationResult(_ context.Context, _, _, runID string, r FinancialResult) (FinancialResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r.RunID = runID
	if r.ID == "" {
		r.ID = uuid.Must(uuid.NewV7()).String()
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now().UTC()
	}
	if r.Exception != nil {
		r.Exception.RunID = runID
		if r.Exception.ID == "" {
			r.Exception.ID = uuid.Must(uuid.NewV7()).String()
		}
	}
	for i := range m.Results {
		if m.Results[i].EntityType == r.EntityType && m.Results[i].EntityID == r.EntityID {
			m.Results[i] = r
			return r, nil
		}
	}
	m.Results = append(m.Results, r)
	return r, nil
}

func (m *MemoryFinancialStore) GetReconciliationResult(_ context.Context, _, _, entityType, entityID string) (FinancialResult, bool, error) {
	for _, r := range m.Results {
		if r.EntityType == entityType && r.EntityID == entityID {
			return r, true, nil
		}
	}
	return FinancialResult{}, false, nil
}

func (m *MemoryFinancialStore) InsertReconciliationException(_ context.Context, tenantID, connectorID, runID string, ex ReconciliationException) (ReconciliationException, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ex.ID == "" {
		ex.ID = uuid.Must(uuid.NewV7()).String()
	}
	ex.TenantID = tenantID
	ex.ConnectorID = connectorID
	ex.RunID = runID
	ex.CreatedAt = time.Now().UTC()
	m.Exceptions = append(m.Exceptions, ex)
	return ex, nil
}

func (m *MemoryFinancialStore) ListReconciliationExceptions(context.Context, string, string) ([]ReconciliationException, error) {
	return append([]ReconciliationException{}, m.Exceptions...), nil
}

func (m *MemoryFinancialStore) ListReconciliationResults(context.Context, string, string) ([]FinancialResult, error) {
	return append([]FinancialResult{}, m.Results...), nil
}

func (m *MemoryFinancialStore) GetReconciliationException(_ context.Context, _, _, id string) (ReconciliationException, bool, error) {
	for _, ex := range m.Exceptions {
		if ex.ID == id {
			return ex, true, nil
		}
	}
	return ReconciliationException{}, false, nil
}

func (m *MemoryFinancialStore) InsertInvestigation(_ context.Context, rec InvestigationRecord) (InvestigationRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if rec.ID == "" {
		rec.ID = uuid.Must(uuid.NewV7()).String()
	}
	now := time.Now().UTC()
	rec.CreatedAt = now
	rec.UpdatedAt = now
	m.Investigations = append(m.Investigations, rec)
	return rec, nil
}

func (m *MemoryFinancialStore) GetInvestigation(_ context.Context, _, _, id string) (InvestigationRecord, bool, error) {
	for _, rec := range m.Investigations {
		if rec.ID == id {
			return rec, true, nil
		}
	}
	return InvestigationRecord{}, false, nil
}

func (m *MemoryFinancialStore) ListInvestigations(context.Context, string, string) ([]InvestigationRecord, error) {
	return append([]InvestigationRecord{}, m.Investigations...), nil
}

func (m *MemoryFinancialStore) ListMerchantBooks(context.Context, string, string) ([]MerchantBookFact, error) {
	return append([]MerchantBookFact{}, m.MerchantBooks...), nil
}

func refundScopeKey(tenantID, connectorID string) string {
	return strings.ToLower(strings.TrimSpace(tenantID)) + "|" + strings.ToLower(strings.TrimSpace(connectorID))
}

func (m *MemoryFinancialStore) ListRefunds(_ context.Context, tenantID, connectorID, paymentID string) ([]RefundFact, error) {
	scope := refundScopeKey(tenantID, connectorID)
	var out []RefundFact
	for _, r := range m.Refunds {
		if m.refundScopes != nil {
			if m.refundScopes[r.RefundID] != scope {
				continue
			}
		}
		if paymentID != "" && r.PaymentID != paymentID {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func (m *MemoryFinancialStore) UpsertRefund(_ context.Context, tenantID, connectorID string, r RefundFact) (RefundFact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r.ID == "" {
		r.ID = uuid.Must(uuid.NewV7()).String()
	}
	r.SellerID = strings.TrimSpace(r.SellerID)
	if m.refundScopes == nil {
		m.refundScopes = map[string]string{}
	}
	if r.RefundID != "" {
		m.refundScopes[r.RefundID] = refundScopeKey(tenantID, connectorID)
	}
	for i := range m.Refunds {
		if m.Refunds[i].RefundID == r.RefundID && r.RefundID != "" {
			m.Refunds[i] = r
			return r, nil
		}
	}
	m.Refunds = append(m.Refunds, r)
	return r, nil
}

// ListMarketplaceSellerPatterns aggregates refund COUNT/SUM by seller for one tenant+connector.
// Advisory read only — does not mutate cash, verdicts, or payouts.
func (m *MemoryFinancialStore) ListMarketplaceSellerPatterns(ctx context.Context, tenantID, connectorID string) (MarketplacePatternResult, error) {
	refunds, err := m.ListRefunds(ctx, tenantID, connectorID, "")
	if err != nil {
		return MarketplacePatternResult{}, err
	}
	return MarketplacePatternResult{
		TenantID:    tenantID,
		ConnectorID: connectorID,
		Sellers:     AggregateMarketplaceSellerPatterns(refunds),
	}, nil
}

func (m *MemoryFinancialStore) InsertMatchOutbox(_ context.Context, row models.OutboxRow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Outbox = append(m.Outbox, row)
	return nil
}

func (m *MemoryFinancialStore) ListTransferEdges(_ context.Context, tenantID, connectorID string) ([]MarketplaceTransferEdge, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	scope := refundScopeKey(tenantID, connectorID)
	var out []MarketplaceTransferEdge
	for _, e := range m.TransferEdges {
		// Prefer denormalized tenant/connector on the edge (UNIQUE tenant+connector+transfer_id).
		if strings.TrimSpace(e.TenantID) != "" || strings.TrimSpace(e.ConnectorID) != "" {
			if refundScopeKey(e.TenantID, e.ConnectorID) != scope {
				continue
			}
		} else if m.edgeScopes != nil {
			key := scope + "|" + e.TransferID
			if m.edgeScopes[key] != scope && m.edgeScopes[e.TransferID] != scope {
				continue
			}
		}
		out = append(out, e)
	}
	return out, nil
}

func (m *MemoryFinancialStore) UpsertTransferEdge(_ context.Context, tenantID, connectorID string, e MarketplaceTransferEdge) (MarketplaceTransferEdge, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e.ID == "" {
		e.ID = uuid.Must(uuid.NewV7()).String()
	}
	e.TenantID = tenantID
	e.ConnectorID = connectorID
	e.TransferID = strings.TrimSpace(e.TransferID)
	e.ReverseTransferID = strings.TrimSpace(e.ReverseTransferID)
	e.PaymentID = strings.TrimSpace(e.PaymentID)
	e.RefundID = strings.TrimSpace(e.RefundID)
	e.SellerID = strings.TrimSpace(e.SellerID)
	if e.Currency == "" {
		e.Currency = "INR"
	}
	fromReversal := e.RefundIDFromReversal && e.RefundID != ""
	e.RefundIDFromReversal = false
	scope := refundScopeKey(tenantID, connectorID)
	if m.edgeScopes == nil {
		m.edgeScopes = map[string]string{}
	}
	scopeKey := scope + "|" + e.TransferID
	if e.TransferID != "" {
		m.edgeScopes[scopeKey] = scope
		// Keep legacy transfer_id index for List filter compatibility.
		m.edgeScopes[e.TransferID] = scope
	}
	for i := range m.TransferEdges {
		prev := m.TransferEdges[i]
		if prev.TransferID == "" || prev.TransferID != e.TransferID {
			continue
		}
		if refundScopeKey(prev.TenantID, prev.ConnectorID) != scope {
			continue
		}
		// Enrichment-safe coalesce (mirror SQL ON CONFLICT).
		if e.ReverseTransferID == "" && prev.ReverseTransferID != "" {
			e.ReverseTransferID = prev.ReverseTransferID
		}
		if e.ReverseAt.IsZero() && !prev.ReverseAt.IsZero() {
			e.ReverseAt = prev.ReverseAt
		}
		if e.PaymentID == "" && prev.PaymentID != "" {
			e.PaymentID = prev.PaymentID
		}
		// refund_id: keep existing non-empty value unless the new one comes
		// from a reversal's customer_refund_id (mirror SQL CASE).
		if prev.RefundID != "" && !fromReversal {
			e.RefundID = prev.RefundID
		}
		if e.SellerID == "" && prev.SellerID != "" {
			e.SellerID = prev.SellerID
		}
		if e.AmountMinor == 0 && prev.AmountMinor != 0 {
			e.AmountMinor = prev.AmountMinor
		}
		if e.TransferAt.IsZero() && !prev.TransferAt.IsZero() {
			e.TransferAt = prev.TransferAt
		}
		e.ID = prev.ID
		m.TransferEdges[i] = e
		return e, nil
	}
	m.TransferEdges = append(m.TransferEdges, e)
	return e, nil
}
