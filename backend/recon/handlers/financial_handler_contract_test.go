package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"zord-outcome-engine/internal/auth"
	"zord-outcome-engine/internal/recon"

	"github.com/gin-gonic/gin"
)

func financeRouter(t *testing.T) (*gin.Engine, *recon.MemoryFinancialStore) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	store := recon.NewMemoryFinancialStore()
	store.Payments = []recon.PaymentFact{{
		PaymentID: "pay_1", CanonicalStatus: recon.PaymentCaptured, ProviderStatus: "captured",
		Captured: true, AmountMinor: 10000, Currency: "INR", Sources: []string{"webhook"},
	}}
	store.Events["pay_1"] = []recon.ObservationFact{{
		Source: "webhook", ProviderStatus: "authorized", CanonicalStatus: "authorized", SourceEventID: "evt_1",
	}, {
		Source: "api", ProviderStatus: "captured", CanonicalStatus: "captured", SourceEventID: "evt_2",
	}}
	store.Lines = []recon.SettlementLine{{
		ID: "sl1", PaymentID: "pay_1", LineType: "payment", AmountMinor: 10000, CreditMinor: 9728, FeeMinor: 272, Currency: "INR",
	}}
	store.Results = []recon.FinancialResult{{
		EntityType: recon.EntityPayment, EntityID: "pay_1", Result: recon.ResultMatched,
		Reason: "captured_settlement_exact_bank", ExpectedAmount: 10000, ObservedAmount: 9728, BankCreditProven: true,
	}}
	svc := recon.NewFinancialService(store)
	h := &FinancialHandler{Service: svc, Store: store}
	r := gin.New()
	r.GET("/v1/reconciliation/payments/:payment_id", h.GetPayment)
	r.GET("/v1/reconciliation/summary", h.GetFinanceSummary)
	r.GET("/v1/reconciliation/cash-position", h.GetCashPosition)
	r.GET("/v1/reconciliation/cash-schedule", h.GetCashSchedule)
	r.GET("/v1/reconciliation/tax-breakdown/:payment_id", h.GetTaxBreakdown)
	r.GET("/v1/reconciliation/ledger", h.GetLedger)
	r.GET("/v1/reconciliation/refunds", h.ListRefunds)
	r.GET("/v1/reconciliation/sla-policy", h.SLAPolicy)
	r.POST("/v1/reconciliation/run", h.Run)
	r.POST("/v1/reconciliation/batches/:batch_id/reconcile", h.ReconcileBatch)
	r.GET("/v1/reconciliation/batches/:batch_id", h.GetBatch)
	r.GET("/v1/reconciliation/payouts/:payout_id/timeline", h.GetPayoutTimeline)
	r.GET("/v1/reconciliation/payments/:payment_id/timeline", h.GetPaymentTimeline)
	r.GET("/v1/reconciliation/results", h.ListResults)
	r.GET("/v1/reconciliation/evaluation", h.GetEvaluation)
	r.GET("/v1/reconciliation/investigations", h.ListInvestigations)
	r.GET("/v1/reconciliation/payouts/:payout_id", h.GetPayout)
	r.GET("/v1/cash/instruments", h.ListInstruments)
	return r, store
}

func getJSON(t *testing.T, r *gin.Engine, path string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	ct := w.Header().Get("Content-Type")
	if w.Code == 200 && ct != "" && ct[:16] != "application/json" {
		t.Fatalf("content-type=%s", ct)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v body=%s", err, w.Body.String())
	}
	return w.Code, body
}

func TestFinancePaymentJSONIncludesObservations(t *testing.T) {
	r, _ := financeRouter(t)
	code, body := getJSON(t, r, "/v1/reconciliation/payments/pay_1?tenant_id=t&connector_id=c")
	if code != 200 {
		t.Fatalf("code=%d body=%v", code, body)
	}
	if body["status"] != "captured" || body["provider_status"] != "captured" {
		t.Fatalf("%v", body)
	}
	obs, ok := body["observations"].([]any)
	if !ok || len(obs) != 2 {
		t.Fatalf("observations=%v", body["observations"])
	}
	reconObj, _ := body["reconciliation"].(map[string]any)
	if reconObj["bank_credit_proven"] != true {
		t.Fatalf("recon=%v", reconObj)
	}
	two, _ := reconObj["two_way"].(map[string]any)
	three, _ := reconObj["three_way"].(map[string]any)
	if two["result"] != "MATCHED" || three["result"] != "MATCHED" {
		t.Fatalf("legs two=%v three=%v", two, three)
	}
	if _, ok := body["fully_reconciled"]; ok {
		t.Fatal("must not emit fully_reconciled")
	}
}

