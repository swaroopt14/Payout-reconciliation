package recon

import (
	"strings"
	"time"
)

// MatchCandidate is the shared vocabulary for payment, payout, and
// settlement↔bank selectors. Rule functions pick a Selection; they do not
// each invent their own candidate loop.
type MatchCandidate struct {
	ID          string
	AmountMinor int64
	Currency    string
	UTR         string
	ValueDate   time.Time
}

type Selection struct {
	IDs    []string
	Unique *MatchCandidate
}

func SelectByUTR(cands []MatchCandidate, utr string) Selection {
	utr = normalizeUTR(utr)
	if utr == "" {
		return Selection{}
	}
	var hit []MatchCandidate
	for _, c := range cands {
		if normalizeUTR(c.UTR) == utr {
			hit = append(hit, c)
		}
	}
	return selectionOf(hit)
}

func SelectByAmountWindow(cands []MatchCandidate, amount int64, currency string, at time.Time, window time.Duration) Selection {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	var hit []MatchCandidate
	for _, c := range cands {
		if c.AmountMinor != amount {
			continue
		}
		cc := strings.ToUpper(strings.TrimSpace(c.Currency))
		if currency != "" && cc != "" && cc != currency {
			continue
		}
		if !at.IsZero() && !c.ValueDate.IsZero() {
			delta := c.ValueDate.Sub(at)
			if delta < 0 {
				delta = -delta
			}
			if window <= 0 {
				window = defaultDateWindow
			}
			if delta > window {
				continue
			}
		}
		hit = append(hit, c)
	}
	return selectionOf(hit)
}

func selectionOf(hit []MatchCandidate) Selection {
	ids := make([]string, 0, len(hit))
	for _, c := range hit {
		ids = append(ids, c.ID)
	}
	out := Selection{IDs: ids}
	if len(hit) == 1 {
		c := hit[0]
		out.Unique = &c
	}
	return out
}

func bankCandidates(credits []BankTxn) []MatchCandidate {
	out := make([]MatchCandidate, 0, len(credits))
	for _, b := range credits {
		out = append(out, MatchCandidate{
			ID: b.ID, AmountMinor: b.CreditMinor, Currency: b.Currency,
			UTR: b.UTR, ValueDate: b.ValueDate,
		})
	}
	return out
}
