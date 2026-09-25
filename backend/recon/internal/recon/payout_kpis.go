package recon

import (
	"context"
	"strings"

	"zord-outcome-engine/internal/poll/providers/razorpay"
)

const (
	// PayoutOriginProvider marks a canonical payout created from a provider
	// observation (webhook / poll / backfill). Empty Origin means the same.
	PayoutOriginProvider = "provider"
	// PayoutOriginFileIntent marks a canonical payout seeded from an uploaded
	// payout file intent. It only counts in KPIs once a provider observation
	// is linked to it.
	PayoutOriginFileIntent = "file_intent"

	payoutKPICurrency = "INR"
)

// PayoutKPIs mirrors Console FinancePayoutKpis (financeTypes.ts). Amounts are
// int64 minor units (paise). INR payouts only; refunds and settlements never
// contribute. Buckets mirror Console payoutStatusBucket: processed ->
// processed; pending/scheduled/queued/processing -> review; everything else
// (failed, cancelled, rejected, reversed, unknown) -> failed.
type PayoutKPIs struct {
	Currency             string `json:"currency"`
	ScoredCount          int    `json:"scored_count"`
	ProcessedCount       int    `json:"processed_count"`
	ProcessedAmountMinor int64  `json:"processed_amount_minor"`
	ReviewCount          int    `json:"review_count"`
	ReviewAmountMinor    int64  `json:"review_amount_minor"`
	FailedCount          int    `json:"failed_count"`
	FailedAmountMinor    int64  `json:"failed_amount_minor"`
	TotalAmountMinor     int64  `json:"total_amount_minor"`
}

// PayoutKPIs computes payout KPIs for one tenant+connector from canonical payouts.
func (s *FinancialService) PayoutKPIs(ctx context.Context, tenantID, connectorID string) (PayoutKPIs, error) {
	out := PayoutKPIs{Currency: payoutKPICurrency}
	payouts, err := s.Store.ListCanonicalPayouts(ctx, tenantID, connectorID)
	if err != nil {
		return out, err
	}
	for _, p := range payouts {
		if !strings.EqualFold(strings.TrimSpace(p.Currency), payoutKPICurrency) {
			continue // INR only; other currencies are excluded, never converted.
		}
		if strings.EqualFold(strings.TrimSpace(p.Origin), PayoutOriginFileIntent) {
			linked, err := s.payoutHasProviderObservation(ctx, tenantID, connectorID, p.PayoutID)
			if err != nil {
				return out, err
			}
			if !linked {
				continue // unlinked file intent: not a payout the provider has seen.
			}
		}
		addPayoutKPI(&out, p.ProviderStatus, p.AmountMinor)
	}
	return out, nil
}

func addPayoutKPI(out *PayoutKPIs, status string, amount int64) {
	out.ScoredCount++
	out.TotalAmountMinor += amount
	switch {
	case razorpay.IsPayoutProcessed(status):
		out.ProcessedCount++
		out.ProcessedAmountMinor += amount
	case razorpay.IsPayoutOpen(status):
		out.ReviewCount++
		out.ReviewAmountMinor += amount
	default:
		// IsPayoutNoNetOutflow (failed/cancelled/rejected/reversed) and any
		// unknown status land in failed, matching Console's bucket fallback.
		out.FailedCount++
		out.FailedAmountMinor += amount
	}
}

func (s *FinancialService) payoutHasProviderObservation(ctx context.Context, tenantID, connectorID, payoutID string) (bool, error) {
	if strings.TrimSpace(payoutID) == "" {
		return false, nil
	}
	events, err := s.Store.ListPayoutObservationFacts(ctx, tenantID, connectorID, payoutID)
	if err != nil {
		return false, err
	}
	for _, e := range events {
		src := strings.ToLower(strings.TrimSpace(e.Source))
		if src == "" || src == PayoutOriginFileIntent || src == "file" {
			continue
		}
		return true, nil
	}
	return false, nil
}