func TestCashInstrumentsCatalog(t *testing.T) {
	r, _ := financeRouter(t)
	code, body := getJSON(t, r, "/v1/cash/instruments")
	if code != 200 {
		t.Fatalf("code=%d body=%v", code, body)
	}
	inst, ok := body["instruments"].([]any)
	if !ok || len(inst) < 4 {
		t.Fatalf("instruments=%v", body["instruments"])
	}
	psps, _ := body["psps"].([]any)
	if len(psps) < 2 {
		t.Fatalf("psps=%v", body["psps"])
	}
}

func TestFinanceSummaryJSON(t *testing.T) {
	r, _ := financeRouter(t)
	code, body := getJSON(t, r, "/v1/reconciliation/summary?tenant_id=t&connector_id=c")
	if code != 200 {
		t.Fatalf("code=%d", code)
	}
	for _, k := range []string{"scored_count", "matched_count", "exposure_minor", "currency", "result_counts"} {
		if _, ok := body[k]; !ok {
			t.Fatalf("missing %s in %v", k, body)
		}
	}
	if body["currency"] != "INR" {
		t.Fatalf("currency=%v", body["currency"])
	}
}

func TestCashPositionJSON(t *testing.T) {
	r, _ := financeRouter(t)
	code, body := getJSON(t, r, "/v1/reconciliation/cash-position?tenant_id=t&connector_id=c")
	if code != 200 {
		t.Fatalf("code=%d %v", code, body)
	}
	for _, k := range []string{"gross_captured_minor", "settlement_expected_net_minor", "bank_credited_proven_minor", "in_flight_minor", "unresolved_exposure_minor", "currency"} {
		if _, ok := body[k]; !ok {
			t.Fatalf("missing %s", k)
		}
	}
}

func TestTaxBreakdownAndLedgerJSON(t *testing.T) {
	r, _ := financeRouter(t)
	code, body := getJSON(t, r, "/v1/reconciliation/tax-breakdown/pay_1?tenant_id=t&connector_id=c")
	if code != 200 {
		t.Fatalf("code=%d %v", code, body)
	}
	if body["fee_minor"] != float64(272) || body["explained"] != true {
		t.Fatalf("%v", body)
	}
	code, body = getJSON(t, r, "/v1/reconciliation/ledger?tenant_id=t&connector_id=c&entity_id=pay_1")
	if code != 200 {
		t.Fatalf("ledger code=%d %v", code, body)
	}
	if _, ok := body["lines"]; !ok {
		t.Fatalf("ledger=%v", body)
	}
}

func TestRefundsJSONEmpty(t *testing.T) {
	r, _ := financeRouter(t)
	code, body := getJSON(t, r, "/v1/reconciliation/refunds?tenant_id=t&connector_id=c&payment_id=pay_1")
	if code != 200 {
		t.Fatalf("code=%d", code)
	}
	if body["source"] != "provider_refund_observations" {
		t.Fatalf("%v", body)
	}
	if body["error"] != "not_found" {
		t.Fatalf("empty refunds must be not_found: %v", body)
	}
}

func TestSLAPolicyJSON(t *testing.T) {
	r, _ := financeRouter(t)
	code, body := getJSON(t, r, "/v1/reconciliation/sla-policy?tenant_id=t&connector_id=c")
	if code != 200 {
		t.Fatalf("code=%d", code)
	}
	if _, ok := body["policies"]; !ok {
		t.Fatalf("%v", body)
	}
}

func TestCashScheduleKind(t *testing.T) {
	r, _ := financeRouter(t)
	code, body := getJSON(t, r, "/v1/reconciliation/cash-schedule?tenant_id=t&connector_id=c")
	if code != 200 {
		t.Fatalf("code=%d %v", code, body)
	}
	if body["kind"] != "schedule_projection" {
		t.Fatalf("%v", body)
	}
	if _, ok := body["days"].([]any); !ok {
		t.Fatalf("days=%v", body["days"])
	}
	if _, ok := body["unknown_timing_minor"]; !ok {
		t.Fatalf("%v", body)
	}
}

func TestLedgerRequiresEntityID(t *testing.T) {
	r, _ := financeRouter(t)
	code, body := getJSON(t, r, "/v1/reconciliation/ledger?tenant_id=t&connector_id=c")
	if code != 400 {
		t.Fatalf("code=%d %v", code, body)
	}
}

func TestFinanceRunJSON(t *testing.T) {
	r, _ := financeRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/reconciliation/run", strings.NewReader(`{"tenant_id":"t","connector_id":"c"}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.WithPrincipalForTest(req.Context(), "t"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"run_id", "matched_count", "exception_count"} {
		if _, ok := body[k]; !ok {
			t.Fatalf("missing %s in %v", k, body)
		}
	}
}

