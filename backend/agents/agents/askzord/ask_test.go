package askzord

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"zord-prompt-layer/tools"
)

func fixtureServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("tenant_id") == "tenant-b" {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "tenant_isolation"})
			return
		}
		switch {
		case strings.Contains(r.URL.Path, "/tax-breakdown/"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"payment_id": "pay_123", "gross_minor": 10000, "fee_minor": 272, "tax_minor": 0,
				"net_minor": 9728, "bank_credited_minor": 9728, "explained": true, "reason": "fee_explained", "currency": "INR",
			})
		case strings.Contains(r.URL.Path, "/cash-schedule"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"kind": "schedule_projection", "horizon_days": 7, "unknown_timing_minor": 0,
				"days": []any{
					map[string]any{"date": "2026-09-25", "expected_credit_minor": float64(1500), "expected_debit_minor": float64(200), "count": 2},
				},
			})
		case strings.Contains(r.URL.Path, "/marketplace/refund-graph-exceptions"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tenant_id": "tenant-a", "connector_id": "conn", "not_cash": true, "advisory": true,
				"signals": []any{
					map[string]any{
						"refund_id": "rf_1", "payment_id": "pay_rg", "seller_id": "sel_1",
						"amount_minor": float64(500), "reason": "refund_without_reverse_transfer",
					},
				},
			})
		case strings.Contains(r.URL.Path, "/marketplace/velocity-flags"):
			hold := r.URL.Query().Get("hold_enabled") == "true"
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tenant_id": "tenant-a", "connector_id": "conn", "not_cash": true, "advisory": true,
				"flags": []any{
					map[string]any{
						"seller_id": "sel_1", "refund_count": float64(5), "refund_sum_minor": float64(9000),
						"reason": "velocity_count", "ops_flag": true, "hold_recommended": hold,
						"auto_block_payout": false,
					},
				},
			})
		case strings.Contains(r.URL.Path, "/refunds"):
			_ = json.NewEncoder(w).Encode(map[string]any{"refunds": []any{}, "error": "not_found", "source": "provider_refund_observations"})
		case strings.Contains(r.URL.Path, "/reconciliation/summary"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"entity_counts":  map[string]any{"payment": 80, "payout": 20},
				"result_counts":  map[string]any{"MATCHED": 94, "AMBIGUOUS": 2, "UNRESOLVED": 3, "CONFLICTED": 1},
				"exposure_minor": 42500,
				"scored_count":   100,
				"matched_count":  94,
				"currency":       "INR",
				"exposure_by_reason": []any{
					map[string]any{"reason": "amount_mismatch", "count": 1, "exposure_minor": 25000},
					map[string]any{"reason": "failed_with_bank_movement", "count": 1, "exposure_minor": 10000},
					map[string]any{"reason": "captured_missing_settlement", "count": 1, "exposure_minor": 7500},
				},
			})
		case strings.Contains(r.URL.Path, "/exceptions"):
			_ = json.NewEncoder(w).Encode(map[string]any{"exceptions": []any{
				map[string]any{"entity_id": "pay_var", "reason": "amount_mismatch", "variance_amount": 25000},
				map[string]any{"entity_id": "pay_123", "reason": "failed_with_bank_movement", "variance_amount": 10000},
				map[string]any{"entity_id": "pay_cap", "reason": "captured_missing_settlement", "variance_amount": 7500},
			}})
		case strings.Contains(r.URL.Path, "/payments/") && strings.HasSuffix(r.URL.Path, "/evidence"):
			_ = json.NewEncoder(w).Encode(map[string]any{"evidence_ids": []any{"ev_1"}, "evidence_refs": map[string]any{"bank_observation_id": "bank_1"}})
		case strings.Contains(r.URL.Path, "/payouts/") && strings.HasSuffix(r.URL.Path, "/evidence"):
			_ = json.NewEncoder(w).Encode(map[string]any{"evidence_ids": []any{"ev_p"}, "evidence_refs": map[string]any{}})
		case strings.Contains(r.URL.Path, "/payouts/"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"payout_id": "pout_123", "status": "processed",
				"reconciliation": map[string]any{"result": "UNRESOLVED", "reason": "payout_missing_bank", "variance_amount": 25000},
			})
		case strings.Contains(r.URL.Path, "/payments/"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"payment_id": "pay_123", "status": "failed", "amount_minor": 10000,
				"reconciliation": map[string]any{"result": "UNRESOLVED", "reason": "failed_with_bank_movement", "variance_amount": 10000, "observed_amount": 10000},
				"evidence_refs":  map[string]any{"bank_observation_id": "bank_1"},
			})
		case strings.Contains(r.URL.Path, "/settlements"):
			_ = json.NewEncoder(w).Encode(map[string]any{"settlements": []any{}})
		case strings.Contains(r.URL.Path, "/bank-transactions"):
			_ = json.NewEncoder(w).Encode(map[string]any{"bank_transactions": []any{map[string]any{"id": "bank_1"}}})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{})
		}
	}))
}

