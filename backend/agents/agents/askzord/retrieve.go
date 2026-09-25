package askzord

import (
	"sort"
	"strconv"
	"strings"

	"zord-prompt-layer/agents/briefing"
	"zord-prompt-layer/tools"
)

func Retrieve(c *tools.OutcomeClient, tenantID, connectorID string, plan QueryPlan) FinanceContext {
	ctx := FinanceContext{Plan: plan}
	need := func(name string) bool {
		for _, s := range plan.RequiredSources {
			if s == name {
				return true
			}
		}
		return false
	}

	if plan.Intent == IntentKnowledge {
		ctx.Knowledge = SearchKnowledge(plan.Filters["question"])
		if len(ctx.Knowledge) == 0 {
			ctx.Knowledge = SearchKnowledge(strings.Join(plan.RequiredSources, " "))
		}
		return ctx
	}

	if need("summary") || plan.Intent == IntentAggregate || plan.Intent == IntentCashPosition ||
		plan.Intent == IntentReconciliation || plan.Intent == IntentInvestigation {
		sum, _ := c.GetReconSummary(tenantID, connectorID)
		if tools.FinanceEvidenceNone(sum) || sum["error"] == "not_found" {
			exs, _ := c.GetException(tenantID, connectorID, "")
			sum = SummaryFromExceptions(exs)
		}
		ctx.Summary = sum
		applySummaryFacts(&ctx, sum)
	}

	if plan.Intent == IntentCashPosition {
		cash, _ := c.GetCashPosition(tenantID, connectorID)
		for _, key := range []string{
			"gross_captured_minor", "settlement_expected_net_minor", "bank_credited_proven_minor",
			"in_flight_minor", "unresolved_exposure_minor",
		} {
			if v, ok := cash[key]; ok {
				if key == "settlement_expected_net_minor" {
					addLabeledFact(&ctx, key, v, "INR", "settlement expected net "+briefing.ExpectedCashLabel)
					continue
				}
				addFact(&ctx, key, v, "INR")
			}
		}
	}

	needOps := plan.Intent == IntentCashPosition || plan.Intent == IntentAggregate ||
		plan.Intent == IntentInvestigation || plan.Intent == IntentReconciliation ||
		isMorningOrOpsQuestion(plan.Filters["question"])
	if needOps {
		pullOpsCounselSurfaces(c, tenantID, connectorID, &ctx, plan)
	}

	if need("exception") || plan.Intent == IntentInvestigation || plan.Intent == IntentAggregate ||
		plan.Intent == IntentReconciliation || plan.Intent == IntentCashPosition {
		exs, _ := c.GetException(tenantID, connectorID, "")
		ctx.Exceptions = exceptionMaps(exs)
		sort.Slice(ctx.Exceptions, func(i, j int) bool {
			return intField(ctx.Exceptions[i], "variance_amount") > intField(ctx.Exceptions[j], "variance_amount")
		})
		for i, ex := range ctx.Exceptions {
			if v := intField(ex, "variance_amount"); v != 0 {
				addFact(&ctx, "exception_exposure_"+stringField(ex, "reason")+"_"+strconv.Itoa(i), v, "INR")
			}
		}
		if ctx.Summary == nil {
			ctx.Summary = SummaryFromExceptions(exs)
			applySummaryFacts(&ctx, ctx.Summary)
		}
	}

	if plan.Entity.ID == "" {
		if plan.Intent == IntentKnowledge {
			ctx.Knowledge = SearchKnowledge(plan.Entity.ID)
		}
		attachKnowledgeFootnote(&ctx)
		return ctx
	}

	entityType := plan.Entity.Type
	if entityType == "" {
		entityType = "payment"
	}
	var primary map[string]any
	if entityType == "payout" {
		primary, _ = c.GetPayout(tenantID, connectorID, plan.Entity.ID)
	} else {
		primary, _ = c.GetPayment(tenantID, connectorID, plan.Entity.ID)
	}
	ctx.Primary = primary
	if noneRecord(primary) {
		ctx.MissingPrimary = true
		ctx.Limitations = append(ctx.Limitations, "UNKNOWN: no "+entityType+" record was returned. Do not invent one.")
		return ctx
	}

	status := stringField(primary, "status")
	addFact(&ctx, "provider_status", status, "")
	if rec, ok := primary["reconciliation"].(map[string]any); ok {
		addFact(&ctx, "reconciliation_result", stringField(rec, "result"), "")
		addFact(&ctx, "reason", stringField(rec, "reason"), "")
		addFact(&ctx, "exposure_minor", intField(rec, "variance_amount"), "INR")
		addFact(&ctx, "expected_amount", intField(rec, "expected_amount"), "INR")
		addFact(&ctx, "observed_amount", intField(rec, "observed_amount"), "INR")
		if v := intField(rec, "variance_amount"); true {
			ctx.Calculations = append(ctx.Calculations, Calculation{Formula: "structured_variance_amount", Output: v})
		}
	}
	if amt := intField(primary, "amount_minor"); amt != 0 {
		addFact(&ctx, "amount_minor", amt, "INR")
	}

	if need("settlement") && entityType == "payment" {
		setl, _ := c.GetSettlement(tenantID, connectorID, plan.Entity.ID)
		if tools.HasRecords(setl, "settlements") {
			addFact(&ctx, "settlement", "found", "")
		} else {
			addFact(&ctx, "settlement", nil, "")
			ctx.Limitations = append(ctx.Limitations, "No settlement line was returned for this payment.")
		}
	}
	if need("refund") && entityType == "payment" {
		ref, _ := c.GetRefund(tenantID, connectorID, plan.Entity.ID)
		if tools.HasRecords(ref, "refunds", "settlements") {
			addFact(&ctx, "refund", "found", "")
		} else {
			addFact(&ctx, "refund", nil, "")
			ctx.Limitations = append(ctx.Limitations, "No refund line was returned.")
		}
	}
	if need("bank") {
		banks, _ := c.SearchBankTransactions(tenantID, connectorID, "", "")
		if tools.HasRecords(banks, "bank_transactions") {
			addFact(&ctx, "bank_movement", "found", "")
			if rec, ok := primary["reconciliation"].(map[string]any); ok {
				if obs := intField(rec, "observed_amount"); obs != 0 {
					addFact(&ctx, "bank_movement_minor", obs, "INR")
				}
			}
		} else {
			addFact(&ctx, "bank_movement", nil, "")
			ctx.Limitations = append(ctx.Limitations, "No bank transaction was returned.")
		}
	}
	if refs, ok := primary["evidence_refs"].(map[string]any); ok {
		if stringField(refs, "bank_observation_id") != "" {
			ctx.BankProven = true
		}
	}

	if need("ledger") {
		led, _ := c.GetLedgerEntry(tenantID, connectorID, plan.Entity.ID)
		if tools.LedgerEmpty(led) {
			ctx.Limitations = append(ctx.Limitations, "No derived ledger lines were returned. Do not invent a ledger entry.")
		} else if lines, ok := led["lines"].([]any); ok {
			addFact(&ctx, "ledger_line_count", len(lines), "")
		}
	}

	if need("tax") && entityType == "payment" {
		tb, _ := c.GetTaxBreakdown(tenantID, connectorID, plan.Entity.ID)
		if tb["error"] != "not_found" && tb["error"] != "none" {
			for _, key := range []string{"gross_minor", "fee_minor", "tax_minor", "net_minor", "bank_credited_minor"} {
				if v, ok := tb[key]; ok {
					addFact(&ctx, key, v, "INR")
				}
			}
			if explained, _ := tb["explained"].(bool); explained {
				addFact(&ctx, "tax_explained", true, "")
			}
			if r := stringField(tb, "reason"); r != "" {
				addFact(&ctx, "tax_reason", r, "")
			}
		}
	}

	if need("evidence") || plan.Intent == IntentExplanation || plan.Intent == IntentRecord {
		var ev map[string]any
		if entityType == "payout" {
			ev, _ = c.GetPayoutEvidence(tenantID, connectorID, plan.Entity.ID)
		} else {
			ev, _ = c.GetEvidence(tenantID, connectorID, plan.Entity.ID)
		}
		for _, id := range stringSlice(ev, "evidence_ids") {
			ctx.Evidence = appendUnique(ctx.Evidence, id)
		}
		if refs, ok := ev["evidence_refs"].(map[string]any); ok {
			for _, k := range []string{"bank_observation_id", "settlement_line_id", "canonical_payment_id"} {
				if id := stringField(refs, k); id != "" {
					ctx.Evidence = appendUnique(ctx.Evidence, id)
				}
			}
		}
		fin, _ := c.ListFinanceEvidence(tenantID, entityType, plan.Entity.ID)
		for _, id := range tools.CollectFinanceEvidenceIDs(fin) {
			ctx.Evidence = appendUnique(ctx.Evidence, id)
		}
		if tools.FinanceEvidenceNone(fin) {
			ctx.Limitations = append(ctx.Limitations, "No finance evidence pack was returned. Do not invent evidence IDs.")
		}
		calcs, _ := c.GetCalculationTrace(tenantID, entityType, plan.Entity.ID)
		if v, ok := tools.StructuredCalcVariance(calcs); ok {
			ctx.Calculations = []Calculation{{Formula: "structured_variance_amount", Output: v}}
			setFact(&ctx, "exposure_minor", v, "INR")
		}
		if len(ctx.Evidence) > 0 && strings.HasPrefix(ctx.Evidence[0], "ev_") {
			ver, _ := c.VerifyEvidence(tenantID, ctx.Evidence[0])
			ctx.Integrity = stringField(ver, "integrity")
			if ctx.Integrity == "INVALID" {
				ctx.Limitations = append(ctx.Limitations, "Evidence integrity is INVALID. Do not treat the snapshot as authoritative.")
			}
		}
	}

	reason := factString(ctx, "reason")
	if reason == "failed_with_bank_movement" || reason == "payout_failed_with_bank_movement" {
		ctx.Limitations = append(ctx.Limitations, "Root cause remains UNKNOWN. Bank movement is proven; why it happened is not.")
	}
	if st := FinancialState(factString(ctx, "provider_status"), factString(ctx, "reconciliation_result"), reason, ctx.BankProven); st != "" {
		addFact(&ctx, "financial_state", st, "")
	}
	attachKnowledgeFootnote(&ctx)
	return ctx
}

