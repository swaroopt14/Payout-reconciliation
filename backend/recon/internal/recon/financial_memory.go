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
	refundScopes   map[string]string // refund row ID -> tenant|connector for isolation
	TransferEdges  []MarketplaceTransferEdge
	edgeScopes     map[string]string // transfer_id -> tenant|connector for isolation
	payoutScopes   map[string]string // payout row ID -> tenant|connector for isolation
	MerchantBooks  []MerchantBookFact
	heldRuns       map[string]struct{}
}

func NewMemoryFinancialStore() *MemoryFinancialStore {
	return &MemoryFinancialStore{Events: map[string][]ObservationFact{}, PayoutEvents: map[string][]ObservationFact{}}
}

func (m *MemoryFinancialStore) ListCanonicalPayouts(_ context.Context, tenantID, connectorID string) ([]PayoutFact, error) {
	if m.payoutScopes == nil {
		// Legacy unscoped fixtures (tests assign m.Payouts directly).
		return append([]PayoutFact{}, m.Payouts...), nil
	}
	scope := refundScopeKey(tenantID, connectorID)
	var out []PayoutFact
	for _, p := range m.Payouts {
		if m.payoutScopes[p.ID] == scope {
			out = append(out, p)
		}
	}
	return out, nil
}

// PutScopedPayout stores a payout under tenant+connector so the memory store
// mirrors the SQL tenant_id/connector_id filter. Once used, unscoped rows are
// no longer returned by ListCanonicalPayouts.
func (m *MemoryFinancialStore) PutScopedPayout(tenantID, connectorID string, p PayoutFact) PayoutFact {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p.ID == "" {
		p.ID = uuid.Must(uuid.NewV7()).String()
	}
	if m.payoutScopes == nil {
		m.payoutScopes = map[string]string{}
	}
	m.payoutScopes[p.ID] = refundScopeKey(tenantID, connectorID)
	m.Payouts = append(m.Payouts, p)
	return p
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
			if m.refundScopes[r.ID] != scope {
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
	// Mirror UNIQUE (tenant_id, connector_id, refund_id): the same refund id
	// from webhook and poll (or a replay) updates one row; the same refund id
	// under another tenant/connector is a different row.
	scope := refundScopeKey(tenantID, connectorID)
	for i := range m.Refunds {
		if r.RefundID != "" && m.Refunds[i].RefundID == r.RefundID && m.refundScopes[m.Refunds[i].ID] == scope {
			r.ID = m.Refunds[i].ID
			if r.SellerID == "" {
				r.SellerID = m.Refunds[i].SellerID // COALESCE(EXCLUDED.seller_id, existing)
			}
			if r.EnrichmentStatus == "" {
				r.EnrichmentStatus = m.Refunds[i].EnrichmentStatus // COALESCE(EXCLUDED.enrichment_status, existing)
			}
			m.Refunds[i] = r
			return r, nil
		}
	}
	m.refundScopes[r.ID] = scope
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

// ListSkippedEnrichmentRefunds returns up to limit refunds in one
// tenant+connector whose transfer enrichment was skipped (D52).
func (m *MemoryFinancialStore) ListSkippedEnrichmentRefunds(ctx context.Context, tenantID, connectorID string, limit int) ([]RefundFact, error) {
	all, err := m.ListRefunds(ctx, tenantID, connectorID, "")
	if err != nil {
		return nil, err
	}
	var out []RefundFact
	for _, r := range all {
		if r.EnrichmentSkipped() {
			out = append(out, r)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

// CountSkippedEnrichment returns skipped-enrichment refund counts by reason
// for a tenant (all connectors when connectorID is ""). Data-gap input only.
func (m *MemoryFinancialStore) CountSkippedEnrichment(_ context.Context, tenantID, connectorID string) (SkippedEnrichmentCounts, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := SkippedEnrichmentCounts{TenantID: tenantID, ConnectorID: connectorID, ByReason: map[string]int64{}}
	tenant := strings.ToLower(strings.TrimSpace(tenantID))
	for _, r := range m.Refunds {
		scope := m.refundScopes[r.ID]
		if !strings.HasPrefix(scope, tenant+"|") {
			continue
		}
		if connectorID != "" && scope != refundScopeKey(tenantID, connectorID) {
			continue
		}
		if r.EnrichmentSkipped() {
			out.ByReason[r.EnrichmentStatus]++
			out.Total++
		}
	}
	return out, nil
}
