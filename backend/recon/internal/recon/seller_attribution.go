package recon

import (
	"context"
	"strings"
)

// storedPaymentSellers maps payment_id -> seller_id using only seller_id values
// already stored on transfer edges and refund facts for this tenant+connector.
// A payment whose stored facts name more than one distinct seller (split
// payment) maps to "": we never pick one or derive a seller heuristically (D48).
// paymentID narrows the refund lookup when non-empty.
func (s *FinancialService) storedPaymentSellers(ctx context.Context, tenantID, connectorID, paymentID string) (map[string]string, error) {
	seen := map[string]map[string]struct{}{}
	add := func(pid, seller string) {
		pid, seller = strings.TrimSpace(pid), strings.TrimSpace(seller)
		if pid == "" || seller == "" {
			return
		}
		if paymentID != "" && pid != paymentID {
			return
		}
		if seen[pid] == nil {
			seen[pid] = map[string]struct{}{}
		}
		seen[pid][seller] = struct{}{}
	}
	edges, err := s.Store.ListTransferEdges(ctx, tenantID, connectorID)
	if err != nil {
		return nil, err
	}
	for _, e := range edges {
		add(e.PaymentID, e.SellerID)
	}
	refunds, err := s.Store.ListRefunds(ctx, tenantID, connectorID, paymentID)
	if err != nil {
		return nil, err
	}
	for _, r := range refunds {
		add(r.PaymentID, r.SellerID)
	}
	out := make(map[string]string, len(seen))
	for pid, sellers := range seen {
		if len(sellers) != 1 {
			continue
		}
		for seller := range sellers {
			out[pid] = seller
		}
	}
	return out, nil
}