func attachKnowledgeFootnote(ctx *FinanceContext) {
	if ctx.Plan.Intent == IntentKnowledge {
		return
	}
	res := factString(*ctx, "reconciliation_result")
	status := factString(*ctx, "provider_status")
	if status == "settled" || res == "MATCHED" {
		hits := SearchKnowledge("settlement bank credit matched fully reconciled")
		if len(hits) > 0 {
			ctx.Knowledge = append(ctx.Knowledge, hits[0])
		}
	}
}

func applySummaryFacts(ctx *FinanceContext, sum map[string]any) {
	if sum == nil {
		return
	}
	if v, ok := intish(sum["exposure_minor"]); ok {
		addFact(ctx, "exposure_minor", v, "INR")
	}
	if v, ok := intish(sum["scored_count"]); ok {
		addFact(ctx, "scored_count", v, "")
	}
	if v, ok := intish(sum["matched_count"]); ok {
		addFact(ctx, "matched_count", v, "")
	}
	if rc, ok := sum["result_counts"].(map[string]any); ok {
		for k, raw := range rc {
			if n, ok := intish(raw); ok {
				addFact(ctx, "count_"+strings.ToLower(k), n, "")
			}
		}
	}
}

func addFact(ctx *FinanceContext, field string, value any, currency string) {
	ctx.Facts = append(ctx.Facts, Fact{Field: field, Value: value, Currency: currency})
}

