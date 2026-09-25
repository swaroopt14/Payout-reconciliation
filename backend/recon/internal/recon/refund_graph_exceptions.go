package recon

import (
	"context"
	"strings"

	"github.com/google/uuid"
)

// EntityRefund is the exception entity_type for refund-scoped exceptions
// (e.g. refund_without_reverse_transfer). It is an entity label only — not a
// recon verdict.
const EntityRefund = "refund"

// refundGraphExceptionNamespace seeds deterministic exception IDs for derived
// refund-graph exceptions so the same (tenant, connector, refund_id) always
// maps to the same exception id across list/get/investigate calls.
var refundGraphExceptionNamespace = uuid.NewSHA1(uuid.NameSpaceURL, []byte("zord:recon:exception:"+ReasonRefundWithoutReverse))

// RefundGraphExceptionID returns the deterministic exception id for a
// refund_without_reverse_transfer exception in one tenant+connector scope.
func RefundGraphExceptionID(tenantID, connectorID, refundID string) string {
	key := strings.ToLower(strings.TrimSpace(tenantID)) + "|" +
		strings.ToLower(strings.TrimSpace(connectorID)) + "|" + strings.TrimSpace(refundID)
	return uuid.NewSHA1(refundGraphExceptionNamespace, []byte(key)).String()
}

// RefundGraphSignalException projects a refund-without-reverse signal onto the
// existing ReconciliationException shape:
//
//   - ReconciliationResult = UNRESOLVED (existing verdict; never MATCHED)
//   - Reason = refund_without_reverse_transfer
//   - EntityType = refund, EntityID = refund_id
//   - VarianceAmount = 0 and ObservedAmount = 0: this is an exception to
//     investigate, NOT money that left. Cash position / exposure totals sum
//     VarianceAmount, so they are unchanged by these exceptions.
//   - ExpectedAmount = refund amount (the reverse transfer we expected to see),
//     reference only.
//
// The exception is derived at read time from refunds + transfer edges; it is
// never written to reconciliation_exceptions, so it disappears on its own
// once a reverse transfer edge is ingested.
func RefundGraphSignalException(tenantID, connectorID string, sig MarketplaceRefundGraphSignal) ReconciliationException {
	refs := EvidenceRefs{
		Sources: []SourceRef{
			{Kind: SourceKindRefund, ID: sig.RefundID, Amount: sig.AmountMinor},
			{Kind: SourceKindPayment, ID: sig.PaymentID},
			{Kind: "seller", ID: sig.SellerID},
		},
	}
	return ReconciliationException{
		ID:                   RefundGraphExceptionID(tenantID, connectorID, sig.RefundID),
		TenantID:             tenantID,
		ConnectorID:          connectorID,
		EntityType:           EntityRefund,
		EntityID:             sig.RefundID,
		Status:               sig.ProviderStatus,
		ReconciliationResult: ResultUnresolved,
		Reason:               ReasonRefundWithoutReverse,
		ExpectedAmount:       sig.AmountMinor,
		ObservedAmount:       0,
		VarianceAmount:       0,
		CandidateIDs:         []string{},
		Confidence:           0.6,
		EvidenceIDs:          []string{},
		EvidenceRefs:         refs,
	}
}

// refundGraphSignals loads refunds + transfer edges for one tenant+connector
// and returns scoped refund-without-reverse signals.
func (s *FinancialService) refundGraphSignals(ctx context.Context, tenantID, connectorID string) ([]MarketplaceRefundGraphSignal, error) {
	refunds, err := s.Store.ListRefunds(ctx, tenantID, connectorID, "")
	if err != nil {
		return nil, err
	}
	edges, err := s.Store.ListTransferEdges(ctx, tenantID, connectorID)
	if err != nil {
		return nil, err
	}
	signals := DetectRefundsWithoutReverse(refunds, edges)
	for i := range signals {
		signals[i].TenantID = tenantID
		signals[i].ConnectorID = connectorID
	}
	return signals, nil
}

// RefundGraphExceptions returns refund_without_reverse_transfer exceptions for
// one tenant+connector in the existing ReconciliationException shape.
func (s *FinancialService) RefundGraphExceptions(ctx context.Context, tenantID, connectorID string) ([]ReconciliationException, error) {
	tenantID = strings.TrimSpace(tenantID)
	connectorID = strings.TrimSpace(connectorID)
	if tenantID == "" || connectorID == "" {
		return nil, nil
	}
	signals, err := s.refundGraphSignals(ctx, tenantID, connectorID)
	if err != nil {
		return nil, err
	}
	out := make([]ReconciliationException, 0, len(signals))
	for _, sig := range signals {
		out = append(out, RefundGraphSignalException(tenantID, connectorID, sig))
	}
	return out, nil
}

// ListExceptions is the exceptions listing for one tenant+connector: persisted
// recon exceptions plus derived refund_without_reverse_transfer exceptions.
// A persisted exception for the same entity_type+entity_id wins.
func (s *FinancialService) ListExceptions(ctx context.Context, tenantID, connectorID string) ([]ReconciliationException, error) {
	persisted, err := s.Store.ListReconciliationExceptions(ctx, tenantID, connectorID)
	if err != nil {
		return nil, err
	}
	derived, err := s.RefundGraphExceptions(ctx, tenantID, connectorID)
	if err != nil {
		return nil, err
	}
	if len(derived) == 0 {
		return persisted, nil
	}
	seen := make(map[string]struct{}, len(persisted))
	for _, ex := range persisted {
		seen[ex.EntityType+"|"+ex.EntityID] = struct{}{}
	}
	out := append([]ReconciliationException{}, persisted...)
	for _, ex := range derived {
		if _, dup := seen[ex.EntityType+"|"+ex.EntityID]; dup {
			continue
		}
		out = append(out, ex)
	}
	return out, nil
}

// GetException resolves an exception id from the persisted store first, then
// from the derived refund-graph exceptions for the same tenant+connector.
func (s *FinancialService) GetException(ctx context.Context, tenantID, connectorID, id string) (ReconciliationException, bool, error) {
	ex, ok, err := s.Store.GetReconciliationException(ctx, tenantID, connectorID, id)
	if err != nil || ok {
		return ex, ok, err
	}
	derived, err := s.RefundGraphExceptions(ctx, tenantID, connectorID)
	if err != nil {
		return ReconciliationException{}, false, err
	}
	for _, d := range derived {
		if d.ID == id {
			return d, true, nil
		}
	}
	return ReconciliationException{}, false, nil
}
