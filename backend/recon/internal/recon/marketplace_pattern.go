package recon

import (
	"sort"
	"strings"
)

// MarketplaceSellerPattern is the Slice 1 advisory COUNT/SUM cluster per seller.
// Pattern ≠ cash: these aggregates never mutate verdicts or payout blocks.
type MarketplaceSellerPattern struct {
	SellerID       string `json:"seller_id"`
	RefundCount    int64  `json:"refund_count"`
	RefundSumMinor int64  `json:"refund_sum_minor"`
}

// MarketplacePatternResult scopes seller patterns to one tenant+connector.
type MarketplacePatternResult struct {
	TenantID    string                     `json:"tenant_id"`
	ConnectorID string                     `json:"connector_id"`
	Sellers     []MarketplaceSellerPattern `json:"sellers"`
}

// MarketplacePatternAdvisory is a recommendation-only breach signal.
// Never an automatic payout block; stop = recommendation+audit OR configured hold only.
type MarketplacePatternAdvisory struct {
	SellerID       string `json:"seller_id"`
	RefundCount    int64  `json:"refund_count"`
	RefundSumMinor int64  `json:"refund_sum_minor"`
	Reason         string `json:"reason"` // "refund_count_threshold" | "refund_sum_threshold"
}

// CountsTowardSellerPattern excludes statuses that mean no money movement.
// Failed/cancelled must not inflate seller velocity SUM/COUNT as cash out
// (pattern ≠ cash; processed ≠ bank double-count). Include processed and
// in-flight statuses (created/pending/etc.) that represent refund activity.
//
// A refund whose transfer enrichment was skipped (D52) never counts: its
// marketplace shape is unknown, so it must not feed seller patterns (D48) or
// the refund-without-reversal check (D47), which both use this predicate.
func CountsTowardSellerPattern(r RefundFact) bool {
	if r.EnrichmentSkipped() {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(r.ProviderStatus)) {
	case "failed", "cancelled", "canceled":
		return false
	default:
		return true
	}
}

// AggregateMarketplaceSellerPatterns groups refunds by seller_id.
// Only JoinsSellerCluster() rows with CountsTowardSellerPattern contribute;
// null/empty/whitespace seller_id never forms a bucket.
func AggregateMarketplaceSellerPatterns(refunds []RefundFact) []MarketplaceSellerPattern {
	type acc struct {
		count int64
		sum   int64
	}
	by := map[string]*acc{}
	for _, r := range refunds {
		if !r.JoinsSellerCluster() || !CountsTowardSellerPattern(r) {
			continue
		}
		sid := strings.TrimSpace(r.SellerID)
		a := by[sid]
		if a == nil {
			a = &acc{}
			by[sid] = a
		}
		a.count++
		a.sum += r.AmountMinor
	}
	out := make([]MarketplaceSellerPattern, 0, len(by))
	for sid, a := range by {
		out = append(out, MarketplaceSellerPattern{
			SellerID: sid, RefundCount: a.count, RefundSumMinor: a.sum,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SellerID < out[j].SellerID })
	return out
}

// EvaluateMarketplacePatternBreach returns advisory recommendations for sellers
// that breach configured thresholds. Zero/negative threshold disables that dimension.
// Never blocks payouts — callers may audit/recommend or apply a configured hold.
func EvaluateMarketplacePatternBreach(patterns []MarketplaceSellerPattern, countThreshold, sumThreshold int64) []MarketplacePatternAdvisory {
	var out []MarketplacePatternAdvisory
	for _, p := range patterns {
		if countThreshold > 0 && p.RefundCount >= countThreshold {
			out = append(out, MarketplacePatternAdvisory{
				SellerID: p.SellerID, RefundCount: p.RefundCount, RefundSumMinor: p.RefundSumMinor,
				Reason: "refund_count_threshold",
			})
		}
		if sumThreshold > 0 && p.RefundSumMinor >= sumThreshold {
			out = append(out, MarketplacePatternAdvisory{
				SellerID: p.SellerID, RefundCount: p.RefundCount, RefundSumMinor: p.RefundSumMinor,
				Reason: "refund_sum_threshold",
			})
		}
	}
	return out
}
