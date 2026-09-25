package briefing

import (
	"fmt"
	"regexp"
	"strings"
)

type Report struct {
	Records                 int     `json:"records"`
	Matched                 int     `json:"matched"`
	Exceptions              int     `json:"exceptions"`
	MatchRate               float64 `json:"match_rate"`
	UnresolvedExposureMinor int64   `json:"unresolved_exposure_minor"`
	FalseResolutions        int     `json:"false_resolutions"`
	ThroughputPerS          float64 `json:"throughput_per_s"`
}

// OpsInputs holds close-report facts plus cash schedule / marketplace counsel
// surfaces. All amounts must be copied from tool JSON; never invented.
type OpsInputs struct {
	Close Report

	CashScheduleKind   string
	NextDayCreditMinor int64
	NextDayDebitMinor  int64
	ScheduleAvailable  bool

	RefundGraphCount        int
	RefundGraphReasons      []string
	HasRefundWithoutReverse bool
	RefundGraphAvailable    bool

	VelocityFlagCount    int
	HoldRecommendedCount int
	HoldEnabledOnFetch   bool
	VelocityAvailable    bool
}

type Result struct {
	Briefing      string   `json:"briefing"`
	Source        string   `json:"source"`
	Limitations   []string `json:"limitations"`
	HumanNextStep string   `json:"human_next_step"`
}

type Rewriter func(prompt string) (string, error)

const (
	ReasonRefundWithoutReverse = "refund_without_reverse_transfer"

	limScheduleProjection = "schedule_projection amounts are expected (projected, not yet banked), not bank credited"
	limMatchedNotCash     = "MATCHED is not cash"
	limHoldCounselOnly    = "HoldRecommended is counsel only when HoldEnabled; no auto-block"
)

// Template is the close-only briefing (backward compatible).
func Template(r Report) string {
	return TemplateMust(OpsInputs{Close: r})
}

// TemplateMust copies ONLY numbers present in OpsInputs into prose.
// Two clocks: recon status (close) vs cash projection (schedule).
func TemplateMust(in OpsInputs) string {
	r := in.Close
	pct := int(r.MatchRate * 100)
	var b strings.Builder
	fmt.Fprintf(&b,
		"Closed %d records. Match rate %d percent: %d MATCHED, %d exceptions remain. Unresolved exposure is %d (copied from exception variance). False resolutions %d. Settled is not bank credited. MATCHED is not fully reconciled. Throughput %.0f records per second. Root causes are listed; none were guessed. ",
		r.Records, pct, r.Matched, r.Exceptions, r.UnresolvedExposureMinor, r.FalseResolutions, r.ThroughputPerS,
	)
	if in.ScheduleAvailable {
		kind := in.CashScheduleKind
		if kind == "" {
			kind = "schedule_projection"
		}
		fmt.Fprintf(&b,
			"Cash clock: next banking-day "+ExpectedCashLabel+" credit %d and debit %d from %s (projection only, not bank credited). ",
			in.NextDayCreditMinor, in.NextDayDebitMinor, kind,
		)
	}
	if in.RefundGraphAvailable {
		fmt.Fprintf(&b, "Refund-graph counsel signals: %d. ", in.RefundGraphCount)
		if in.HasRefundWithoutReverse {
			b.WriteString("Reason refund_without_reverse_transfer is present (counsel signal, not MATCHED). ")
		}
		if len(in.RefundGraphReasons) > 0 {
			fmt.Fprintf(&b, "Sample reason codes: %s. ", strings.Join(uniqueNonEmpty(in.RefundGraphReasons), ", "))
		}
	}
	if in.VelocityAvailable {
		fmt.Fprintf(&b, "Velocity flags: %d. ", in.VelocityFlagCount)
		if in.HoldEnabledOnFetch {
			fmt.Fprintf(&b, "HoldRecommended count %d (only because HoldEnabled was true on fetch). ", in.HoldRecommendedCount)
		} else {
			b.WriteString("HoldRecommended not applicable (HoldEnabled false on fetch). ")
		}
	}
	b.WriteString(limScheduleProjection + ". " + limMatchedNotCash + ". " + limHoldCounselOnly + ".")
	return strings.TrimSpace(b.String())
}

