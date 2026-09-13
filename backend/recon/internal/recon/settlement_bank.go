package recon

import (
	"strings"
	"sync/atomic"
	"time"
)

const (
	BankMatchExact          = "EXACT_MATCH"
	BankMatchHighConfidence = "HIGH_CONFIDENCE"
	BankMatchAmbiguous      = "AMBIGUOUS"
	BankMatchUnresolved     = "UNRESOLVED"
	BankMatchConflicted     = "CONFLICTED"
	BankMatchVariance       = "VARIANCE"
	BankMatchOrphanBank     = "ORPHAN_BANK"
)

var (
	MetricBankUTRExactMatches atomic.Int64
	MetricBankAmbiguous       atomic.Int64
	MetricBankUnresolved      atomic.Int64
	MetricBankConflicted      atomic.Int64
	MetricBankOrphan          atomic.Int64
	MetricBankHighConfidence  atomic.Int64
)

type SettlementBankDecision struct {
	ID                string
	TenantID          string
	ConnectorID       string
	SettlementLineID  string
	BankObservationID string
	State             string
	Confidence        float64
	Rule              string
	Candidates        []string
	Evidence          map[string]any
	DecidedAt         time.Time
}

// MatchSettlementBank produces Settlement↔Bank candidates only.
// It never sets ProofVerified or ReconFullyReconciled.
func MatchSettlementBank(lines []SettlementLine, banks []BankTxn) []SettlementBankDecision {
	credits := make([]BankTxn, 0, len(banks))
	for _, b := range banks {
		if isBankCredit(b) {
			credits = append(credits, b)
		}
	}
	used := map[string]struct{}{}
	var out []SettlementBankDecision
	now := time.Now().UTC()
	for _, line := range lines {
		d := matchOneSettlementBank(line, credits)
		d.DecidedAt = now
		out = append(out, d)
		if d.BankObservationID != "" {
			used[d.BankObservationID] = struct{}{}
		}
		for _, id := range d.Candidates {
			used[id] = struct{}{}
		}
		bumpBankMatchMetric(d.State)
	}
	for _, b := range credits {
		if _, ok := used[b.ID]; ok {
			continue
		}
		out = append(out, SettlementBankDecision{
			BankObservationID: b.ID,
			State:             BankMatchOrphanBank,
			Rule:              "orphan_bank_credit",
			Evidence: map[string]any{
				"credit_minor": b.CreditMinor,
				"utr":          b.UTR,
				"currency":     b.Currency,
			},
			DecidedAt: now,
		})
		MetricBankOrphan.Add(1)
	}
	return out
}

func bumpBankMatchMetric(state string) {
	switch state {
	case BankMatchExact:
		MetricBankUTRExactMatches.Add(1)
	case BankMatchAmbiguous:
		MetricBankAmbiguous.Add(1)
	case BankMatchUnresolved:
		MetricBankUnresolved.Add(1)
	case BankMatchConflicted, BankMatchVariance:
		MetricBankConflicted.Add(1)
	case BankMatchHighConfidence:
		MetricBankHighConfidence.Add(1)
	}
}

