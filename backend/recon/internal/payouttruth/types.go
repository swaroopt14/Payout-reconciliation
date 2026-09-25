package payouttruth

import (
	"time"

	"zord-outcome-engine/internal/poll/providers/razorpay"
)

type Observation struct {
	TenantID         string
	ConnectorID      string
	Provider         string
	PayoutID         string
	AmountMinor      int64
	Currency         string
	ProviderStatus   string
	UTR              string
	Mode             string
	Purpose          string
	StatusReason     string
	ProviderCreatedAt time.Time
	ObservedAt       time.Time
	Source           string
	SourceEventID    string
	SourceHash       string
	RawReference     string
	IdentityHash     string
}

type CanonicalPayout struct {
	ID                string
	TenantID          string
	ConnectorID       string
	Provider          string
	PayoutID          string
	AmountMinor       int64
	Currency          string
	ProviderStatus    string
	UTR               string
	Mode              string
	Purpose           string
	StatusReason      string
	ProviderCreatedAt time.Time
	FirstObservedAt   time.Time
	LastObservedAt    time.Time
	Sources           []string
	// AmountConflictMinor (B5): a later observation carried a different
	// amount. AmountMinor keeps the first observed amount; nil = no conflict.
	AmountConflictMinor *int64
}

func Reduce(current CanonicalPayout, incoming Observation) CanonicalPayout {
	now := incoming.ObservedAt
	if current.PayoutID == "" {
		return CanonicalPayout{
			TenantID: incoming.TenantID, ConnectorID: incoming.ConnectorID, Provider: incoming.Provider,
			PayoutID: incoming.PayoutID, AmountMinor: incoming.AmountMinor, Currency: incoming.Currency,
			ProviderStatus: incoming.ProviderStatus, UTR: incoming.UTR, Mode: incoming.Mode,
			Purpose: incoming.Purpose, StatusReason: incoming.StatusReason,
			ProviderCreatedAt: incoming.ProviderCreatedAt, FirstObservedAt: now, LastObservedAt: now,
			Sources: []string{incoming.Source},
		}
	}
	out := current
	out.LastObservedAt = now
	out.Sources = appendUnique(out.Sources, incoming.Source)
	// B5: never overwrite a known amount; surface a different one instead.
	if incoming.AmountMinor > 0 {
		switch {
		case out.AmountMinor <= 0:
			out.AmountMinor = incoming.AmountMinor
		case incoming.AmountMinor != out.AmountMinor:
			amt := incoming.AmountMinor
			out.AmountConflictMinor = &amt
		}
	}
	if incoming.Currency != "" {
		out.Currency = incoming.Currency
	}
	if incoming.UTR != "" {
		out.UTR = incoming.UTR
	}
	if incoming.Mode != "" {
		out.Mode = incoming.Mode
	}
	if incoming.Purpose != "" {
		out.Purpose = incoming.Purpose
	}
	if incoming.StatusReason != "" {
		out.StatusReason = incoming.StatusReason
	}
	if out.ProviderCreatedAt.IsZero() && !incoming.ProviderCreatedAt.IsZero() {
		out.ProviderCreatedAt = incoming.ProviderCreatedAt
	}
	out.ProviderStatus = nextStatus(current.ProviderStatus, incoming.ProviderStatus)
	return out
}

func nextStatus(current, incoming string) string {
	cur := razorpay.NormalizePayoutStatus(current)
	in := razorpay.NormalizePayoutStatus(incoming)
	if cur == "" {
		return in
	}
	if razorpay.IsPayoutProcessed(cur) && razorpay.IsPayoutFailedLike(in) {
		return cur
	}
	if razorpay.IsPayoutProcessed(cur) && razorpay.IsPayoutOpen(in) {
		return cur
	}
	if razorpay.PayoutRank(in) >= razorpay.PayoutRank(cur) {
		return in
	}
	return cur
}

func appendUnique(existing []string, source string) []string {
	for _, s := range existing {
		if s == source {
			return existing
		}
	}
	return append(append([]string(nil), existing...), source)
}
