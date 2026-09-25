package recon

import (
	"sort"
	"strings"
	"time"
)

// Bank-landed suppression for the cash schedule projection.
//
// Match key (documented choice): BankTxn carries no refund/invoice/payment id
// column, only UTR / UTRRaw / BankTxnID / Description. So:
//  1. Reference pass: exact amount + same currency (nzCur) AND one of the
//     expected item's references (refund_id, payment_id, invoice_id,
//     order_id; len >= 6) appears in the bank line's UTR, UTRRaw, BankTxnID
//     or Description (case-insensitive).
//  2. Fallback pass: exact amount + same currency with the bank ValueDate
//     inside the projection window (see scheduleBankPool.window).
//
// Each bank line is consumed at most once (one bank line can never drop two
// refunds or two invoices). Bank lines already cited as evidence by a
// reconciliation result (EvidenceRefs.BankObservationID) are excluded so a
// payout- or settlement-proving bank row is not reused (no double-count).
// Callers must pass bank lines already filtered by tenant_id + connector_id
// (FinancialStore.ListBankTxns does this). Nothing here sets MATCHED or
// BankCreditProven; suppressed items are simply not projected.

type scheduleBankPool struct {
	lines []BankTxn
	used  map[int]bool
}

func newScheduleBankPool(banks []BankTxn, results []FinancialResult, debit bool) *scheduleBankPool {
	cited := map[string]bool{}
	for _, r := range results {
		if id := strings.TrimSpace(r.EvidenceRefs.BankObservationID); id != "" {
			cited[id] = true
		}
	}
	p := &scheduleBankPool{used: map[int]bool{}}
	for _, b := range banks {
		if b.ID != "" && cited[b.ID] {
			continue
		}
		if debit {
			if IsCredit(b) || b.DebitMinor <= 0 {
				continue
			}
		} else if !IsCredit(b) || b.CreditMinor <= 0 {
			continue
		}
		p.lines = append(p.lines, b)
	}
	sort.SliceStable(p.lines, func(i, j int) bool {
		if !p.lines[i].ValueDate.Equal(p.lines[j].ValueDate) {
			return p.lines[i].ValueDate.Before(p.lines[j].ValueDate)
		}
		return p.lines[i].ID < p.lines[j].ID
	})
	return p
}

type scheduleBankClaim struct {
	amount   int64
	currency string
	refs     []string
	// earliest/latest bound the fallback window; zero = unbounded side.
	earliest time.Time
	latest   time.Time
}

func bankAmount(b BankTxn) int64 {
	if IsCredit(b) {
		return b.CreditMinor
	}
	return b.DebitMinor
}

func bankHasRef(b BankTxn, refs []string) bool {
	hay := strings.ToUpper(strings.Join([]string{b.UTR, b.UTRRaw, b.BankTxnID, b.Description}, " "))
	for _, r := range refs {
		r = strings.ToUpper(strings.TrimSpace(r))
		if len(r) < 6 {
			continue
		}
		if strings.Contains(hay, r) {
			return true
		}
	}
	return false
}

func (p *scheduleBankPool) eligible(i int, c scheduleBankClaim) bool {
	if p.used[i] {
		return false
	}
	b := p.lines[i]
	if bankAmount(b) != c.amount || c.amount <= 0 {
		return false
	}
	return strings.EqualFold(nzCur(b.Currency), nzCur(c.currency))
}

func (p *scheduleBankPool) inWindow(b BankTxn, c scheduleBankClaim) bool {
	if b.ValueDate.IsZero() {
		return true
	}
	if !c.earliest.IsZero() && b.ValueDate.Before(c.earliest) {
		return false
	}
	if !c.latest.IsZero() && b.ValueDate.After(c.latest) {
		return false
	}
	return true
}

// claimAll resolves claims against the pool: all reference matches first,
// then amount+currency+window fallback. Returns landed[i] per claim.
func (p *scheduleBankPool) claimAll(claims []scheduleBankClaim) []bool {
	landed := make([]bool, len(claims))
	if p == nil || len(p.lines) == 0 {
		return landed
	}
	for ci, c := range claims {
		for i := range p.lines {
			if p.eligible(i, c) && bankHasRef(p.lines[i], c.refs) {
				p.used[i] = true
				landed[ci] = true
				break
			}
		}
	}
	for ci, c := range claims {
		if landed[ci] {
			continue
		}
		for i := range p.lines {
			if p.eligible(i, c) && p.inWindow(p.lines[i], c) {
				p.used[i] = true
				landed[ci] = true
				break
			}
		}
	}
	return landed
}
