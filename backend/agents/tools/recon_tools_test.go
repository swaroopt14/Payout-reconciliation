package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestToolNames(t *testing.T) {
	names := Names()
	want := map[string]bool{
		GetTransactionProof: true, GetSettlementBreakdown: true,
		GetBankMatch: true, GetPaymentGaps: true, GetFreshnessStatus: true,
		GetPayment: true, GetPaymentEvents: true, GetSettlement: true, SearchSettlements: true,
		GetBankTransaction: true, SearchBankTxns: true, GetReconciliation: true, GetException: true,
		GetRefund: true, GetEvidence: true, GetPayout: true, GetPayoutEvents: true,
		GetSLAPolicy: true, GetSimilarCases: true, GetLedgerEntry: true,
		GetEvidencePack: true, GetDecisionTrace: true, GetCalculationTrace: true,
		GetAuditTrail: true, VerifyEvidenceTool: true, GetSourceSnapshot: true,
		GetReconSummary: true, GetCashPosition: true, GetTaxBreakdown: true, GetCashSchedule: true,
		GetMarketplaceRefundGraphExceptions: true, GetMarketplaceVelocityFlags: true,
	}
	if len(names) != len(want) {
		t.Fatalf("%v", names)
	}
	for _, n := range names {
		if !want[n] {
			t.Fatalf("unexpected %s", n)
		}
	}
}

func TestBankCreditProvenRequiresProvenField(t *testing.T) {
	if BankCreditProven(map[string]any{"data": map[string]any{"proof_summary": map[string]any{
		"provider_settled": "proven", "bank_credited": "unproven",
	}}}) {
		t.Fatal("settled must not imply bank credited")
	}
	if !BankCreditProven(map[string]any{"data": map[string]any{"proof_summary": map[string]any{"bank_credited": "proven"}}}) {
		t.Fatal("expected proven")
	}
}

func TestSelectToolBankQuestion(t *testing.T) {
	if SelectTool("Did the money for pay_test_001 reach the bank?") != GetBankMatch {
		t.Fatal(SelectTool("Did the money for pay_test_001 reach the bank?"))
	}
}

func TestAnswerBankQuestionDoesNotInventCredit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"message": "provider settlement proven; bank credit unresolved",
				"proof_summary": map[string]any{
					"provider_settled": "proven",
					"bank_credited":    "unproven",
					"fully_reconciled": false,
				},
			},
		})
	}))
	defer srv.Close()
	c := NewOutcomeClient(srv.URL, "")
	ans, ok := Answer(c, "t", "c", "Did the money for pay_test_001 reach the bank?")
	if !ok {
		t.Fatal("expected handled")
	}
	if strings.Contains(strings.ToLower(ans), "fully reconciled") {
		t.Fatalf("%s", ans)
	}
	if !strings.Contains(ans, "Not proven") && !strings.Contains(ans, "not proven") {
		t.Fatalf("%s", ans)
	}
}
