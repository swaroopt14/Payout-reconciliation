package briefing

import (
	"strings"
	"testing"
)

func TestTemplateHasNoLossClaim(t *testing.T) {
	r := Write(Report{Records: 120, Matched: 106, Exceptions: 14, MatchRate: 0.883, UnresolvedExposureMinor: 12845, FalseResolutions: 0, ThroughputPerS: 1800}, nil)
	if r.Source != "template" {
		t.Fatalf("source=%s", r.Source)
	}
	if containsAny(r.Briefing, "STUCK") {
		t.Fatalf("%s", r.Briefing)
	}
	low := toLower(r.Briefing)
	if contains(low, "we lost") || contains(low, "was lost") {
		t.Fatalf("%s", r.Briefing)
	}
	if contains(low, "is fully reconciled") && !contains(low, "not fully") {
		t.Fatalf("%s", r.Briefing)
	}
	if r.HumanNextStep == "" {
		t.Fatal("expected HumanNextStep")
	}
}

func TestRewriteInjectingLossIsDiscarded(t *testing.T) {
	r := Write(Report{Records: 10, Matched: 8, Exceptions: 2, MatchRate: 0.8, UnresolvedExposureMinor: 5000}, func(string) (string, error) {
		return "We lost 50000 rupees and 13 records are STUCK.", nil
	})
	if r.Source != "template" {
		t.Fatalf("must discard rewrite, got %s: %s", r.Source, r.Briefing)
	}
}

func TestRewriteKeepingNumbersAccepted(t *testing.T) {
	rep := Report{Records: 10, Matched: 8, Exceptions: 2, MatchRate: 0.8, UnresolvedExposureMinor: 5000, ThroughputPerS: 12}
	base := Template(rep)
	r := Write(rep, func(string) (string, error) { return base, nil })
	if r.Source != "gemini" {
		t.Fatalf("source=%s", r.Source)
	}
}

func TestOpsTemplateCopiesScheduleAndLimitations(t *testing.T) {
	in := OpsInputs{
		Close:              Report{Records: 10, Matched: 10, Exceptions: 0, MatchRate: 1, UnresolvedExposureMinor: 0, ThroughputPerS: 5},
		ScheduleAvailable:  true,
		CashScheduleKind:   "schedule_projection",
		NextDayCreditMinor: 1200,
		NextDayDebitMinor:  300,
	}
	r := WriteOps(in, nil)
	if !strings.Contains(r.Briefing, "1200") || !strings.Contains(r.Briefing, "300") {
		t.Fatal(r.Briefing)
	}
	if !strings.Contains(r.Briefing, limScheduleProjection) {
		t.Fatal(r.Briefing)
	}
	if !strings.Contains(r.Briefing, limMatchedNotCash) {
		t.Fatal(r.Briefing)
	}
	if !strings.Contains(r.Briefing, limHoldCounselOnly) {
		t.Fatal(r.Briefing)
	}
	if !strings.Contains(r.HumanNextStep, "1200") || !strings.Contains(r.HumanNextStep, "300") {
		t.Fatalf("next step should confirm schedule: %s", r.HumanNextStep)
	}
}

func TestHumanNextStepPriorityRefundGraphFirst(t *testing.T) {
	in := OpsInputs{
		Close:                   Report{Exceptions: 5, UnresolvedExposureMinor: 999},
		HasRefundWithoutReverse: true,
		RefundGraphAvailable:    true,
		RefundGraphCount:        2,
		RefundGraphReasons:      []string{ReasonRefundWithoutReverse},
		HoldEnabledOnFetch:      true,
		HoldRecommendedCount:    3,
		VelocityAvailable:       true,
		ScheduleAvailable:       true,
		NextDayCreditMinor:      1,
	}
	step := ChooseHumanNextStep(in)
	if !strings.Contains(step, "refund_without_reverse_transfer") {
		t.Fatal(step)
	}
	if strings.Contains(toLower(step), "hold") || strings.Contains(toLower(step), "credit") {
		t.Fatalf("refund graph must win: %s", step)
	}
}