func TestAskRecordAndExplanation(t *testing.T) {
	srv := fixtureServer(t)
	defer srv.Close()
	c := tools.NewOutcomeClient(srv.URL, "")
	resp := Ask(c, "tenant-a", "conn", "Why is pay_123 unresolved?", EntityRef{})
	if resp.Intent != IntentExplanation {
		t.Fatalf("intent=%s", resp.Intent)
	}
	if !strings.Contains(resp.Answer, "failed") || !strings.Contains(resp.Answer, "UNRESOLVED") {
		t.Fatal(resp.Answer)
	}
	if !strings.Contains(resp.Answer, "10000") {
		t.Fatal(resp.Answer)
	}
	if strings.Contains(resp.Answer, "STUCK") || strings.Contains(resp.Answer, "fully reconciled") {
		t.Fatal(resp.Answer)
	}
	if !containsLimitation(resp, "UNKNOWN") {
		t.Fatalf("%v", resp.Limitations)
	}
	if !containsStr(resp.Evidence, "ev_1") && !strings.Contains(resp.Answer, "ev_1") {
		t.Fatalf("evidence=%v answer=%s", resp.Evidence, resp.Answer)
	}
}

func TestAskPayoutRecord(t *testing.T) {
	srv := fixtureServer(t)
	defer srv.Close()
	c := tools.NewOutcomeClient(srv.URL, "")
	resp := Ask(c, "tenant-a", "conn", "What happened to payout pout_123?", EntityRef{})
	if resp.Intent != IntentRecord {
		t.Fatalf("%s", resp.Intent)
	}
	if !strings.Contains(resp.Answer, "processed") {
		t.Fatal(resp.Answer)
	}
}

func TestAskAggregateRateAndLoss(t *testing.T) {
	srv := fixtureServer(t)
	defer srv.Close()
	c := tools.NewOutcomeClient(srv.URL, "")
	resp := Ask(c, "tenant-a", "conn", "Why is the reconciliation rate only 94%?", EntityRef{})
	if resp.Intent != IntentReconciliation {
		t.Fatalf("%s", resp.Intent)
	}
	if !strings.Contains(resp.Answer, "94") || !strings.Contains(resp.Answer, "42500") {
		t.Fatal(resp.Answer)
	}
	loss := Ask(c, "tenant-a", "conn", "How much money did we lose from failed payments?", EntityRef{})
	low := strings.ToLower(loss.Answer)
	if strings.Contains(low, "we lost") {
		t.Fatal(loss.Answer)
	}
	if !strings.Contains(low, "exposure") || !strings.Contains(low, "does not establish") {
		t.Fatal(loss.Answer)
	}
}

func TestAskInvestigationBiggest(t *testing.T) {
	srv := fixtureServer(t)
	defer srv.Close()
	c := tools.NewOutcomeClient(srv.URL, "")
	resp := Ask(c, "tenant-a", "conn", "Show me the biggest unresolved issue.", EntityRef{})
	if resp.Intent != IntentInvestigation {
		t.Fatalf("%s", resp.Intent)
	}
	if !strings.Contains(resp.Answer, "amount_mismatch") || !strings.Contains(resp.Answer, "25000") {
		t.Fatal(resp.Answer)
	}
}

func TestAskKnowledgeDoesNotInventPayment(t *testing.T) {
	srv := fixtureServer(t)
	defer srv.Close()
	c := tools.NewOutcomeClient(srv.URL, "")
	resp := Ask(c, "tenant-a", "conn", "What is the difference between settlement and bank credit?", EntityRef{})
	if resp.Intent != IntentKnowledge {
		t.Fatalf("%s", resp.Intent)
	}
	if strings.Contains(resp.Answer, "pay_123") {
		t.Fatal(resp.Answer)
	}
	if !strings.Contains(strings.ToLower(resp.Answer), "settled") {
		t.Fatal(resp.Answer)
	}
	if len(resp.Sources) == 0 {
		t.Fatal("expected knowledge source")
	}
}

