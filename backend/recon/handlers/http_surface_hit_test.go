package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"zord-outcome-engine/internal/auth"
	"zord-outcome-engine/internal/recon"

	"github.com/gin-gonic/gin"
)

func financeAllRoutes(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	store := recon.NewMemoryFinancialStore()
	store.Payments = []recon.PaymentFact{{
		PaymentID: "pay_1", CanonicalStatus: recon.PaymentCaptured, ProviderStatus: "captured",
		Captured: true, AmountMinor: 10000, Currency: "INR", Sources: []string{"webhook"},
	}}
	store.Events["pay_1"] = []recon.ObservationFact{{
		Source: "webhook", ProviderStatus: "authorized", CanonicalStatus: "authorized", SourceEventID: "evt_1",
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
	rh := &ReconHandler{Service: recon.NewService(recon.NewMemoryStore())}

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(auth.WithPrincipalForTest(c.Request.Context(), "t"))
		c.Next()
	})
	r.GET("/v1/health", HealthCheck)
	r.GET("/v1/settlement/supported-psps", GetSupportedPSPs)
	r.GET("/v1/cash/instruments", h.ListInstruments)
	r.GET("/v1/bank-statements/:upload_id", rh.GetUpload)
	r.GET("/v1/merchant/transactions/:payment_id/proof", rh.GetProof)
	r.GET("/v1/merchant/transactions", rh.Transactions)
	r.GET("/v1/merchant/reconciliation/summary", rh.Summary)
	r.GET("/v1/merchant/reconciliation/gaps", rh.Gaps)
	r.GET("/v1/merchant/settlements/:settlement_id/breakdown", rh.Breakdown)
	r.GET("/v1/merchant/freshness", rh.Freshness)
	r.GET("/v1/merchant/evidence/:id/verify", rh.VerifyEvidence)
	r.GET("/v1/merchant/ask/proof", rh.AskProof)
	r.GET("/v1/reconciliation/payments/:payment_id", h.GetPayment)
	r.GET("/v1/reconciliation/payments/:payment_id/evidence", h.GetEvidence)
	r.GET("/v1/reconciliation/payments/:payment_id/timeline", h.GetPaymentTimeline)
	r.GET("/v1/reconciliation/payouts/:payout_id", h.GetPayout)
	r.GET("/v1/reconciliation/payouts/:payout_id/evidence", h.GetPayoutEvidence)
	r.GET("/v1/reconciliation/payouts/:payout_id/timeline", h.GetPayoutTimeline)
	r.GET("/v1/reconciliation/sla-policy", h.SLAPolicy)
	r.GET("/v1/reconciliation/results", h.ListResults)
	r.GET("/v1/reconciliation/evaluation", h.GetEvaluation)
	r.GET("/v1/reconciliation/summary", h.GetFinanceSummary)
	r.GET("/v1/reconciliation/cash-position", h.GetCashPosition)
	r.GET("/v1/reconciliation/cash-schedule", h.GetCashSchedule)
	r.GET("/v1/reconciliation/tax-breakdown/:payment_id", h.GetTaxBreakdown)
	r.GET("/v1/reconciliation/ledger", h.GetLedger)
	r.GET("/v1/reconciliation/refunds", h.ListRefunds)
	r.GET("/v1/reconciliation/marketplace/seller-patterns", h.ListMarketplaceSellerPatterns)
	r.GET("/v1/reconciliation/exceptions", h.ListExceptions)
	r.GET("/v1/reconciliation/exceptions/:id", h.GetException)
	r.POST("/v1/reconciliation/run", h.Run)
	r.GET("/v1/reconciliation/runs/:id", h.GetRun)
	r.POST("/v1/reconciliation/batches/:batch_id/reconcile", h.ReconcileBatch)
	r.GET("/v1/reconciliation/batches/:batch_id", h.GetBatch)
	r.POST("/v1/reconciliation/investigations", h.CreateInvestigation)
	r.GET("/v1/reconciliation/investigations", h.ListInvestigations)
	r.GET("/v1/reconciliation/investigations/:id", h.GetInvestigation)
	r.GET("/v1/reconciliation/settlements", h.SearchSettlements)
	r.GET("/v1/reconciliation/bank-transactions", h.SearchBank)
	r.GET("/v1/reconciliation/bank-transactions/:id", h.GetBank)
	r.POST("/internal/reconciliation/run", h.InternalRun)
	r.POST("/internal/recon/run", rh.Run)
	return r
}

