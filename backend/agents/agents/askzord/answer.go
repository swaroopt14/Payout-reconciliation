package askzord

import (
	"fmt"
	"strings"

	"zord-prompt-layer/agents/briefing"
)

func BuildAnswer(ctx FinanceContext) Response {
	resp := Response{
		Intent:       ctx.Plan.Intent,
		Facts:        ctx.Facts,
		Calculations: ctx.Calculations,
		Evidence:     ctx.Evidence,
		Limitations:  append([]string{}, ctx.Limitations...),
		Confidence:   confidence(ctx),
	}
	for _, k := range ctx.Knowledge {
		resp.Sources = append(resp.Sources, fmt.Sprintf("%s, %s", k.Title, k.Version))
	}

	switch ctx.Plan.Intent {
	case IntentKnowledge:
		resp.Answer = knowledgeAnswer(ctx)
	case IntentAggregate, IntentReconciliation:
		resp.Answer = aggregateAnswer(ctx)
	case IntentCashPosition:
		resp.Answer = cashPositionAnswer(ctx)
	case IntentInvestigation:
		resp.Answer = investigationAnswer(ctx)
	default:
		resp.Answer = recordAnswer(ctx)
	}
	if ctx.Plan.LossQuestion {
		exp := factInt(ctx, "exposure_minor")
		resp.Answer += fmt.Sprintf(" I can identify %d of unresolved financial exposure, but the available evidence does not establish that this amount represents a permanent loss.", exp)
		resp.Limitations = appendUnique(resp.Limitations, "Unresolved exposure is not proven permanent loss.")
	}
	if ctx.Plan.BankCauseQuestion {
		resp.Answer += " UNKNOWN: the available evidence does not establish which bank caused the failures."
		resp.Limitations = appendUnique(resp.Limitations, "UNKNOWN")
	}
	if ctx.Plan.SettledAllQuestion {
		unres := factInt(ctx, "count_unresolved")
		if unres == 0 {
			if n := factInt(ctx, "exposure_minor"); n > 0 {
				unres = 1
			}
		}
		resp.Answer += fmt.Sprintf(" No. Structured exceptions remain; settled is not bank credited. Unresolved exposure is %d.", factInt(ctx, "exposure_minor"))
		_ = unres
	}
	if ctx.Plan.Intent != IntentKnowledge {
		resp.Answer += citeEvidence(ctx.Evidence)
		resp.Answer += citeSources(ctx.Knowledge)
	} else {
		resp.Answer += citeSources(ctx.Knowledge)
	}

	cleaned, extra := Validate(resp.Answer, ctx)
	if cleaned == "" {
		resp.Answer = fallbackTemplate(ctx)
		resp.Limitations = append(resp.Limitations, extra...)
		cleaned, extra = Validate(resp.Answer, ctx)
		if cleaned != "" {
			resp.Answer = cleaned
		}
		resp.Limitations = append(resp.Limitations, extra...)
	} else {
		resp.Answer = cleaned
		resp.Limitations = append(resp.Limitations, extra...)
	}
	resp.HumanNextStep = humanNextStepFromContext(ctx)
	return resp
}

func humanNextStepFromContext(ctx FinanceContext) string {
	in := briefing.OpsInputs{
		Close: briefing.Report{
			Exceptions:              int(factInt(ctx, "count_unresolved") + factInt(ctx, "count_ambiguous") + factInt(ctx, "count_conflicted") + factInt(ctx, "count_variance") + factInt(ctx, "count_orphan")),
			UnresolvedExposureMinor: factInt(ctx, "exposure_minor"),
			Matched:                 int(factInt(ctx, "matched_count")),
			Records:                 int(factInt(ctx, "scored_count")),
		},
		ScheduleAvailable:       factString(ctx, "cash_schedule_kind") != "" || factPresent(ctx, "expected_credit_minor") || factPresent(ctx, "expected_debit_minor"),
		CashScheduleKind:        factString(ctx, "cash_schedule_kind"),
		NextDayCreditMinor:      factInt(ctx, "expected_credit_minor"),
		NextDayDebitMinor:       factInt(ctx, "expected_debit_minor"),
		RefundGraphAvailable:    factPresent(ctx, "refund_graph_signal_count"),
		RefundGraphCount:        int(factInt(ctx, "refund_graph_signal_count")),
		HasRefundWithoutReverse: factInt(ctx, "refund_without_reverse_count") > 0,
		VelocityAvailable:       factPresent(ctx, "velocity_flag_count"),
		VelocityFlagCount:       int(factInt(ctx, "velocity_flag_count")),
		HoldEnabledOnFetch:      factInt(ctx, "hold_enabled") == 1,
		HoldRecommendedCount:    int(factInt(ctx, "hold_recommended_count")),
	}
	if in.Close.Exceptions == 0 && in.Close.Records > in.Close.Matched {
		in.Close.Exceptions = in.Close.Records - in.Close.Matched
	}
	if in.HasRefundWithoutReverse {
		in.RefundGraphReasons = []string{briefing.ReasonRefundWithoutReverse}
	}
	return briefing.ChooseHumanNextStep(in)
}