func TestAskBankCauseUnknown(t *testing.T) {
	srv := fixtureServer(t)
	defer srv.Close()
	c := tools.NewOutcomeClient(srv.URL, "")
	resp := Ask(c, "tenant-a", "conn", "Which bank caused the failures?", EntityRef{})
	if !strings.Contains(resp.Answer, "UNKNOWN") {
		t.Fatal(resp.Answer)
	}
}

func TestAskSettledAllNotAllCredited(t *testing.T) {
	srv := fixtureServer(t)
	defer srv.Close()
	c := tools.NewOutcomeClient(srv.URL, "")
	resp := Ask(c, "tenant-a", "conn", "Was every settled payment credited to the bank?", EntityRef{})
	if !strings.Contains(resp.Answer, "No.") {
		t.Fatal(resp.Answer)
	}
}

func TestAskTenantIsolation(t *testing.T) {
	srv := fixtureServer(t)
	defer srv.Close()
	c := tools.NewOutcomeClient(srv.URL, "")
	resp := Ask(c, "tenant-b", "conn", "Why is pay_123 unresolved?", EntityRef{})
	if containsLimitation(resp, "UNKNOWN") || strings.Contains(strings.ToLower(resp.Answer), "do not invent") {
		return
	}
	if strings.Contains(resp.Answer, "failed") && strings.Contains(resp.Answer, "10000") && resp.Intent == IntentExplanation {
		t.Fatal("tenant-b must not receive tenant-a payment facts")
	}
}

func TestAskFollowupRequeriesRefund(t *testing.T) {
	srv := fixtureServer(t)
	defer srv.Close()
	c := tools.NewOutcomeClient(srv.URL, "")
	resp := Ask(c, "tenant-a", "conn", "And what about the refund?", EntityRef{Type: "payment", ID: "pay_123"})
	if resp.Intent != IntentRecord {
		t.Fatalf("%s", resp.Intent)
	}
	if !containsLimitation(resp, "refund") && !strings.Contains(strings.ToLower(resp.Answer), "refund") {
		t.Fatal(resp.Answer)
	}
}

func TestAskTaxBreakdown(t *testing.T) {
	srv := fixtureServer(t)
	defer srv.Close()
	c := tools.NewOutcomeClient(srv.URL, "")
	resp := Ask(c, "tenant-a", "conn", "Why is net 9728 not 10000 for pay_123?", EntityRef{})
	if !strings.Contains(resp.Answer, "272") || !strings.Contains(resp.Answer, "9728") {
		t.Fatal(resp.Answer)
	}
	if strings.Contains(strings.ToLower(resp.Answer), "we lost") {
		t.Fatal(resp.Answer)
	}
}

func TestAskMorningBriefingHumanNextStepRefundGraph(t *testing.T) {
	srv := fixtureServer(t)
	defer srv.Close()
	c := tools.NewOutcomeClient(srv.URL, "")
	resp := Ask(c, "tenant-a", "conn", "What is my morning briefing?", EntityRef{})
	if resp.Intent != IntentAggregate {
		t.Fatalf("intent=%s", resp.Intent)
	}
	if !strings.Contains(resp.HumanNextStep, "refund_without_reverse_transfer") {
		t.Fatalf("next=%s", resp.HumanNextStep)
	}
	if !hasFact(resp, "expected_credit_minor", "1500") {
		t.Fatalf("facts=%+v", resp.Facts)
	}
	if !hasFact(resp, "refund_without_reverse_count", "1") {
		t.Fatalf("facts=%+v", resp.Facts)
	}
	if strings.Contains(strings.ToLower(resp.Answer), "we lost") {
		t.Fatal(resp.Answer)
	}
}