func hit(t *testing.T, r *gin.Engine, method, path, body string) (int, string) {
	t.Helper()
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code, w.Body.String()
}

func TestHTTPSurfaceHitAllFinanceRoutes(t *testing.T) {
	r := financeAllRoutes(t)
	q := "?tenant_id=t&connector_id=c"
	type call struct {
		method, path, body string
	}
	calls := []call{
		{"GET", "/v1/health", ""},
		{"GET", "/v1/settlement/supported-psps", ""},
		{"GET", "/v1/cash/instruments", ""},
		{"GET", "/v1/bank-statements/upl_1", ""},
		{"GET", "/v1/merchant/transactions/pay_1/proof" + q, ""},
		{"GET", "/v1/merchant/transactions" + q, ""},
		{"GET", "/v1/merchant/reconciliation/summary" + q, ""},
		{"GET", "/v1/merchant/reconciliation/gaps" + q, ""},
		{"GET", "/v1/merchant/settlements/set_1/breakdown" + q, ""},
		{"GET", "/v1/merchant/freshness" + q, ""},
		{"GET", "/v1/merchant/evidence/ev_1/verify" + q, ""},
		{"GET", "/v1/merchant/ask/proof" + q, ""},
		{"GET", "/v1/reconciliation/payments/pay_1" + q, ""},
		{"GET", "/v1/reconciliation/payments/pay_1/evidence" + q, ""},
		{"GET", "/v1/reconciliation/payments/pay_1/timeline" + q, ""},
		{"GET", "/v1/reconciliation/payouts/pout_missing" + q, ""},
		{"GET", "/v1/reconciliation/payouts/pout_missing/evidence" + q, ""},
		{"GET", "/v1/reconciliation/payouts/pout_missing/timeline" + q, ""},
		{"GET", "/v1/reconciliation/sla-policy" + q, ""},
		{"GET", "/v1/reconciliation/results" + q, ""},
		{"GET", "/v1/reconciliation/evaluation" + q, ""},
		{"GET", "/v1/reconciliation/summary" + q, ""},
		{"GET", "/v1/reconciliation/cash-position" + q, ""},
		{"GET", "/v1/reconciliation/cash-schedule" + q, ""},
		{"GET", "/v1/reconciliation/tax-breakdown/pay_1" + q, ""},
		{"GET", "/v1/reconciliation/ledger" + q + "&entity_id=pay_1", ""},
		{"GET", "/v1/reconciliation/refunds" + q + "&payment_id=pay_1", ""},
		{"GET", "/v1/reconciliation/marketplace/seller-patterns" + q, ""},
		{"GET", "/v1/reconciliation/exceptions" + q, ""},
		{"GET", "/v1/reconciliation/exceptions/ex_missing" + q, ""},
		{"POST", "/v1/reconciliation/run" + q, `{"tenant_id":"t","connector_id":"c"}`},
		{"GET", "/v1/reconciliation/runs/run_missing" + q, ""},
		{"POST", "/v1/reconciliation/batches/b1/reconcile" + q, `{"tenant_id":"t","connector_id":"c"}`},
		{"GET", "/v1/reconciliation/batches/b1" + q, ""},
		{"POST", "/v1/reconciliation/investigations" + q, `{"entity_id":"pay_1"}`},
		{"GET", "/v1/reconciliation/investigations" + q, ""},
		{"GET", "/v1/reconciliation/investigations/inv_missing" + q, ""},
		{"GET", "/v1/reconciliation/settlements" + q, ""},
		{"GET", "/v1/reconciliation/bank-transactions" + q, ""},
		{"GET", "/v1/reconciliation/bank-transactions/bnk_missing" + q, ""},
		{"POST", "/internal/reconciliation/run", `{"tenant_id":"t","connector_id":"c"}`},
		{"POST", "/internal/recon/run", `{"tenant_id":"t","connector_id":"c"}`},
	}
	for _, c := range calls {
		code, body := hit(t, r, c.method, c.path, c.body)
		t.Logf("HIT %s %s -> %d", c.method, c.path, code)
		if code >= 500 {
			t.Errorf("%s %s 5xx=%d body=%s", c.method, c.path, code, truncate(body, 240))
		}
		if code == 200 && len(body) > 0 && body[0] == '{' && !json.Valid([]byte(body)) {
			t.Errorf("%s %s invalid json body=%s", c.method, c.path, truncate(body, 120))
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