func factPresent(ctx FinanceContext, field string) bool {
	for _, f := range ctx.Facts {
		if f.Field == field {
			return true
		}
	}
	return false
}

func recordAnswer(ctx FinanceContext) string {
	var b strings.Builder
	if ctx.MissingPrimary {
		return strings.Join(ctx.Limitations, " ")
	}
	if ctx.Plan.Entity.Type == "payout" {
		fmt.Fprintf(&b, "Payout %s: ", ctx.Plan.Entity.ID)
	} else if ctx.Plan.Entity.ID != "" {
		fmt.Fprintf(&b, "Payment %s: ", ctx.Plan.Entity.ID)
	}
	if st := factString(ctx, "provider_status"); st != "" {
		fmt.Fprintf(&b, "Razorpay status remains %s. ", st)
	}
	if res := factString(ctx, "reconciliation_result"); res != "" {
		fmt.Fprintf(&b, "Reconciliation result is %s. ", res)
		if res == "AMBIGUOUS" {
			b.WriteString("Candidates stay AMBIGUOUS; the agent cannot force MATCHED. ")
		}
	}
	if reason := factString(ctx, "reason"); reason != "" {
		fmt.Fprintf(&b, "Structured exception reason: %s. ", reason)
	}
	if fs := factString(ctx, "financial_state"); fs != "" {
		fmt.Fprintf(&b, "Financial state (our label, not a Razorpay status): %s. ", fs)
	}
	if exp := factInt(ctx, "exposure_minor"); true {
		fmt.Fprintf(&b, "Exposure is %d (copied from structured variance_amount). ", exp)
	}
	if factInt(ctx, "gross_minor") != 0 || factInt(ctx, "fee_minor") != 0 || factInt(ctx, "tax_minor") != 0 {
		fmt.Fprintf(&b, "Gross %d. Fee %d. Tax %d. Net %d. ",
			factInt(ctx, "gross_minor"), factInt(ctx, "fee_minor"), factInt(ctx, "tax_minor"), factInt(ctx, "net_minor"))
		if factString(ctx, "tax_reason") != "" {
			fmt.Fprintf(&b, "Fee/tax reason: %s. This is not an exception unless the structured result says so. ", factString(ctx, "tax_reason"))
		}
	}
	for _, lim := range ctx.Limitations {
		if strings.Contains(lim, "UNKNOWN") || strings.Contains(lim, "No settlement") || strings.Contains(lim, "No bank") || strings.Contains(lim, "Ledger") {
			b.WriteString(lim)
			if !strings.HasSuffix(strings.TrimSpace(lim), ".") {
				b.WriteString(".")
			}
			b.WriteString(" ")
		}
	}
	return strings.TrimSpace(b.String())
}

func aggregateAnswer(ctx FinanceContext) string {
	var b strings.Builder
	scored := factInt(ctx, "scored_count")
	matched := factInt(ctx, "matched_count")
	if scored > 0 {
		rate := matched * 100 / scored
		fmt.Fprintf(&b, "Reconciliation rate is %d percent: %d MATCHED of %d scored records. ", rate, matched, scored)
		for _, key := range []string{"ambiguous", "unresolved", "conflicted", "variance", "orphan"} {
			if n := factInt(ctx, "count_"+key); n > 0 {
				fmt.Fprintf(&b, "%s=%d. ", strings.ToUpper(key), n)
			}
		}
	}
	fmt.Fprintf(&b, "Unresolved financial exposure is %d (copied from exception variance_amount). ", factInt(ctx, "exposure_minor"))
	if factPresent(ctx, "expected_credit_minor") || factPresent(ctx, "expected_debit_minor") {
		fmt.Fprintf(&b, "Next banking-day "+briefing.ExpectedCashLabel+" credit %d and debit %d (schedule_projection, not bank credited). ",
			factInt(ctx, "expected_credit_minor"), factInt(ctx, "expected_debit_minor"))
	}
	if factInt(ctx, "refund_without_reverse_count") > 0 {
		fmt.Fprintf(&b, "refund_without_reverse_transfer signals: %d. ", factInt(ctx, "refund_without_reverse_count"))
	}
	if factInt(ctx, "hold_enabled") == 1 && factInt(ctx, "hold_recommended_count") > 0 {
		fmt.Fprintf(&b, "HoldRecommended flags: %d (counsel only; no auto-block). ", factInt(ctx, "hold_recommended_count"))
	}
	b.WriteString("Settled is not bank credited. MATCHED is not fully reconciled. MATCHED is not cash. ")
	return strings.TrimSpace(b.String())
}

