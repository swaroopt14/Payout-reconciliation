package askzord

import (
	"strings"

	"zord-prompt-layer/agents/finance"
	"zord-prompt-layer/tools"
)

func IsFinanceQuestion(q string) bool {
	if finance.IsFinanceQuery(q) {
		return true
	}
	s := strings.ToLower(q)
	if tools.ExtractPaymentID(q) != "" || tools.ExtractPayoutID(q) != "" {
		return true
	}
	knowledge := []string{
		"difference between", "what does", "what is settlement", "what is matched",
		"bank credit", "fully reconciled", "reconciliation policy", "payout sla",
		"settlement status", "exception category",
	}
	for _, p := range knowledge {
		if strings.Contains(s, p) {
			return true
		}
	}
	agg := []string{
		"how much", "unresolved", "reconciliation rate", "cash", "expected versus",
		"expected vs", "biggest", "top exception", "money moved", "did we lose",
		"every settled", "credited to the bank",
	}
	for _, p := range agg {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

func Plan(question string, inherit EntityRef) QueryPlan {
	s := strings.ToLower(strings.TrimSpace(question))
	p := QueryPlan{Filters: map[string]string{}}
	p.LossQuestion = strings.Contains(s, "lose") || strings.Contains(s, "lost") || strings.Contains(s, "permanent loss")
	p.BankCauseQuestion = strings.Contains(s, "which bank") || strings.Contains(s, "what bank caused")
	p.SettledAllQuestion = strings.Contains(s, "every settled") || strings.Contains(s, "all settled") ||
		(strings.Contains(s, "settled") && strings.Contains(s, "credited"))

	if id := tools.ExtractPayoutID(question); id != "" {
		p.Entity = EntityRef{Type: "payout", ID: id}
	} else if id := tools.ExtractPaymentID(question); id != "" {
		p.Entity = EntityRef{Type: "payment", ID: id}
	} else if inherit.ID != "" {
		p.Entity = inherit
	}

	switch {
	case p.Entity.ID == "" && isKnowledge(s):
		p.Intent = IntentKnowledge
		p.RequiredSources = []string{"knowledge"}
	case strings.Contains(s, "reconciliation rate") || (strings.Contains(s, "rate") && strings.Contains(s, "reconcil")):
		p.Intent = IntentReconciliation
		p.RequiredSources = []string{"summary", "exception"}
		p.Metrics = []string{"count", "matched", "exposure"}
	case strings.Contains(s, "expected") && (strings.Contains(s, "received") || strings.Contains(s, "bank") || strings.Contains(s, "cash")):
		p.Intent = IntentCashPosition
		p.RequiredSources = []string{"summary", "exception"}
		p.Metrics = []string{"exposure"}
	case strings.Contains(s, "cash schedule") || (strings.Contains(s, "in-flight") && strings.Contains(s, "cash")) ||
		(strings.Contains(s, "when does") && strings.Contains(s, "cash")):
		p.Intent = IntentCashPosition
		p.RequiredSources = []string{"summary", "exception"}
		p.Metrics = []string{"exposure"}
	case strings.Contains(s, "biggest") || strings.Contains(s, "top exception") ||
		(strings.Contains(s, "failed") && strings.Contains(s, "money moved")) ||
		strings.Contains(s, "biggest unresolved"):
		p.Intent = IntentInvestigation
		p.RequiredSources = []string{"exception", "evidence"}
		p.Metrics = []string{"exposure"}
	case p.Entity.ID == "" && (strings.Contains(s, "how much") || strings.Contains(s, "unresolved") ||
		p.LossQuestion || p.SettledAllQuestion || strings.Contains(s, "cash")):
		p.Intent = IntentAggregate
		p.RequiredSources = []string{"summary", "exception"}
		p.Metrics = []string{"count", "total_amount"}
	case p.Entity.ID != "" && (strings.Contains(s, "why") || strings.Contains(s, "root cause") ||
		strings.Contains(s, "unresolved") || strings.Contains(s, "unexplained") || strings.Contains(s, "investigat") ||
		p.LossQuestion):
		p.Intent = IntentExplanation
		p.RequiredSources = recordSources(p.Entity.Type)
		p.RequiredSources = append(p.RequiredSources, "evidence")
	case p.Entity.ID != "":
		p.Intent = IntentRecord
		p.RequiredSources = recordSources(p.Entity.Type)
		if strings.Contains(s, "refund") {
			p.RequiredSources = append(p.RequiredSources, "refund")
		}
		if strings.Contains(s, "ledger") {
			p.RequiredSources = append(p.RequiredSources, "ledger")
		}
		if strings.Contains(s, "tax") || strings.Contains(s, "fee") || strings.Contains(s, "net") {
			p.RequiredSources = append(p.RequiredSources, "tax")
		}
	default:
		if isKnowledge(s) {
			p.Intent = IntentKnowledge
			p.RequiredSources = []string{"knowledge"}
		} else {
			p.Intent = IntentKnowledge
			p.RequiredSources = []string{"knowledge"}
		}
	}
	if p.Entity.ID != "" && p.Entity.Type != "payout" &&
		(strings.Contains(s, "tax") || strings.Contains(s, "fee") || strings.Contains(s, "net ₹") || strings.Contains(s, "net ")) {
		p.RequiredSources = append(p.RequiredSources, "tax")
	}
	return p
}

func recordSources(entityType string) []string {
	if entityType == "payout" {
		return []string{"payout", "bank", "reconciliation", "evidence"}
	}
	return []string{"payment", "settlement", "bank", "refund", "reconciliation", "evidence"}
}

func isKnowledge(s string) bool {
	if strings.Contains(s, "difference between") || strings.Contains(s, "what does") ||
		strings.Contains(s, "what is the difference") || strings.Contains(s, "policy") ||
		strings.Contains(s, "what is settlement") || strings.Contains(s, "what is matched") ||
		strings.Contains(s, "payout sla") || strings.Contains(s, "exception category") ||
		strings.Contains(s, "mean") && (strings.Contains(s, "settlement") || strings.Contains(s, "matched") || strings.Contains(s, "unresolved")) {
		return true
	}
	return false
}

func EntityFromText(texts ...string) EntityRef {
	for _, t := range texts {
		if id := tools.ExtractPayoutID(t); id != "" {
			return EntityRef{Type: "payout", ID: id}
		}
		if id := tools.ExtractPaymentID(t); id != "" {
			return EntityRef{Type: "payment", ID: id}
		}
	}
	return EntityRef{}
}
