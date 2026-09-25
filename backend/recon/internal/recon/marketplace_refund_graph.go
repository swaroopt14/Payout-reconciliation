package recon

import (
	"sort"
	"strings"
	"time"
)

// ReasonRefundWithoutReverse is the ops counsel reason when a marketplace
// refund has seller_id but no matching reverse_transfer edge.
// Not a recon Result* verdict — never written as MATCHED.
const ReasonRefundWithoutReverse = "refund_without_reverse_transfer"

// MarketplaceTransferEdge is a reference-only graph edge linking a Route
// transfer to an optional reverse transfer. Not a money ledger; amounts are
// int64 minor units for correlation only — never treated as bank cash.
type MarketplaceTransferEdge struct {
	ID                string    `json:"id"`
	TenantID          string    `json:"tenant_id,omitempty"`
	ConnectorID       string    `json:"connector_id,omitempty"`
	TransferID        string    `json:"transfer_id"`
	ReverseTransferID string    `json:"reverse_transfer_id,omitempty"`
	PaymentID         string    `json:"payment_id"`
	RefundID          string    `json:"refund_id,omitempty"`
	SellerID          string    `json:"seller_id"`
	AmountMinor       int64     `json:"amount_minor"`
	Currency          string    `json:"currency"`
	TransferAt        time.Time `json:"transfer_at,omitempty"`
	ReverseAt         time.Time `json:"reverse_at,omitempty"`
	CreatedAt         time.Time `json:"created_at,omitempty"`
	// RefundIDFromReversal marks RefundID as sourced from a Razorpay reversal's
	// customer_refund_id. Only then may Upsert replace an existing non-empty
	// refund_id; refund-observe stamps never overwrite. Not persisted.
	RefundIDFromReversal bool `json:"-"`
}

// HasReverse reports whether this edge records a reverse_transfer_id.
func (e MarketplaceTransferEdge) HasReverse() bool {
	return strings.TrimSpace(e.ReverseTransferID) != ""
}

// MarketplaceRefundGraphSignal is an ops/counsel exception for a marketplace
// refund lacking a reverse transfer. Not a recon verdict enum value.
type MarketplaceRefundGraphSignal struct {
	TenantID    string `json:"tenant_id,omitempty"`
	ConnectorID string `json:"connector_id,omitempty"`
	RefundID    string `json:"refund_id"`
	PaymentID   string `json:"payment_id"`
	SellerID    string `json:"seller_id"`
	AmountMinor int64  `json:"amount_minor"`
	Reason      string `json:"reason"`
	// ProviderStatus / Currency are copied from the refund fact so the signal can
	// be projected onto a ReconciliationException without re-reading refunds.
	ProviderStatus string `json:"provider_status,omitempty"`
	Currency       string `json:"currency,omitempty"`
}

// MarketplaceRefundGraphResult scopes counsel signals to one tenant+connector.
// Patterns/exceptions here are not cash (Finance: not bank debit).
type MarketplaceRefundGraphResult struct {
	TenantID    string                         `json:"tenant_id"`
	ConnectorID string                         `json:"connector_id"`
	Signals     []MarketplaceRefundGraphSignal `json:"signals"`
	NotCash     bool                           `json:"not_cash"`
	Advisory    bool                           `json:"advisory"`
}

// MarketplaceVelocityConfig holds merchant thresholds and optional hold.
// HoldEnabled gates HoldRecommended only; agents never auto-block payouts.
type MarketplaceVelocityConfig struct {
	CountThreshold int64 `json:"count_threshold"`
	SumThreshold   int64 `json:"sum_threshold"`
	HoldEnabled    bool  `json:"hold_enabled"`
}

// MarketplaceVelocityFlag is an ops flag + audit for seller velocity breach.
// AutoBlockPayout is always false from agent/recon code paths.
type MarketplaceVelocityFlag struct {
	SellerID        string `json:"seller_id"`
	RefundCount     int64  `json:"refund_count"`
	RefundSumMinor  int64  `json:"refund_sum_minor"`
	Reason          string `json:"reason"`
	OpsFlag         bool   `json:"ops_flag"`
	AuditNote       string `json:"audit_note"`
	HoldRecommended bool   `json:"hold_recommended"`
	AutoBlockPayout bool   `json:"auto_block_payout"`
}