func matchOneSettlementBank(line SettlementLine, credits []BankTxn) SettlementBankDecision {
	net := lineNet(line)
	lineID := line.ID
	if lineID == "" {
		lineID = line.EntityID
	}
	base := SettlementBankDecision{
		SettlementLineID: lineID,
		Evidence: map[string]any{
			"settlement_net": net,
			"currency":       line.Currency,
			"utr":            line.UTR,
		},
	}
	cands := bankCandidates(credits)
	utr := strings.TrimSpace(line.UTR)
	if utr != "" {
		sel := SelectByUTR(cands, utr)
		if sel.Unique != nil {
			return decideUniqueUTR(base, line, bankByID(credits, sel.Unique.ID), net)
		}
		if len(sel.IDs) > 1 {
			base.State = BankMatchAmbiguous
			base.Rule = "duplicate_utr_candidates"
			base.Candidates = sel.IDs
			base.Evidence["candidates"] = sel.IDs
			return base
		}
		base.State = BankMatchUnresolved
		base.Rule = "utr_not_in_bank"
		return base
	}

	sel := SelectByAmountWindow(cands, net, line.Currency, line.SettledAt, defaultDateWindow)
	amountCands := banksByIDs(credits, sel.IDs)
	if len(amountCands) == 1 {
		b := amountCands[0]
		conf, breakdown := ScoreUTRAndAmount(false, true, b.Currency == line.Currency || line.Currency == "", true)
		if narrationBoost(line, b) {
			conf += ScoreDescription
			breakdown["description_similarity"] = ScoreDescription
		}
		base.BankObservationID = b.ID
		base.State = BankMatchHighConfidence
		base.Rule = MatchNetAmountDateAccount
		base.Confidence = conf
		base.Candidates = []string{b.ID}
		base.Evidence["score_breakdown"] = breakdown
		return base
	}
	if len(amountCands) > 1 {
		ids := bankIDs(amountCands)
		base.State = BankMatchAmbiguous
		base.Rule = "duplicate_amount_date_candidates"
		base.Candidates = ids
		base.Evidence["candidates"] = ids
		return base
	}
	base.State = BankMatchUnresolved
	base.Rule = "no_bank_credit"
	return base
}

func decideUniqueUTR(base SettlementBankDecision, line SettlementLine, b BankTxn, net int64) SettlementBankDecision {
	currencyOK := b.Currency == line.Currency || line.Currency == "" || b.Currency == ""
	inWindow := inDateWindow(line.SettledAt, b.ValueDate)
	conf, breakdown := ScoreUTRAndAmount(true, b.CreditMinor == net, currencyOK, inWindow)
	base.BankObservationID = b.ID
	base.Candidates = []string{b.ID}
	base.Confidence = conf
	base.Evidence["bank_credit_minor"] = b.CreditMinor
	base.Evidence["score_breakdown"] = breakdown
	if b.CreditMinor != net || !currencyOK {
		base.State = BankMatchConflicted
		base.Rule = BankMatchVariance
		base.Evidence["difference_minor"] = net - b.CreditMinor
		return base
	}
	base.State = BankMatchExact
	base.Rule = MatchExactUTRAndAmount
	return base
}

func isBankCredit(b BankTxn) bool {
	if strings.EqualFold(b.CreditDebit, "DEBIT") {
		return false
	}
	if strings.EqualFold(b.CreditDebit, "CREDIT") {
		return true
	}
	return b.CreditMinor > 0 && b.DebitMinor == 0
}

func inDateWindow(settledAt, valueDate time.Time) bool {
	if settledAt.IsZero() || valueDate.IsZero() {
		return true
	}
	delta := valueDate.Sub(settledAt)
	if delta < 0 {
		delta = -delta
	}
	return delta <= defaultDateWindow
}

func narrationBoost(line SettlementLine, b BankTxn) bool {
	blob := strings.ToLower(b.Description)
	if line.SettlementID != "" && strings.Contains(blob, strings.ToLower(line.SettlementID)) {
		return true
	}
	if line.UTR != "" && strings.Contains(blob, strings.ToLower(line.UTR)) {
		return true
	}
	return false
}

func bankIDs(banks []BankTxn) []string {
	ids := make([]string, 0, len(banks))
	for _, b := range banks {
		ids = append(ids, b.ID)
	}
	return ids
}

func bankByID(banks []BankTxn, id string) BankTxn {
	for _, b := range banks {
		if b.ID == id {
			return b
		}
	}
	return BankTxn{}
}

func banksByIDs(banks []BankTxn, ids []string) []BankTxn {
	want := map[string]struct{}{}
	for _, id := range ids {
		want[id] = struct{}{}
	}
	var out []BankTxn
	for _, b := range banks {
		if _, ok := want[b.ID]; ok {
			out = append(out, b)
		}
	}
	return out
}