// addLabeledFact attaches a user-facing label (used for expected-cash projection wording).
func addLabeledFact(ctx *FinanceContext, field string, value any, currency, label string) {
	ctx.Facts = append(ctx.Facts, Fact{Field: field, Value: value, Currency: currency, Label: label})
}

func setFact(ctx *FinanceContext, field string, value any, currency string) {
	for i := range ctx.Facts {
		if ctx.Facts[i].Field == field {
			ctx.Facts[i].Value = value
			ctx.Facts[i].Currency = currency
			return
		}
	}
	addFact(ctx, field, value, currency)
}

func factString(ctx FinanceContext, field string) string {
	for _, f := range ctx.Facts {
		if f.Field == field {
			if s, ok := f.Value.(string); ok {
				return s
			}
		}
	}
	return ""
}

func noneRecord(body map[string]any) bool {
	if body == nil {
		return true
	}
	if err, _ := body["error"].(string); err == "not_found" || err == "source_not_in_this_phase" || err == "none" {
		return true
	}
	return false
}

func isMorningOrOpsQuestion(q string) bool {
	s := strings.ToLower(q)
	for _, p := range []string{"morning", "briefing", "ops counsel", "next step", "what should i do"} {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

// pullOpsCounselSurfaces copies numeric fields from cash schedule / refund-graph /
// velocity-flags only when present. HoldEnabled defaults false (no HoldRecommended).
func pullOpsCounselSurfaces(c *tools.OutcomeClient, tenantID, connectorID string, ctx *FinanceContext, plan QueryPlan) {
	holdEnabled := false
	if plan.Filters != nil {
		v := strings.ToLower(strings.TrimSpace(plan.Filters["hold_enabled"]))
		holdEnabled = v == "true" || v == "1"
	}

	sch, _ := c.GetCashSchedule(tenantID, connectorID)
	applyCashScheduleFacts(ctx, sch)

	rg, _ := c.GetMarketplaceRefundGraphExceptions(tenantID, connectorID)
	applyRefundGraphFacts(ctx, rg)

	vf, _ := c.GetMarketplaceVelocityFlags(tenantID, connectorID, holdEnabled)
	applyVelocityFacts(ctx, vf, holdEnabled)
}

func applyCashScheduleFacts(ctx *FinanceContext, sch map[string]any) {
	if noneRecord(sch) {
		ctx.Limitations = append(ctx.Limitations, "Cash schedule surface unavailable; "+briefing.ExpectedCashLabel+" schedule_projection clock omitted.")
		return
	}
	if kind, _ := sch["kind"].(string); kind != "" {
		addLabeledFact(ctx, "cash_schedule_kind", kind, "", kind+" ("+briefing.ExpectedCashLabel+")")
	}
	if v, ok := sch["unknown_timing_minor"]; ok {
		addLabeledFact(ctx, "unknown_timing_minor", v, "INR", "expected cash, timing unknown (projected, not yet banked)")
	}
	days, _ := sch["days"].([]any)
	if len(days) == 0 {
		return
	}
	day, ok := days[0].(map[string]any)
	if !ok {
		return
	}
	if v, ok := day["expected_credit_minor"]; ok {
		addLabeledFact(ctx, "expected_credit_minor", v, "INR", "next banking-day "+briefing.ExpectedCashLabel+" credit")
	}
	if v, ok := day["expected_debit_minor"]; ok {
		addLabeledFact(ctx, "expected_debit_minor", v, "INR", "next banking-day "+briefing.ExpectedCashLabel+" debit")
	}
}

func applyRefundGraphFacts(ctx *FinanceContext, body map[string]any) {
	if noneRecord(body) {
		ctx.Limitations = append(ctx.Limitations, "Refund-graph exceptions surface unavailable.")
		return
	}
	signals, _ := body["signals"].([]any)
	addFact(ctx, "refund_graph_signal_count", len(signals), "")
	var withoutReverse int
	for _, raw := range signals {
		sig, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		reason, _ := sig["reason"].(string)
		if reason == "refund_without_reverse_transfer" {
			withoutReverse++
		}
		if v, ok := sig["amount_minor"]; ok {
			// only copy present variance/amount fields; do not invent totals
			_ = v
		}
	}
	if withoutReverse > 0 {
		addFact(ctx, "refund_without_reverse_count", withoutReverse, "")
	}
}

func applyVelocityFacts(ctx *FinanceContext, body map[string]any, holdEnabled bool) {
	if noneRecord(body) {
		ctx.Limitations = append(ctx.Limitations, "Velocity flags surface unavailable.")
		return
	}
	flags, _ := body["flags"].([]any)
	addFact(ctx, "velocity_flag_count", len(flags), "")
	if holdEnabled {
		addFact(ctx, "hold_enabled", 1, "")
	} else {
		addFact(ctx, "hold_enabled", 0, "")
	}
	holdRec := 0
	if holdEnabled {
		for _, raw := range flags {
			f, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if hr, _ := f["hold_recommended"].(bool); hr {
				holdRec++
			}
			if v, ok := f["refund_sum_minor"]; ok {
				_ = v // present but not summed — avoid inventing aggregates
			}
		}
	}
	addFact(ctx, "hold_recommended_count", holdRec, "")
}