// ChooseHumanNextStep returns exactly one deterministic human next step.
func ChooseHumanNextStep(in OpsInputs) string {
	if in.HasRefundWithoutReverse || reasonPresent(in.RefundGraphReasons, ReasonRefundWithoutReverse) {
		return "Investigate marketplace refund graph exceptions with reason refund_without_reverse_transfer (counsel signal, not MATCHED)."
	}
	if in.HoldEnabledOnFetch && in.HoldRecommendedCount > 0 {
		return "Review velocity hold recommendations (HoldRecommended is counsel only when HoldEnabled; no auto-block)."
	}
	if in.Close.UnresolvedExposureMinor > 0 || in.Close.Exceptions > 0 {
		return fmt.Sprintf(
			"Work top exceptions; unresolved exposure is %d (copied from close report).",
			in.Close.UnresolvedExposureMinor,
		)
	}
	if in.ScheduleAvailable {
		return fmt.Sprintf(
			"Confirm next banking-day "+ExpectedCashLabel+" credit %d and debit %d from schedule_projection (not bank credited).",
			in.NextDayCreditMinor, in.NextDayDebitMinor,
		)
	}
	return "Confirm next banking-day " + ExpectedCashLabel + " credit and debit from the cash schedule when available."
}

func Write(r Report, rewrite Rewriter) Result {
	return WriteOps(OpsInputs{Close: r}, rewrite)
}

func WriteOps(in OpsInputs, rewrite Rewriter) Result {
	base := TemplateMust(in)
	out := Result{
		Briefing:      base,
		Source:        "template",
		Limitations:   defaultLimitations(in),
		HumanNextStep: ChooseHumanNextStep(in),
	}
	if rewrite == nil {
		return out
	}
	rewritten, err := rewrite(RewritePrompt + base)
	if err != nil || strings.TrimSpace(rewritten) == "" {
		return out
	}
	if !numbersSubset(base, rewritten) {
		out.Limitations = append(out.Limitations, "Gemini rewrite discarded: it introduced numbers not in the close report.")
		return out
	}
	if !ExpectedCashWordingOK(rewritten) || MatchedCalledCash(rewritten) ||
		(strings.Contains(base, ExpectedCashLabel) && !strings.Contains(rewritten, ExpectedCashLabel)) {
		out.Limitations = append(out.Limitations, "Gemini rewrite discarded: it changed expected-cash projection or MATCHED wording.")
		return out
	}
	out.Briefing = strings.TrimSpace(rewritten)
	out.Source = "gemini"
	return out
}

// RewritePrompt instructs the LLM to rephrase only; numbers and projection wording are fixed.
const RewritePrompt = "Rewrite this finance ops morning briefing in plain sentences. Rules: " +
	"Do not add, remove, round, or change any number; use only numbers already present. " +
	"Keep the exact phrase '" + ExpectedCashLabel + "' wherever it appears; do not change 'projected' or 'projection' wording. " +
	"Never describe expected or scheduled cash as settled, credited, received, banked, or cash in bank. " +
	"Never describe MATCHED as cash or banked.\n\n"

func defaultLimitations(in OpsInputs) []string {
	lims := []string{
		"Every amount is copied from recon close / cash schedule / marketplace counsel JSON. Gemini may only rephrase.",
		limScheduleProjection,
		limMatchedNotCash,
		limHoldCounselOnly,
	}
	if !in.ScheduleAvailable {
		lims = append(lims, "Cash schedule surface unavailable; cash projection clock omitted.")
	}
	if !in.RefundGraphAvailable {
		lims = append(lims, "Refund-graph exceptions surface unavailable.")
	}
	if !in.VelocityAvailable {
		lims = append(lims, "Velocity flags surface unavailable.")
	}
	return lims
}

var numRe = regexp.MustCompile(`\d+`)

func numbersSubset(allowed, candidate string) bool {
	allow := map[string]bool{}
	for _, n := range numRe.FindAllString(allowed, -1) {
		allow[n] = true
	}
	for _, n := range numRe.FindAllString(candidate, -1) {
		if !allow[n] {
			return false
		}
	}
	return true
}

func reasonPresent(reasons []string, want string) bool {
	for _, r := range reasons {
		if r == want {
			return true
		}
	}
	return false
}

func uniqueNonEmpty(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
