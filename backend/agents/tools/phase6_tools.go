package tools

import (
	"fmt"
	"net/url"
	"strings"
)

const (
	GetPayment                          = "get_payment"
	GetPaymentEvents                    = "get_payment_events"
	GetSettlement                       = "get_settlement"
	SearchSettlements                   = "search_settlements"
	GetBankTransaction                  = "get_bank_transaction"
	SearchBankTxns                      = "search_bank_transactions"
	GetReconciliation                   = "get_reconciliation"
	GetException                        = "get_exception"
	GetRefund                           = "get_refund"
	GetEvidence                         = "get_evidence"
	GetPayout                           = "get_payout"
	GetPayoutEvents                     = "get_payout_events"
	GetSLAPolicy                        = "get_sla_policy"
	GetSimilarCases                     = "get_similar_cases"
	GetLedgerEntry                      = "get_ledger_entry"
	GetEvidencePack                     = "get_evidence_pack"
	GetDecisionTrace                    = "get_decision_trace"
	GetCalculationTrace                 = "get_calculation_trace"
	GetAuditTrail                       = "get_audit_trail"
	VerifyEvidenceTool                  = "verify_evidence"
	GetSourceSnapshot                   = "get_source_snapshot"
	GetReconSummary                     = "get_recon_summary"
	GetCashPosition                     = "get_cash_position"
	GetTaxBreakdown                     = "get_tax_breakdown"
	GetCashSchedule                     = "get_cash_schedule"
	GetMarketplaceRefundGraphExceptions = "get_marketplace_refund_graph_exceptions"
	GetMarketplaceVelocityFlags         = "get_marketplace_velocity_flags"
)

func Phase6Names() []string {
	return []string{
		GetPayment, GetPaymentEvents, GetSettlement, SearchSettlements,
		GetBankTransaction, SearchBankTxns, GetReconciliation, GetException,
		GetRefund, GetEvidence, GetPayout, GetPayoutEvents, GetSLAPolicy, GetSimilarCases,
		GetLedgerEntry,
		GetEvidencePack, GetDecisionTrace, GetCalculationTrace, GetAuditTrail,
		VerifyEvidenceTool, GetSourceSnapshot, GetReconSummary, GetCashPosition,
		GetTaxBreakdown, GetCashSchedule,
		GetMarketplaceRefundGraphExceptions, GetMarketplaceVelocityFlags,
	}
}

func Names() []string {
	return append([]string{
		GetTransactionProof, GetSettlementBreakdown, GetBankMatch, GetPaymentGaps, GetFreshnessStatus,
	}, Phase6Names()...)
}

func SourceNotInThisPhase(tool string) map[string]any {
	return map[string]any{
		"tool":    tool,
		"error":   "source_not_in_this_phase",
		"message": "This source is not implemented in Phase 6. Do not invent records.",
	}
}

func tenantQ(tenantID, connectorID string) url.Values {
	q := url.Values{}
	q.Set("tenant_id", tenantID)
	q.Set("connector_id", connectorID)
	return q
}

func (c *OutcomeClient) GetPayment(tenantID, connectorID, paymentID string) (map[string]any, error) {
	return c.get("/v1/reconciliation/payments/"+paymentID, tenantQ(tenantID, connectorID))
}

func (c *OutcomeClient) GetPaymentEvents(tenantID, connectorID, paymentID string) (map[string]any, error) {
	return c.GetPayment(tenantID, connectorID, paymentID)
}

func (c *OutcomeClient) SearchSettlements(tenantID, connectorID, paymentID, lineType string) (map[string]any, error) {
	q := tenantQ(tenantID, connectorID)
	if paymentID != "" {
		q.Set("payment_id", paymentID)
	}
	if lineType != "" {
		q.Set("line_type", lineType)
	}
	return c.get("/v1/reconciliation/settlements", q)
}

func (c *OutcomeClient) GetSettlement(tenantID, connectorID, paymentID string) (map[string]any, error) {
	return c.SearchSettlements(tenantID, connectorID, paymentID, "")
}

func (c *OutcomeClient) SearchBankTransactions(tenantID, connectorID, id, utr string) (map[string]any, error) {
	q := tenantQ(tenantID, connectorID)
	if id != "" {
		q.Set("id", id)
	}
	if utr != "" {
		q.Set("utr", utr)
	}
	return c.get("/v1/reconciliation/bank-transactions", q)
}