func TestBatchReconcileScopesPayouts(t *testing.T) {
	r, store := financeRouter(t)
	store.Payouts = []recon.PayoutFact{{
		PayoutID: "pout_1", ProviderStatus: "failed", AmountMinor: 1, Currency: "INR",
	}}
	req := httptest.NewRequest(http.MethodPost, "/v1/reconciliation/batches/BATCH-001/reconcile?tenant_id=t&connector_id=c",
		strings.NewReader(`{"tenant_id":"t","connector_id":"c","payout_ids":["pout_1"]}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.WithPrincipalForTest(req.Context(), "t"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["batch_id"] != "BATCH-001" {
		t.Fatalf("%v", body)
	}
	code, got := getJSON(t, r, "/v1/reconciliation/batches/BATCH-001?tenant_id=t&connector_id=c")
	if code != 200 {
		t.Fatalf("get code=%d %v", code, got)
	}
	if got["batch_id"] != "BATCH-001" {
		t.Fatalf("%v", got)
	}
}

func TestPayoutTimelineUsesCapturedWebhookTimes(t *testing.T) {
	r, store := financeRouter(t)
	captured := time.Date(2026, 4, 1, 10, 0, 12, 0, time.UTC)
	store.Payouts = []recon.PayoutFact{{
		PayoutID: "pout_1", ProviderStatus: "processed", AmountMinor: 10000, Currency: "INR", UTR: "HDFC123",
	}}
	store.PayoutEvents = map[string][]recon.ObservationFact{
		"pout_1": {{
			Source: "webhook", ProviderStatus: "processed", SourceEventID: "evt_payout",
			RawReference: "HDFC123", ObservedAt: captured,
		}},
	}
	code, body := getJSON(t, r, "/v1/reconciliation/payouts/pout_1/timeline?tenant_id=t&connector_id=c")
	if code != 200 {
		t.Fatalf("code=%d body=%v", code, body)
	}
	if body["entity_type"] != "payout" || body["entity_id"] != "pout_1" {
		t.Fatalf("%v", body)
	}
	if body["reconciliation"] != nil {
		t.Fatalf("reconciliation should be null before a run: %v", body["reconciliation"])
	}
	steps, _ := body["steps"].([]any)
	if len(steps) != 14 {
		t.Fatalf("steps=%d", len(steps))
	}
	created, _ := steps[1].(map[string]any)
	if created["captured"] != true {
		t.Fatalf("created=%v", created)
	}
	if got := created["captured_at"]; got != captured.UTC().Format(time.RFC3339) && got != captured.Format(time.RFC3339Nano) {
		// encoding/json uses RFC3339Nano for times with zero nanos as RFC3339
		if !strings.HasPrefix(strings.TrimSpace(stringify(got)), "2026-04-01T10:00:12Z") {
			t.Fatalf("captured_at=%v", got)
		}
	}
	validation, _ := steps[2].(map[string]any)
	if validation["captured"] != false || validation["captured_at"] != nil {
		t.Fatalf("validation should be uncaptured: %v", validation)
	}
}

func stringify(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestListResultsJSONIncludesPayoutAndPayment(t *testing.T) {
	r, store := financeRouter(t)
	store.Payouts = []recon.PayoutFact{{
		PayoutID: "pout_1", ProviderStatus: "processed", AmountMinor: 5000, Currency: "INR", UTR: "HDFC123", Mode: "IMPS",
	}}
	store.Results = append(store.Results, recon.FinancialResult{
		EntityType: recon.EntityPayout, EntityID: "pout_1", Result: recon.ResultUnresolved, Reason: "payout_missing_bank",
		ExpectedAmount: 5000, VarianceAmount: 5000,
	})
	code, body := getJSON(t, r, "/v1/reconciliation/results?tenant_id=t&connector_id=c")
	if code != 200 {
		t.Fatalf("code=%d body=%v", code, body)
	}
	rows, _ := body["results"].([]any)
	if len(rows) != 2 {
		t.Fatalf("results=%v", body["results"])
	}
	if body["records"] != float64(2) {
		t.Fatalf("records=%v", body["records"])
	}
}

func TestListInvestigationsJSON(t *testing.T) {
	r, store := financeRouter(t)
	store.Investigations = []recon.InvestigationRecord{{
		ID: "inv_1", EntityID: "pay_1", Status: "completed", RootCause: "captured_missing_settlement", FinancialImpact: 100,
	}}
	code, body := getJSON(t, r, "/v1/reconciliation/investigations?tenant_id=t&connector_id=c")
	if code != 200 {
		t.Fatalf("code=%d body=%v", code, body)
	}
	list, _ := body["investigations"].([]any)
	if len(list) != 1 {
		t.Fatalf("investigations=%v", body["investigations"])
	}
}

func TestEvaluationJSON(t *testing.T) {
	r, _ := financeRouter(t)
	code, body := getJSON(t, r, "/v1/reconciliation/evaluation?tenant_id=t&connector_id=c")
	if code != 200 {
		t.Fatalf("code=%d body=%v", code, body)
	}
	if body["dataset_records"] != float64(1) {
		t.Fatalf("%v", body)
	}
	if body["reconciliation_rate"] != 1.0 {
		t.Fatalf("rate=%v", body["reconciliation_rate"])
	}
}