func TestAskOpsDoesNotInventHoldWhenDisabled(t *testing.T) {
	srv := fixtureServer(t)
	defer srv.Close()
	c := tools.NewOutcomeClient(srv.URL, "")
	resp := Ask(c, "tenant-a", "conn", "Show me the biggest unresolved issue.", EntityRef{})
	if resp.Intent != IntentInvestigation {
		t.Fatalf("%s", resp.Intent)
	}
	if hasFact(resp, "hold_recommended_count", "1") {
		t.Fatalf("HoldRecommended must stay 0 when hold_enabled false: %+v", resp.Facts)
	}
	if !hasFact(resp, "hold_recommended_count", "0") && !hasFact(resp, "hold_enabled", "0") {
		// hold_recommended_count should be present as 0
		if !hasFact(resp, "hold_recommended_count", "0") {
			t.Fatalf("facts=%+v", resp.Facts)
		}
	}
	// refund graph still wins over exceptions for next step
	if !strings.Contains(resp.HumanNextStep, "refund_without_reverse_transfer") {
		t.Fatalf("next=%s", resp.HumanNextStep)
	}
}

func TestAskCashScheduleCopiesDayMinors(t *testing.T) {
	srv := fixtureServer(t)
	defer srv.Close()
	c := tools.NewOutcomeClient(srv.URL, "")
	resp := Ask(c, "tenant-a", "conn", "What is the cash schedule for expected bank credits?", EntityRef{})
	if resp.Intent != IntentCashPosition {
		t.Fatalf("%s", resp.Intent)
	}
	if !strings.Contains(resp.Answer, "1500") || !strings.Contains(resp.Answer, "200") {
		t.Fatal(resp.Answer)
	}
	if !strings.Contains(strings.ToLower(resp.Answer), "not bank credited") && !strings.Contains(resp.Answer, "schedule_projection") {
		// cash answer must not treat projection as credited
		if !strings.Contains(resp.Answer, "schedule_projection") && !strings.Contains(strings.ToLower(resp.Answer), "not a statistical") {
			t.Fatal(resp.Answer)
		}
	}
}

func TestGoldenEval(t *testing.T) {

	srv := fixtureServer(t)
	defer srv.Close()
	c := tools.NewOutcomeClient(srv.URL, "")
	files, err := filepath.Glob("testdata/golden/*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 10 {
		t.Fatalf("expected golden files, got %d", len(files))
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var g struct {
			Question            string   `json:"question"`
			ExpectedIntent      string   `json:"expected_intent"`
			RequiredFacts       []string `json:"required_facts"`
			ForbiddenClaims     []string `json:"forbidden_claims"`
			RequiredLimitations []string `json:"required_limitations"`
		}
		if err := json.Unmarshal(raw, &g); err != nil {
			t.Fatal(f, err)
		}
		resp := Ask(c, "tenant-a", "conn", g.Question, EntityRef{})
		if resp.Intent != g.ExpectedIntent {
			t.Fatalf("%s intent=%s want %s", f, resp.Intent, g.ExpectedIntent)
		}
		for _, fact := range g.RequiredFacts {
			k, v, _ := strings.Cut(fact, "=")
			if !hasFact(resp, k, v) && !strings.Contains(resp.Answer, v) {
				t.Fatalf("%s missing %s in %+v / %s", f, fact, resp.Facts, resp.Answer)
			}
		}
		low := strings.ToLower(resp.Answer)
		for _, bad := range g.ForbiddenClaims {
			needle := strings.ToLower(bad)
			if strings.Contains(low, needle) && !strings.Contains(low, "not "+needle) {
				t.Fatalf("%s forbidden %q in %s", f, bad, resp.Answer)
			}
		}
		for _, lim := range g.RequiredLimitations {
			if !containsLimitation(resp, lim) && !strings.Contains(resp.Answer, lim) {
				t.Fatalf("%s missing limitation %s: %v %s", f, lim, resp.Limitations, resp.Answer)
			}
		}
	}
}

func hasFact(resp Response, field, value string) bool {
	for _, f := range resp.Facts {
		if f.Field == field && strings.Contains(strings.ToLower(stringify(f.Value)), strings.ToLower(value)) {
			return true
		}
	}
	return false
}

func stringify(v any) string {
	b, _ := json.Marshal(v)
	return strings.Trim(string(b), `"`)
}

func containsLimitation(resp Response, sub string) bool {
	for _, l := range resp.Limitations {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

func containsStr(in []string, v string) bool {
	for _, x := range in {
		if x == v {
			return true
		}
	}
	return false
}