// MarketplaceVelocityResult scopes velocity flags to one tenant+connector.
type MarketplaceVelocityResult struct {
	TenantID    string                    `json:"tenant_id"`
	ConnectorID string                    `json:"connector_id"`
	Flags       []MarketplaceVelocityFlag `json:"flags"`
	NotCash     bool                      `json:"not_cash"`
	Advisory    bool                      `json:"advisory"`
}

type reverseKey struct {
	payment string
	seller  string
}

// DetectRefundsWithoutReverse returns ops counsel signals for marketplace
// refunds (JoinsSellerCluster + CountsTowardSellerPattern) that lack a
// matching reverse_transfer edge for the same payment_id + seller_id.
// Refunds without seller_id never join and never signal.
func DetectRefundsWithoutReverse(refunds []RefundFact, edges []MarketplaceTransferEdge) []MarketplaceRefundGraphSignal {
	hasReverse := map[reverseKey]struct{}{}
	// byRefund: refund_id → sellers whose reversed edge carries it. A refund_id
	// match is proof only when the edge seller equals the refund seller.
	byRefund := map[string]map[string]struct{}{}
	for _, e := range edges {
		if !e.HasReverse() {
			continue
		}
		sid := strings.TrimSpace(e.SellerID)
		pid := strings.TrimSpace(e.PaymentID)
		if sid != "" {
			hasReverse[reverseKey{payment: pid, seller: sid}] = struct{}{}
		}
		if rid := strings.TrimSpace(e.RefundID); rid != "" && sid != "" {
			if byRefund[rid] == nil {
				byRefund[rid] = map[string]struct{}{}
			}
			byRefund[rid][sid] = struct{}{}
		}
	}

	var out []MarketplaceRefundGraphSignal
	for _, r := range refunds {
		if !r.JoinsSellerCluster() || !CountsTowardSellerPattern(r) {
			continue
		}
		sid := strings.TrimSpace(r.SellerID)
		pid := strings.TrimSpace(r.PaymentID)
		rid := strings.TrimSpace(r.RefundID)
		if sellers, ok := byRefund[rid]; ok && rid != "" && sid != "" {
			if _, same := sellers[sid]; same {
				continue
			}
		}
		if _, ok := hasReverse[reverseKey{payment: pid, seller: sid}]; ok {
			continue
		}
		out = append(out, MarketplaceRefundGraphSignal{
			RefundID:       rid,
			PaymentID:      pid,
			SellerID:       sid,
			AmountMinor:    r.AmountMinor,
			Reason:         ReasonRefundWithoutReverse,
			ProviderStatus: strings.TrimSpace(r.ProviderStatus),
			Currency:       strings.TrimSpace(r.Currency),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SellerID != out[j].SellerID {
			return out[i].SellerID < out[j].SellerID
		}
		return out[i].RefundID < out[j].RefundID
	})
	return out
}

// EvaluateMarketplaceVelocity turns Slice 1 pattern breaches into ops flags
// with audit notes. HoldRecommended is set only when cfg.HoldEnabled is true.
// AutoBlockPayout is always false — agents never auto-block payouts.
func EvaluateMarketplaceVelocity(patterns []MarketplaceSellerPattern, cfg MarketplaceVelocityConfig) []MarketplaceVelocityFlag {
	advisories := EvaluateMarketplacePatternBreach(patterns, cfg.CountThreshold, cfg.SumThreshold)
	out := make([]MarketplaceVelocityFlag, 0, len(advisories))
	for _, a := range advisories {
		flag := MarketplaceVelocityFlag{
			SellerID:        a.SellerID,
			RefundCount:     a.RefundCount,
			RefundSumMinor:  a.RefundSumMinor,
			Reason:          a.Reason,
			OpsFlag:         true,
			AuditNote:       "marketplace_velocity:" + a.Reason,
			HoldRecommended: cfg.HoldEnabled,
			AutoBlockPayout: false,
		}
		out = append(out, flag)
	}
	return out
}
