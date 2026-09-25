package briefing

import (
	"regexp"
	"strings"
	"testing"
)

// forbiddenCashWords is an independent (non-helper) check: expected-cash text
// must never read as settled / banked / credited to bank / received.
var forbiddenCashWords = regexp.MustCompile(`\b(settled|banked|credited to (the )?bank|received|cash in bank)\b`)

func assertExpectedCashLabeled(t *testing.T, where, text string) {
	t.Helper()
	for _, s := range regexp.MustCompile(`[.;]\s+|[.;]$`).Split(text, -1) {
		if !MentionsExpectedCash(s) {
			continue
		}
		if !strings.Contains(s, ExpectedCashLabel) {
			t.Fatalf("%s: expected-cash sentence missing %q: %q", where, ExpectedCashLabel, s)
		}
		low := strings.ToLower(s)
		low = strings.ReplaceAll(low, "not yet banked", "")
		low = strings.ReplaceAll(low, "not bank credited", "")
		if forbiddenCashWords.MatchString(low) {
			t.Fatalf("%s: expected-cash sentence uses cash-in-bank wording: %q", where, s)
		}
	}
	if MatchedCalledCash(text) {
		t.Fatalf("%s: MATCHED called cash: %q", where, text)
	}
}

func opsFixture() OpsInputs {
	return OpsInputs{
		Close:              Report{Records: 10, Matched: 10, MatchRate: 1, ThroughputPerS: 5},
		ScheduleAvailable:  true,
		CashScheduleKind:   "schedule_projection",
		NextDayCreditMinor: 1200,
		NextDayDebitMinor:  300,
	}
}

func TestExpectedCashAlwaysLabeledProjectionInBriefing(t *testing.T) {
	cases := []OpsInputs{opsFixture(), {Close: Report{Records: 3, Matched: 3, MatchRate: 1}}}
	for _, in := range cases {
		r := WriteOps(in, nil)
		assertExpectedCashLabeled(t, "briefing", r.Briefing)
		assertExpectedCashLabeled(t, "human_next_step", r.HumanNextStep)
		for _, l := range r.Limitations {
			assertExpectedCashLabeled(t, "limitation", l)
		}
	}
	r := WriteOps(opsFixture(), nil)
	if !strings.Contains(r.Briefing, ExpectedCashLabel) || !strings.Contains(r.HumanNextStep, ExpectedCashLabel) {
		t.Fatalf("label missing: %s / %s", r.Briefing, r.HumanNextStep)
	}
}

func TestTemplateHasNoHardcodedMoney(t *testing.T) {
	in := OpsInputs{ScheduleAvailable: true, RefundGraphAvailable: true, VelocityAvailable: true, HoldEnabledOnFetch: true}
	out := TemplateMust(in) + " " + ChooseHumanNextStep(in)
	for _, n := range regexp.MustCompile(`\d+`).FindAllString(out, -1) {
		if n != "0" {
			t.Fatalf("template emitted literal %q not from inputs: %s", n, out)
		}
	}
}

func TestRewritePromptForbidsNewNumbersAndProjectionChanges(t *testing.T) {
	var got string
	WriteOps(opsFixture(), func(p string) (string, error) { got = p; return "", nil })
	for _, want := range []string{"Do not add", "number", ExpectedCashLabel, "projected", "settled", "banked", "MATCHED"} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt missing %q: %s", want, got)
		}
	}
}

func TestRewriteFallsBackOnProjectionOrMatchedWording(t *testing.T) {
	base := TemplateMust(opsFixture())
	bad := map[string]string{
		"label dropped":    strings.ReplaceAll(base, ExpectedCashLabel, "expected"),
		"called banked":    strings.ReplaceAll(base, ExpectedCashLabel, "banked"),
		"called settled":   strings.ReplaceAll(base, ExpectedCashLabel, "settled"),
		"matched cash":     base + " MATCHED records are cash in bank.",
		"new number":       strings.Replace(base, "1200", "1201", 1),
		"received wording": base + " Next banking-day expected credit 1200 was received.",
	}
	for name, rw := range bad {
		rw := rw
		r := WriteOps(opsFixture(), func(string) (string, error) { return rw, nil })
		if r.Source != "template" || r.Briefing != base {
			t.Fatalf("%s: rewrite must fall back to template, got %s: %s", name, r.Source, r.Briefing)
		}
	}
	ok := WriteOps(opsFixture(), func(string) (string, error) { return base, nil })
	if ok.Source != "gemini" {
		t.Fatalf("faithful rewrite should be accepted: %s", ok.Source)
	}
}
