package briefing

import (
	"regexp"
	"strings"
)

// ExpectedCashLabel must accompany every expected-cash mention
// (schedule_projection, expected_credit_minor, expected_debit_minor) shown to users.
const ExpectedCashLabel = "expected (projected, not yet banked)"

var (
	sentenceSplitRe = regexp.MustCompile(`[.;!?]\s+|[.;!?]$|\n+`)
	// Words that would present expected cash / MATCHED as money in the bank.
	cashClaimRe   = regexp.MustCompile(`\b(settled|banked|credited|received|cash in bank|in the bank|in bank)\b`)
	matchedCashRe = regexp.MustCompile(`\b(cash|banked|credited|received|in the bank|in bank)\b`)
	// Negated phrases that are explicitly allowed (they deny the cash claim).
	negatedPhrases = []string{
		"not yet banked", "not bank credited", "not bank_credited", "not bank-proven",
		"not bank proven", "not cash", "not fully reconciled", "not fully_reconciled",
	}
)

func sentences(text string) []string {
	return sentenceSplitRe.Split(text, -1)
}

func stripNegated(low string) string {
	for _, p := range negatedPhrases {
		low = strings.ReplaceAll(low, p, " ")
	}
	return low
}

// MentionsExpectedCash reports whether a sentence talks about projected/expected cash.
func MentionsExpectedCash(sentence string) bool {
	low := strings.ToLower(sentence)
	if strings.Contains(low, "schedule_projection") || strings.Contains(low, "expected_credit") ||
		strings.Contains(low, "expected_debit") {
		return true
	}
	if !strings.Contains(low, "expected") {
		return false
	}
	for _, w := range []string{"credit", "debit", "cash", "net", "inflow", "outflow"} {
		if strings.Contains(low, w) {
			return true
		}
	}
	return false
}

// ExpectedCashWordingOK is true when every expected-cash sentence carries the
// projection label and never presents the amount as settled/banked/credited/received.
func ExpectedCashWordingOK(text string) bool {
	for _, s := range sentences(text) {
		if !MentionsExpectedCash(s) {
			continue
		}
		low := strings.ToLower(s)
		if !strings.Contains(low, "projected") {
			return false
		}
		if cashClaimRe.MatchString(stripNegated(low)) {
			return false
		}
	}
	return true
}

// MatchedCalledCash is true when a sentence using the MATCHED status calls it cash/banked.
func MatchedCalledCash(text string) bool {
	for _, s := range sentences(text) {
		if !strings.Contains(s, "MATCHED") {
			continue
		}
		if matchedCashRe.MatchString(stripNegated(strings.ToLower(s))) {
			return true
		}
	}
	return false
}
