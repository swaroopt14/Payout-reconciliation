package askzord

import (
	"regexp"
	"strings"
	"testing"

	"zord-prompt-layer/agents/briefing"
	"zord-prompt-layer/tools"
)

var forbiddenCashWords = regexp.MustCompile(`\b(settled|banked|credited to (the )?bank|received|cash in bank)\b`)

func assertProjectionLabeled(t *testing.T, where, text string) {
	t.Helper()
	for _, s := range regexp.MustCompile(`[.;]\s+|[.;]$`).Split(text, -1) {
		if !briefing.MentionsExpectedCash(s) {
			continue
		}
		if !strings.Contains(s, briefing.ExpectedCashLabel) {
			t.Fatalf("%s: expected-cash sentence missing projection label: %q", where, s)
		}
		low := strings.ToLower(s)
		low = strings.ReplaceAll(low, "not yet banked", "")
		low = strings.ReplaceAll(low, "not bank credited", "")
		if forbiddenCashWords.MatchString(low) {
			t.Fatalf("%s: expected-cash sentence uses cash-in-bank wording: %q", where, s)
		}
	}
	if briefing.MatchedCalledCash(text) {
		t.Fatalf("%s: MATCHED called cash: %q", where, text)
	}
}

func TestAskExpectedCashAlwaysLabeledProjection(t *testing.T) {
	srv := fixtureServer(t)
	defer srv.Close()
	c := tools.NewOutcomeClient(srv.URL, "")
	for _, q := range []string{
		"What is my morning briefing?",
		"What is the cash schedule for expected bank credits?",
		"How much cash is expected versus received in bank?",
		"What is our reconciliation rate?",
		"Show me the biggest unresolved issue.",
		"Is every settled payment credited to the bank?",
	} {
		resp := Ask(c, "tenant-a", "conn", q, EntityRef{})
		assertProjectionLabeled(t, q+" answer", resp.Answer)
		assertProjectionLabeled(t, q+" next_step", resp.HumanNextStep)
		for _, l := range resp.Limitations {
			assertProjectionLabeled(t, q+" limitation", l)
			if strings.Contains(l, "rejected") {
				t.Fatalf("%s: deterministic template should pass validation, got %q (answer=%s)", q, l, resp.Answer)
			}
		}
		for _, f := range resp.Facts {
			switch f.Field {
			case "expected_credit_minor", "expected_debit_minor", "cash_schedule_kind", "unknown_timing_minor":
				if !strings.Contains(f.Label, "projected, not yet banked") {
					t.Fatalf("%s: fact %s label=%q", q, f.Field, f.Label)
				}
			}
		}
	}
	resp := Ask(c, "tenant-a", "conn", "What is the cash schedule for expected bank credits?", EntityRef{})
	if !strings.Contains(resp.Answer, briefing.ExpectedCashLabel) {
		t.Fatalf("cash answer missing projection label: %s", resp.Answer)
	}
}

func TestValidateRejectsProjectionAsCashAndSmallInventedNumbers(t *testing.T) {
	ctx := FinanceContext{Facts: []Fact{
		{Field: "expected_credit_minor", Value: 1500},
		{Field: "expected_debit_minor", Value: 200},
	}}
	good := "Next banking-day " + briefing.ExpectedCashLabel + " credit 1500 and debit 200."
	if out, _ := Validate(good, ctx); out == "" || RejectRewrite(good, ctx) {
		t.Fatalf("labeled projection should pass")
	}
	for _, bad := range []string{
		"Expected credit 1500 has been banked.",
		"Next banking-day expected credit 1500 and debit 200.",
		"Expected credit 1500 was received.",
		"Expected (projected, not yet banked) credit 1500 is settled.",
		"MATCHED payments are cash in bank.",
		"Next banking-day " + briefing.ExpectedCashLabel + " credit 1500 and debit 7.",
	} {
		if out, _ := Validate(bad, ctx); out != "" {
			t.Fatalf("Validate should reject %q", bad)
		}
		if !RejectRewrite(bad, ctx) {
			t.Fatalf("RejectRewrite should reject %q", bad)
		}
	}
}

func TestBuildAnswerFallsBackToTemplateOnUngroundedNumber(t *testing.T) {
	ctx := FinanceContext{
		Plan:       QueryPlan{Intent: IntentInvestigation},
		Facts:      []Fact{{Field: "exposure_minor", Value: 5000, Currency: "INR"}},
		Exceptions: []map[string]any{{"reason": "amount_mismatch", "variance_amount": 777}},
	}
	resp := BuildAnswer(ctx)
	if strings.Contains(resp.Answer, "777") {
		t.Fatalf("ungrounded number leaked: %s", resp.Answer)
	}
	if !strings.Contains(resp.Answer, "Exposure is 5000") || !containsLimitation(resp, "Numeric claim rejected") {
		t.Fatalf("expected fallback template, got %s %v", resp.Answer, resp.Limitations)
	}
}