func TestHumanNextStepPriorityHoldThenExceptionsThenSchedule(t *testing.T) {
	hold := OpsInputs{
		Close:                Report{Exceptions: 2, UnresolvedExposureMinor: 100},
		HoldEnabledOnFetch:   true,
		HoldRecommendedCount: 1,
		VelocityAvailable:    true,
	}
	if !strings.Contains(ChooseHumanNextStep(hold), "velocity hold") {
		t.Fatal(ChooseHumanNextStep(hold))
	}

	ex := OpsInputs{
		Close: Report{Exceptions: 2, UnresolvedExposureMinor: 100},
	}
	if !strings.Contains(ChooseHumanNextStep(ex), "100") {
		t.Fatal(ChooseHumanNextStep(ex))
	}

	sch := OpsInputs{
		ScheduleAvailable:  true,
		NextDayCreditMinor: 50,
		NextDayDebitMinor:  7,
	}
	step := ChooseHumanNextStep(sch)
	if !strings.Contains(step, "50") || !strings.Contains(step, "7") {
		t.Fatal(step)
	}
}

func TestHoldRecommendedIgnoredWhenHoldDisabled(t *testing.T) {
	in := OpsInputs{
		Close:                Report{},
		HoldEnabledOnFetch:   false,
		HoldRecommendedCount: 5, // must not drive next step when HoldEnabled false
		VelocityAvailable:    true,
		ScheduleAvailable:    true,
		NextDayCreditMinor:   11,
		NextDayDebitMinor:    2,
	}
	step := ChooseHumanNextStep(in)
	if strings.Contains(toLower(step), "hold") {
		t.Fatal(step)
	}
	if !strings.Contains(step, "11") {
		t.Fatal(step)
	}
}

func TestWriteOpsSoftMissingSurfacesStillBrief(t *testing.T) {
	r := WriteOps(OpsInputs{
		Close: Report{Records: 4, Matched: 4, Exceptions: 0, MatchRate: 1, ThroughputPerS: 1},
	}, nil)
	if r.Briefing == "" {
		t.Fatal("expected briefing from close facts alone")
	}
	joined := strings.Join(r.Limitations, " ")
	if !strings.Contains(joined, "Cash schedule") {
		t.Fatalf("%v", r.Limitations)
	}
	if !strings.Contains(joined, "Refund-graph") {
		t.Fatalf("%v", r.Limitations)
	}
	if !strings.Contains(joined, "Velocity") {
		t.Fatalf("%v", r.Limitations)
	}
}

func TestApplyHelpersCopyOnlyPresentFields(t *testing.T) {
	in := OpsInputs{}
	applyCashSchedule(&in, map[string]any{
		"kind": "schedule_projection",
		"days": []any{map[string]any{"expected_credit_minor": float64(42), "expected_debit_minor": float64(9)}},
	})
	if !in.ScheduleAvailable || in.NextDayCreditMinor != 42 || in.NextDayDebitMinor != 9 {
		t.Fatalf("%+v", in)
	}
	applyRefundGraph(&in, map[string]any{
		"signals": []any{map[string]any{"reason": ReasonRefundWithoutReverse, "amount_minor": float64(1)}},
	})
	if !in.HasRefundWithoutReverse || in.RefundGraphCount != 1 {
		t.Fatalf("%+v", in)
	}
	applyVelocity(&in, map[string]any{
		"flags": []any{map[string]any{"hold_recommended": true}, map[string]any{"hold_recommended": false}},
	}, true)
	if in.HoldRecommendedCount != 1 || !in.HoldEnabledOnFetch {
		t.Fatalf("%+v", in)
	}
	in2 := OpsInputs{}
	applyVelocity(&in2, map[string]any{
		"flags": []any{map[string]any{"hold_recommended": true}},
	}, false)
	if in2.HoldRecommendedCount != 0 {
		t.Fatalf("must not count HoldRecommended when HoldEnabled false: %+v", in2)
	}
	in3 := OpsInputs{}
	applyCashSchedule(&in3, map[string]any{"error": "not_found"})
	if in3.ScheduleAvailable {
		t.Fatal("soft missing must not mark available")
	}
}

func containsAny(s string, needles ...string) bool {
	low := toLower(s)
	for _, n := range needles {
		if contains(low, toLower(n)) {
			return true
		}
	}
	return false
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