func (c *OutcomeClient) GetBankTransaction(tenantID, connectorID, id string) (map[string]any, error) {
	return c.get("/v1/reconciliation/bank-transactions/"+id, tenantQ(tenantID, connectorID))
}

func (c *OutcomeClient) GetReconciliation(tenantID, connectorID, paymentID string) (map[string]any, error) {
	return c.GetPayment(tenantID, connectorID, paymentID)
}

func (c *OutcomeClient) GetException(tenantID, connectorID, id string) (map[string]any, error) {
	if id == "" {
		return c.get("/v1/reconciliation/exceptions", tenantQ(tenantID, connectorID))
	}
	return c.get("/v1/reconciliation/exceptions/"+id, tenantQ(tenantID, connectorID))
}

func (c *OutcomeClient) GetRefund(tenantID, connectorID, paymentID string) (map[string]any, error) {
	q := tenantQ(tenantID, connectorID)
	if paymentID != "" {
		q.Set("payment_id", paymentID)
	}
	return c.getOptional("/v1/reconciliation/refunds", q)
}

func (c *OutcomeClient) GetLedgerEntry(tenantID, connectorID, paymentID string) (map[string]any, error) {
	q := tenantQ(tenantID, connectorID)
	q.Set("entity_id", paymentID)
	q.Set("entity_type", "payment")
	body, err := c.getOptional("/v1/reconciliation/ledger", q)
	if err != nil {
		return nil, err
	}
	if body == nil || body["error"] == "not_found" || body["error"] == "none" {
		return map[string]any{
			"entity_type": "payment",
			"entity_id":   paymentID,
			"lines":       []any{},
			"balanced":    true,
			"limitations": []string{"No derived ledger lines were returned. Do not invent a ledger entry."},
		}, nil
	}
	return body, nil
}

func (c *OutcomeClient) GetEvidence(tenantID, connectorID, paymentID string) (map[string]any, error) {
	return c.get("/v1/reconciliation/payments/"+paymentID+"/evidence", tenantQ(tenantID, connectorID))
}

func (c *OutcomeClient) GetPayout(tenantID, connectorID, payoutID string) (map[string]any, error) {
	return c.getOptional("/v1/reconciliation/payouts/"+payoutID, tenantQ(tenantID, connectorID))
}

func (c *OutcomeClient) GetPayoutEvents(tenantID, connectorID, payoutID string) (map[string]any, error) {
	return c.GetPayout(tenantID, connectorID, payoutID)
}

func (c *OutcomeClient) GetPayoutEvidence(tenantID, connectorID, payoutID string) (map[string]any, error) {
	return c.getOptional("/v1/reconciliation/payouts/"+payoutID+"/evidence", tenantQ(tenantID, connectorID))
}

func (c *OutcomeClient) GetSLAPolicy(tenantID, connectorID string) (map[string]any, error) {
	return c.getOptional("/v1/reconciliation/sla-policy", tenantQ(tenantID, connectorID))
}

func (c *OutcomeClient) GetSimilarCases(tenantID, connectorID, entityType, reason string) (map[string]any, error) {
	q := tenantQ(tenantID, connectorID)
	if entityType != "" {
		q.Set("entity_type", entityType)
	}
	if reason != "" {
		q.Set("reason", reason)
	}
	return c.getOptional("/v1/reconciliation/exceptions", q)
}

func (c *OutcomeClient) GetReconSummary(tenantID, connectorID string) (map[string]any, error) {
	return c.getOptional("/v1/reconciliation/summary", tenantQ(tenantID, connectorID))
}

func (c *OutcomeClient) GetCashPosition(tenantID, connectorID string) (map[string]any, error) {
	return c.getOptional("/v1/reconciliation/cash-position", tenantQ(tenantID, connectorID))
}

func (c *OutcomeClient) GetTaxBreakdown(tenantID, connectorID, paymentID string) (map[string]any, error) {
	return c.getOptional("/v1/reconciliation/tax-breakdown/"+paymentID, tenantQ(tenantID, connectorID))
}

func (c *OutcomeClient) GetCashSchedule(tenantID, connectorID string) (map[string]any, error) {
	return c.getOptional("/v1/reconciliation/cash-schedule", tenantQ(tenantID, connectorID))
}

func (c *OutcomeClient) GetMarketplaceRefundGraphExceptions(tenantID, connectorID string) (map[string]any, error) {
	return c.getOptional("/v1/reconciliation/marketplace/refund-graph-exceptions", tenantQ(tenantID, connectorID))
}