func cashPositionAnswer(ctx FinanceContext) string {
	var b strings.Builder
	gross := factInt(ctx, "gross_captured_minor")
	settled := factInt(ctx, "settlement_expected_net_minor")
	bank := factInt(ctx, "bank_credited_proven_minor")
	inFlight := factInt(ctx, "in_flight_minor")
	exposure := factInt(ctx, "unresolved_exposure_minor")
	fmt.Fprintf(&b, "Captured payments total %d. Settlement expected net ("+briefing.ExpectedCashLabel+") is %d. Bank credit proven is %d. ", gross, settled, bank)
	if inFlight > 0 {
		fmt.Fprintf(&b, "In-flight cash (settled but not bank-proven) is %d. ", inFlight)
	}
	fmt.Fprintf(&b, "Unresolved exposure is %d. ", exposure)
	if kind := factString(ctx, "cash_schedule_kind"); kind != "" {
		fmt.Fprintf(&b, "Forward cash is a %s, "+briefing.ExpectedCashLabel+", not a statistical forecast. ", kind)
	}
	if factPresent(ctx, "expected_credit_minor") || factPresent(ctx, "expected_debit_minor") {
		fmt.Fprintf(&b, "Next banking-day "+briefing.ExpectedCashLabel+" credit %d and debit %d (copied from schedule days, not bank credited). ",
			factInt(ctx, "expected_credit_minor"), factInt(ctx, "expected_debit_minor"))
	}
	if n := factInt(ctx, "refund_graph_signal_count"); factPresent(ctx, "refund_graph_signal_count") {
		fmt.Fprintf(&b, "Refund-graph counsel signals: %d. ", n)
	}
	if factInt(ctx, "refund_without_reverse_count") > 0 {
		fmt.Fprintf(&b, "refund_without_reverse_transfer count %d (counsel, not MATCHED). ", factInt(ctx, "refund_without_reverse_count"))
	}
	if factPresent(ctx, "velocity_flag_count") {
		fmt.Fprintf(&b, "Velocity flags: %d. ", factInt(ctx, "velocity_flag_count"))
		if factInt(ctx, "hold_enabled") == 1 {
			fmt.Fprintf(&b, "HoldRecommended count %d (counsel only when HoldEnabled; no auto-block). ", factInt(ctx, "hold_recommended_count"))
		}
	}
	b.WriteString("Settled is not bank credited; only bank_credited_proven counts as cash received. MATCHED is not cash.")
	return strings.TrimSpace(b.String())
}

func investigationAnswer(ctx FinanceContext) string {
	var b strings.Builder
	b.WriteString("Top unresolved exposure from structured exceptions. ")
	limit := 3
	if len(ctx.Exceptions) < limit {
		limit = len(ctx.Exceptions)
	}
	total := int64(0)
	for i := 0; i < limit; i++ {
		ex := ctx.Exceptions[i]
		impact := intField(ex, "variance_amount")
		total += impact
		fmt.Fprintf(&b, "%d. %s — %d. ", i+1, stringField(ex, "reason"), impact)
	}
	if total == 0 {
		total = factInt(ctx, "exposure_minor")
	}
	fmt.Fprintf(&b, "Total exposure shown is %d (copied). ", total)
	return strings.TrimSpace(b.String())
}

func knowledgeAnswer(ctx FinanceContext) string {
	if len(ctx.Knowledge) == 0 {
		return "I do not have enough internal finance documentation to answer that. Razorpay settled is not bank credited in our model."
	}
	var b strings.Builder
	b.WriteString(ctx.Knowledge[0].Text)
	b.WriteString(" This is internal model documentation, not a live transaction.")
	return strings.TrimSpace(b.String())
}

func fallbackTemplate(ctx FinanceContext) string {
	exp := factInt(ctx, "exposure_minor")
	st := factString(ctx, "provider_status")
	res := factString(ctx, "reconciliation_result")
	var b strings.Builder
	if st != "" {
		fmt.Fprintf(&b, "Razorpay status remains %s. ", st)
	}
	if res != "" {
		fmt.Fprintf(&b, "Reconciliation result is %s. ", res)
	}
	fmt.Fprintf(&b, "Exposure is %d (copied from structured fields). ", exp)
	if ctx.Plan.LossQuestion {
		fmt.Fprintf(&b, "This is unresolved exposure, not proven permanent loss. ")
	}
	return strings.TrimSpace(b.String())
}

func citeEvidence(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	var parts []string
	for _, id := range ids {
		parts = append(parts, "[Evidence: "+id+"]")
	}
	return " " + strings.Join(parts, " ")
}

func citeSources(hits []KnowledgeHit) string {
	if len(hits) == 0 {
		return ""
	}
	return " [Source: " + hits[0].Title + ", " + hits[0].Version + "]"
}

func confidence(ctx FinanceContext) float64 {
	if ctx.MissingPrimary {
		return 0.3
	}
	switch ctx.Plan.Intent {
	case IntentKnowledge:
		return 0.7
	case IntentAggregate, IntentReconciliation, IntentCashPosition:
		return 0.94
	case IntentExplanation:
		if factString(ctx, "reason") == "failed_with_bank_movement" {
			return 0.82
		}
		return 0.8
	default:
		return 0.88
	}
}