// GetMarketplaceVelocityFlags fetches velocity ops flags.
// HoldRecommended appears only when holdEnabled is true (merchant HoldEnabled).
// Agents never auto-block payouts from this surface.
func (c *OutcomeClient) GetMarketplaceVelocityFlags(tenantID, connectorID string, holdEnabled bool) (map[string]any, error) {
	q := tenantQ(tenantID, connectorID)
	if holdEnabled {
		q.Set("hold_enabled", "true")
	} else {
		q.Set("hold_enabled", "false")
	}
	return c.getOptional("/v1/reconciliation/marketplace/velocity-flags", q)
}

func CallTool(c *OutcomeClient, name, tenantID, connectorID, id string) (map[string]any, error) {
	switch name {
	case GetPayment, GetPaymentEvents, GetReconciliation:
		return c.GetPayment(tenantID, connectorID, id)
	case GetSettlement, SearchSettlements:
		return c.GetSettlement(tenantID, connectorID, id)
	case GetBankTransaction, SearchBankTxns:
		if id == "" {
			return c.SearchBankTransactions(tenantID, connectorID, "", "")
		}
		return c.GetBankTransaction(tenantID, connectorID, id)
	case GetException:
		return c.GetException(tenantID, connectorID, id)
	case GetRefund:
		return c.GetRefund(tenantID, connectorID, id)
	case GetEvidence:
		return c.GetEvidence(tenantID, connectorID, id)
	case GetPayout, GetPayoutEvents:
		return c.GetPayout(tenantID, connectorID, id)
	case GetSLAPolicy:
		return c.GetSLAPolicy(tenantID, connectorID)
	case GetSimilarCases:
		return c.GetSimilarCases(tenantID, connectorID, "payout", id)
	case GetLedgerEntry:
		return c.GetLedgerEntry(tenantID, connectorID, id)
	case GetEvidencePack:
		return c.GetEvidencePack(tenantID, id)
	case GetDecisionTrace:
		return c.GetDecisionTrace(tenantID, entityTypeFromID(id), id)
	case GetCalculationTrace:
		return c.GetCalculationTrace(tenantID, entityTypeFromID(id), id)
	case GetAuditTrail:
		return c.GetAuditTrail(tenantID, entityTypeFromID(id), id)
	case VerifyEvidenceTool:
		return c.VerifyEvidence(tenantID, id)
	case GetSourceSnapshot:
		return c.GetSourceSnapshot(tenantID, id)
	case GetReconSummary:
		return c.GetReconSummary(tenantID, connectorID)
	case GetCashPosition:
		return c.GetCashPosition(tenantID, connectorID)
	case GetTaxBreakdown:
		return c.GetTaxBreakdown(tenantID, connectorID, id)
	case GetCashSchedule:
		return c.GetCashSchedule(tenantID, connectorID)
	case GetMarketplaceRefundGraphExceptions:
		return c.GetMarketplaceRefundGraphExceptions(tenantID, connectorID)
	case GetMarketplaceVelocityFlags:
		holdEnabled := id == "true" || id == "1" || strings.EqualFold(id, "hold_enabled")
		return c.GetMarketplaceVelocityFlags(tenantID, connectorID, holdEnabled)
	default:
		return nil, fmt.Errorf("unknown tool %s", name)
	}
}

func entityTypeFromID(id string) string {
	low := strings.ToLower(id)
	if strings.HasPrefix(low, "pout_") {
		return "payout"
	}
	return "payment"
}

// LedgerEmpty is true when the derived cash ledger has no lines.
// Callers must not invent journal entries in that case.
func LedgerEmpty(body map[string]any) bool {
	if body == nil {
		return true
	}
	if err, _ := body["error"].(string); err == "source_not_in_this_phase" || err == "not_found" || err == "none" {
		return true
	}
	switch v := body["lines"].(type) {
	case []any:
		return len(v) == 0
	case []map[string]any:
		return len(v) == 0
	default:
		return true
	}
}

func HasRecords(body map[string]any, keys ...string) bool {
	if body == nil {
		return false
	}
	for _, k := range keys {
		switch v := body[k].(type) {
		case []any:
			if len(v) > 0 {
				return true
			}
		case map[string]any:
			if len(v) > 0 {
				return true
			}
		case string:
			if strings.TrimSpace(v) != "" {
				return true
			}
		}
	}
	return false
}
