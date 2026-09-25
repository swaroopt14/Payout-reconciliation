package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"zord-outcome-engine/internal/auth"
	"zord-outcome-engine/internal/recon"

	"github.com/gin-gonic/gin"
)

type FinancialHandler struct {
	Service *recon.FinancialService
	Store   recon.FinancialStore
}

func (h *FinancialHandler) Run(c *gin.Context) {
	var body reconRunBody
	_ = c.ShouldBindJSON(&body)
	if body.TenantID == "" {
		body.TenantID = strings.TrimSpace(c.Query("tenant_id"))
	}
	if body.ConnectorID == "" {
		body.ConnectorID = strings.TrimSpace(c.Query("connector_id"))
	}
	if body.AccountID == "" {
		body.AccountID = strings.TrimSpace(c.Query("account_id"))
	}
	if body.TenantID == "" || body.ConnectorID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "tenant_id and connector_id are required"})
		return
	}
	if !auth.EnsureBodyTenant(c, body.TenantID) {
		return
	}
	h.run(c, body, false)
}

func (h *FinancialHandler) InternalRun(c *gin.Context) {
	var body reconRunBody
	_ = c.ShouldBindJSON(&body)
	if body.TenantID == "" {
		body.TenantID = strings.TrimSpace(c.Query("tenant_id"))
	}
	tenantID, ok := relayTenantMustMatch(c, body.TenantID)
	if !ok {
		return
	}
	body.TenantID = tenantID
	if body.ConnectorID == "" {
		body.ConnectorID = strings.TrimSpace(c.Query("connector_id"))
	}
	if body.AccountID == "" {
		body.AccountID = strings.TrimSpace(c.Query("account_id"))
	}
	h.run(c, body, true)
}

func (h *FinancialHandler) run(c *gin.Context, body reconRunBody, _ bool) {
	if h == nil || h.Service == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "financial recon not configured"})
		return
	}
	run, results, err := h.Service.Run(c.Request.Context(), recon.FinancialRunRequest{
		TenantID: body.TenantID, ConnectorID: body.ConnectorID, AccountID: body.AccountID,
		BatchID: body.BatchID, PayoutIDs: body.PayoutIDs,
	})
	if err != nil {
		if errors.Is(err, recon.ErrRunInProgress) {
			c.JSON(http.StatusConflict, gin.H{"error": "reconciliation already running"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := gin.H{
		"run_id":          run.ID,
		"status":          run.Status,
		"payment_count":   run.PaymentCount,
		"matched_count":   run.MatchedCount,
		"exception_count": run.ExceptionCount,
		"counts":          run.Counts,
		"result_count":    len(results),
		"rule_version":    recon.FinancialRuleVersion,
	}
	if strings.TrimSpace(body.BatchID) != "" {
		out["batch_id"] = strings.TrimSpace(body.BatchID)
		out["batch"] = recon.BatchCloseFromResults(strings.TrimSpace(body.BatchID), run.ID, results, len(body.PayoutIDs))
	}
	c.JSON(http.StatusOK, out)
}

func (h *FinancialHandler) ReconcileBatch(c *gin.Context) {
	var body reconRunBody
	_ = c.ShouldBindJSON(&body)
	body.BatchID = strings.TrimSpace(c.Param("batch_id"))
	if body.TenantID == "" {
		body.TenantID = strings.TrimSpace(c.Query("tenant_id"))
	}
	if body.ConnectorID == "" {
		body.ConnectorID = strings.TrimSpace(c.Query("connector_id"))
	}
	if body.AccountID == "" {
		body.AccountID = strings.TrimSpace(c.Query("account_id"))
	}
	if body.TenantID == "" || body.ConnectorID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "tenant_id and connector_id are required"})
		return
	}
	if body.BatchID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "batch_id is required"})
		return
	}
	if !auth.EnsureBodyTenant(c, body.TenantID) {
		return
	}
	h.run(c, body, false)
}

func (h *FinancialHandler) GetBatch(c *gin.Context) {
	tenantID, connectorID, ok := h.scope(c)
	if !ok {
		return
	}
	batchID := strings.TrimSpace(c.Param("batch_id"))
	closeDoc, found, err := h.Service.BatchClose(c.Request.Context(), tenantID, connectorID, batchID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "lookup_failed"})
		return
	}
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	}
	c.JSON(http.StatusOK, closeDoc)
}

func (h *FinancialHandler) GetRun(c *gin.Context) {
	tenantID, connectorID, ok := h.scope(c)
	if !ok {
		return
	}
	_ = connectorID
	run, err := h.Store.GetReconciliationRun(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": run})
}

func (h *FinancialHandler) GetPayment(c *gin.Context) {
	tenantID, connectorID, ok := h.scope(c)
	if !ok {
		return
	}
	pay, fr, found, err := h.Service.GetPayment(c.Request.Context(), tenantID, connectorID, c.Param("payment_id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "lookup_failed"})
		return
	}
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	}
	var events []recon.ObservationFact
	if h.Store != nil {
		events, _ = h.Store.ListObservationEvents(c.Request.Context(), tenantID, connectorID, pay.PaymentID)
	}
	c.JSON(http.StatusOK, gin.H{
		"status":          pay.CanonicalStatus,
		"provider_status": pay.ProviderStatus,
		"payment_id":      pay.PaymentID,
		"amount_minor":    pay.AmountMinor,
		"currency":        pay.Currency,
		"method":          pay.Method,
		"direction":       recon.DirectionInbound,
		"rail":            firstRail(fr.Rail, pay.Method),
		"captured":        pay.Captured,
		"sources":         pay.Sources,
		"observations":    observationJSON(events),
		"reconciliation":  recon.ReconJSON(fr),
		"evidence_refs":   fr.EvidenceRefs,
	})
}

func (h *FinancialHandler) GetPayout(c *gin.Context) {
	tenantID, connectorID, ok := h.scope(c)
	if !ok {
		return
	}
	po, fr, found, err := h.Service.GetPayout(c.Request.Context(), tenantID, connectorID, c.Param("payout_id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "lookup_failed"})
		return
	}
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	}
	var events []recon.ObservationFact
	if h.Store != nil {
		events, _ = h.Store.ListPayoutObservationFacts(c.Request.Context(), tenantID, connectorID, po.PayoutID)
	}
	obs := make([]gin.H, 0, len(events))
	for _, ev := range events {
		obs = append(obs, gin.H{
			"source":           ev.Source,
			"provider_status":  ev.ProviderStatus,
			"canonical_status": ev.CanonicalStatus,
			"source_event_id":  ev.SourceEventID,
			"source_hash":      ev.SourceHash,
			"utr":              ev.RawReference,
			"observed_at":      ev.ObservedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"status":              po.ProviderStatus,
		"provider_status":     po.ProviderStatus,
		"payout_id":           po.PayoutID,
		"amount_minor":        po.AmountMinor,
		"currency":            po.Currency,
		"utr":                 po.UTR,
		"mode":                po.Mode,
		"purpose":             po.Purpose,
		"direction":           recon.DirectionOutbound,
		"rail":                firstRail(fr.Rail, po.Mode),
		"status_reason":       po.StatusReason,
		"provider_created_at": po.ProviderCreatedAt,
		"observations":        obs,
		"reconciliation":      recon.ReconJSON(fr),
		"evidence_refs":       fr.EvidenceRefs,
	})
}

func (h *FinancialHandler) GetPayoutEvidence(c *gin.Context) {
	tenantID, connectorID, ok := h.scope(c)
	if !ok {
		return
	}
	_, fr, found, err := h.Service.GetPayout(c.Request.Context(), tenantID, connectorID, c.Param("payout_id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "lookup_failed"})
		return
	}
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"payout_id":     c.Param("payout_id"),
		"evidence_refs": fr.EvidenceRefs,
		"evidence_ids":  recon.EvidenceIDList(fr.EvidenceRefs),
	})
}

func (h *FinancialHandler) GetPayoutTimeline(c *gin.Context) {
	h.writeTimeline(c, "payout", c.Param("payout_id"))
}

func (h *FinancialHandler) GetPaymentTimeline(c *gin.Context) {
	h.writeTimeline(c, "payment", c.Param("payment_id"))
}

func (h *FinancialHandler) writeTimeline(c *gin.Context, entityType, entityID string) {
	tenantID, connectorID, ok := h.scope(c)
	if !ok {
		return
	}
	if h == nil || h.Service == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "financial recon not configured"})
		return
	}
	var (
		tl    recon.EntityTimeline
		found bool
		err   error
	)
	if entityType == "payout" {
		tl, found, err = h.Service.PayoutTimeline(c.Request.Context(), tenantID, connectorID, entityID)
	} else {
		tl, found, err = h.Service.PaymentTimeline(c.Request.Context(), tenantID, connectorID, entityID)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "lookup_failed"})
		return
	}
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	}
	c.JSON(http.StatusOK, tl)
}

func (h *FinancialHandler) SLAPolicy(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"policies": []gin.H{
			{"entity": "payout", "mode": "IMPS", "sla_minutes": 15},
			{"entity": "payout", "mode": "NEFT", "sla_minutes": 60},
			{"entity": "payment", "open_status_hours": 72},
		},
	})
}

func (h *FinancialHandler) GetEvidence(c *gin.Context) {
	tenantID, connectorID, ok := h.scope(c)
	if !ok {
		return
	}
	_, fr, found, err := h.Service.GetPayment(c.Request.Context(), tenantID, connectorID, c.Param("payment_id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "lookup_failed"})
		return
	}
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"payment_id":    c.Param("payment_id"),
		"evidence_refs": fr.EvidenceRefs,
		"evidence_ids":  recon.EvidenceIDList(fr.EvidenceRefs),
	})
}

func (h *FinancialHandler) ListResults(c *gin.Context) {
	tenantID, connectorID, ok := h.scope(c)
	if !ok {
		return
	}
	if h.Service == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "financial recon not configured"})
		return
	}
	out, err := h.Service.ListFinanceResults(c.Request.Context(), tenantID, connectorID, c.Query("result"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	total := len(out.Results)
	offset, limit, page, pageSize := pageWindow(c, total)
	if offset < total {
		out.Results = out.Results[offset : offset+limit]
	} else {
		out.Results = out.Results[:0]
	}
	c.JSON(http.StatusOK, gin.H{
		"records":     out.Records,
		"matched":     out.Matched,
		"exceptions":  out.Exceptions,
		"results":     out.Results,
		"page":        page,
		"page_size":   pageSize,
		"total":       total,
	})
}

func (h *FinancialHandler) GetEvaluation(c *gin.Context) {
	tenantID, connectorID, ok := h.scope(c)
	if !ok {
		return
	}
	if h.Service == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "financial recon not configured"})
		return
	}
	out, err := h.Service.Evaluation(c.Request.Context(), tenantID, connectorID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, out)
}

func (h *FinancialHandler) ListInvestigations(c *gin.Context) {
	tenantID, connectorID, ok := h.scope(c)
	if !ok {
		return
	}
	list, err := h.Store.ListInvestigations(c.Request.Context(), tenantID, connectorID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if list == nil {
		list = []recon.InvestigationRecord{}
	}
	total := len(list)
	offset, limit, page, pageSize := pageWindow(c, total)
	pageItems := list[offset : offset+limit]
	c.JSON(http.StatusOK, gin.H{
		"investigations": pageItems, "page": page, "page_size": pageSize, "total": total,
	})
}

func (h *FinancialHandler) GetFinanceSummary(c *gin.Context) {
	tenantID, connectorID, ok := h.scope(c)
	if !ok {
		return
	}
	sum, err := h.Service.FinanceSummary(c.Request.Context(), tenantID, connectorID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, sum)
}

func (h *FinancialHandler) GetCashPosition(c *gin.Context) {
	tenantID, connectorID, ok := h.scope(c)
	if !ok {
		return
	}
	snap, err := h.Service.CashPosition(c.Request.Context(), tenantID, connectorID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, snap)
}

func (h *FinancialHandler) ListExceptions(c *gin.Context) {
	tenantID, connectorID, ok := h.scope(c)
	if !ok {
		return
	}
	var list []recon.ReconciliationException
	var err error
	if h.Service != nil {
		// Persisted recon exceptions + derived refund_without_reverse_transfer.
		list, err = h.Service.ListExceptions(c.Request.Context(), tenantID, connectorID)
	} else {
		list, err = h.Store.ListReconciliationExceptions(c.Request.Context(), tenantID, connectorID)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	entity := strings.TrimSpace(c.Query("entity_type"))
	reason := strings.TrimSpace(c.Query("reason"))
	var out []recon.ReconciliationException
	for _, ex := range list {
		if entity != "" && !strings.EqualFold(ex.EntityType, entity) {
			continue
		}
		if reason != "" && ex.Reason != reason {
			continue
		}
		out = append(out, ex)
	}
	total := len(out)
	offset, limit, page, pageSize := pageWindow(c, total)
	c.JSON(http.StatusOK, gin.H{
		"exceptions": out[offset : offset+limit], "page": page, "page_size": pageSize, "total": total,
	})
}

func (h *FinancialHandler) GetException(c *gin.Context) {
	tenantID, connectorID, ok := h.scope(c)
	if !ok {
		return
	}
	var ex recon.ReconciliationException
	var found bool
	var err error
	if h.Service != nil {
		ex, found, err = h.Service.GetException(c.Request.Context(), tenantID, connectorID, c.Param("id"))
	} else {
		ex, found, err = h.Store.GetReconciliationException(c.Request.Context(), tenantID, connectorID, c.Param("id"))
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "lookup_failed"})
		return
	}
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": ex})
}

func (h *FinancialHandler) CreateInvestigation(c *gin.Context) {
	tenantID, connectorID, ok := h.scope(c)
	if !ok {
		return
	}
	var body struct {
		ExceptionID string `json:"exception_id"`
		EntityID    string `json:"entity_id"`
		PaymentID   string `json:"payment_id"`
	}
	_ = c.ShouldBindJSON(&body)
	if body.EntityID == "" {
		body.EntityID = body.PaymentID
	}
	rec, err := h.Service.Investigate(c.Request.Context(), tenantID, connectorID, body.ExceptionID, body.EntityID)
	if err != nil {
		if recon.IsNotFound(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": rec})
}

func (h *FinancialHandler) GetInvestigation(c *gin.Context) {
	tenantID, connectorID, ok := h.scope(c)
	if !ok {
		return
	}
	rec, found, err := h.Store.GetInvestigation(c.Request.Context(), tenantID, connectorID, c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "lookup_failed"})
		return
	}
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": rec})
}

func (h *FinancialHandler) SearchSettlements(c *gin.Context) {
	tenantID, connectorID, ok := h.scope(c)
	if !ok {
		return
	}
	lines, err := h.Store.ListSettlementLines(c.Request.Context(), tenantID, connectorID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	pid := strings.TrimSpace(c.Query("payment_id"))
	lineType := strings.TrimSpace(c.Query("line_type"))
	var out []recon.SettlementLine
	for _, l := range lines {
		if pid != "" && l.PaymentID != pid && l.EntityID != pid {
			continue
		}
		if lineType != "" && !strings.EqualFold(l.LineType, lineType) {
			continue
		}
		out = append(out, l)
	}
	total := len(out)
	offset, limit, page, pageSize := pageWindow(c, total)
	c.JSON(http.StatusOK, gin.H{
		"settlements": out[offset : offset+limit], "page": page, "page_size": pageSize, "total": total,
	})
}

func (h *FinancialHandler) SearchBank(c *gin.Context) {
	tenantID, connectorID, ok := h.scope(c)
	if !ok {
		return
	}
	banks, err := h.Store.ListBankTxns(c.Request.Context(), tenantID, connectorID, c.Query("account_id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	results, err := h.Store.ListReconciliationResults(c.Request.Context(), tenantID, connectorID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	byBank := map[string]recon.FinancialResult{}
	for _, fr := range results {
		if fr.EntityType == recon.EntityBank {
			byBank[fr.EntityID] = fr
		}
		if fr.EvidenceRefs.BankObservationID != "" {
			byBank[fr.EvidenceRefs.BankObservationID] = fr
		}
	}
	id := strings.TrimSpace(c.Query("id"))
	utr := strings.TrimSpace(c.Query("utr"))
	var out []gin.H
	for _, b := range banks {
		if id != "" && b.ID != id && b.BankTxnID != id {
			continue
		}
		if utr != "" && !strings.EqualFold(b.UTR, utr) && !strings.EqualFold(b.UTRRaw, utr) {
			continue
		}
		result := recon.ResultUnresolved
		reason := "bank_not_matched"
		if fr, ok := byBank[b.ID]; ok {
			result = fr.Result
			reason = fr.Reason
		} else if fr, ok := byBank[b.BankTxnID]; ok {
			result = fr.Result
			reason = fr.Reason
		}
		out = append(out, gin.H{
			"id": b.ID, "account_id": b.AccountID, "bank_txn_id": b.BankTxnID,
			"utr": b.UTR, "description": b.Description, "currency": b.Currency,
			"credit_minor": b.CreditMinor, "debit_minor": b.DebitMinor,
			"credit_debit": b.CreditDebit, "value_date": b.ValueDate,
			"recon_result": result, "recon_reason": reason,
		})
	}
	if out == nil {
		out = []gin.H{}
	}
	total := len(out)
	offset, limit, page, pageSize := pageWindow(c, total)
	c.JSON(http.StatusOK, gin.H{
		"bank_transactions": out[offset : offset+limit], "page": page, "page_size": pageSize, "total": total,
	})
}

func (h *FinancialHandler) GetBank(c *gin.Context) {
	tenantID, connectorID, ok := h.scope(c)
	if !ok {
		return
	}
	banks, err := h.Store.ListBankTxns(c.Request.Context(), tenantID, connectorID, c.Query("account_id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	id := c.Param("id")
	for _, b := range banks {
		if b.ID == id || b.BankTxnID == id {
			c.JSON(http.StatusOK, gin.H{"data": b})
			return
		}
	}
	c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
}

func (h *FinancialHandler) ListInstruments(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"instruments": recon.RazorpayInstruments(),
		"psps":        []string{"razorpay", "cashfree", "payu", "stripe"},
		"legs": gin.H{
			"two_way":   "merchant_books vs PSP when merchant facts are present; otherwise PSP payment vs PSP settlement (legacy merchant_psp_settled)",
			"three_way": "two_way plus proven bank CREDIT (inbound) or DEBIT (outbound)",
		},
		"source_kinds": []string{
			recon.SourceKindMerchant, recon.SourceKindPayment, recon.SourceKindPayout,
			recon.SourceKindSettlement, recon.SourceKindBank, recon.SourceKindDispute,
		},
	})
}

func firstRail(fromResult, raw string) string {
	if strings.TrimSpace(fromResult) != "" {
		return recon.NormalizeRail(fromResult)
	}
	return recon.NormalizeRail(raw)
}

func (h *FinancialHandler) scope(c *gin.Context) (string, string, bool) {
	queryTenant := strings.TrimSpace(c.Query("tenant_id"))
	connectorID := strings.TrimSpace(c.Query("connector_id"))
	if connectorID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "tenant_id and connector_id are required"})
		return "", "", false
	}
	if principalTenant, ok := auth.PrincipalTenant(c); ok {
		if queryTenant != "" && !strings.EqualFold(queryTenant, principalTenant) {
			c.JSON(http.StatusForbidden, gin.H{"error": "requested tenant is not authorised for this principal"})
			return "", "", false
		}
		return principalTenant, connectorID, true
	}
	if queryTenant == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "tenant_id and connector_id are required"})
		return "", "", false
	}
	return queryTenant, connectorID, true
}

func observationJSON(events []recon.ObservationFact) []gin.H {
	out := make([]gin.H, 0, len(events))
	for _, ev := range events {
		out = append(out, gin.H{
			"source":           ev.Source,
			"provider_status":  ev.ProviderStatus,
			"canonical_status": ev.CanonicalStatus,
			"source_event_id":  ev.SourceEventID,
			"source_hash":      ev.SourceHash,
			"observed_at":      ev.ObservedAt,
		})
	}
	return out
}

func (h *FinancialHandler) GetTaxBreakdown(c *gin.Context) {
	tenantID, connectorID, ok := h.scope(c)
	if !ok {
		return
	}
	tb, err := h.Service.TaxBreakdown(c.Request.Context(), tenantID, connectorID, c.Param("payment_id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	}
	c.JSON(http.StatusOK, tb)
}

func (h *FinancialHandler) GetCashSchedule(c *gin.Context) {
	tenantID, connectorID, ok := h.scope(c)
	if !ok {
		return
	}
	sch, err := h.Service.CashSchedule(c.Request.Context(), tenantID, connectorID, 7)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, sch)
}

func (h *FinancialHandler) GetLedger(c *gin.Context) {
	tenantID, connectorID, ok := h.scope(c)
	if !ok {
		return
	}
	led, err := h.Service.Ledger(c.Request.Context(), tenantID, connectorID, strings.TrimSpace(c.Query("entity_id")))
	if err != nil {
		if strings.TrimSpace(c.Query("entity_id")) == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "entity_id is required"})
			return
		}
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	}
	c.JSON(http.StatusOK, led)
}

func (h *FinancialHandler) ListRefunds(c *gin.Context) {
	tenantID, connectorID, ok := h.scope(c)
	if !ok {
		return
	}
	list, err := h.Service.ListRefunds(c.Request.Context(), tenantID, connectorID, strings.TrimSpace(c.Query("payment_id")))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if list == nil {
		list = []recon.RefundFact{}
	}
	out := gin.H{
		"payment_id": strings.TrimSpace(c.Query("payment_id")),
		"refunds":    list,
		"source":     "provider_refund_observations",
	}
	if len(list) == 0 {
		out["error"] = "not_found"
	}
	c.JSON(http.StatusOK, out)
}

func (h *FinancialHandler) ListMarketplaceSellerPatterns(c *gin.Context) {
	tenantID, connectorID, ok := h.scope(c)
	if !ok {
		return
	}
	if h == nil || h.Service == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "financial recon not configured"})
		return
	}
	res, err := h.Service.MarketplaceSellerPatterns(c.Request.Context(), tenantID, connectorID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if res.Sellers == nil {
		res.Sellers = []recon.MarketplaceSellerPattern{}
	}
	c.JSON(http.StatusOK, res)
}

func (h *FinancialHandler) ListMarketplaceRefundGraphExceptions(c *gin.Context) {
	tenantID, connectorID, ok := h.scope(c)
	if !ok {
		return
	}
	if h == nil || h.Service == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "financial recon not configured"})
		return
	}
	res, err := h.Service.MarketplaceRefundGraphExceptions(c.Request.Context(), tenantID, connectorID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if res.Signals == nil {
		res.Signals = []recon.MarketplaceRefundGraphSignal{}
	}
	c.JSON(http.StatusOK, res)
}

func (h *FinancialHandler) ListMarketplaceVelocityFlags(c *gin.Context) {
	tenantID, connectorID, ok := h.scope(c)
	if !ok {
		return
	}
	if h == nil || h.Service == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "financial recon not configured"})
		return
	}
	cfg := recon.MarketplaceVelocityConfig{
		HoldEnabled: strings.EqualFold(strings.TrimSpace(c.Query("hold_enabled")), "true") ||
			strings.TrimSpace(c.Query("hold_enabled")) == "1",
	}
	if v := strings.TrimSpace(c.Query("count_threshold")); v != "" {
		var n int64
		if _, err := fmt.Sscan(v, &n); err == nil {
			cfg.CountThreshold = n
		}
	}
	if v := strings.TrimSpace(c.Query("sum_threshold")); v != "" {
		var n int64
		if _, err := fmt.Sscan(v, &n); err == nil {
			cfg.SumThreshold = n
		}
	}
	res, err := h.Service.MarketplaceVelocityFlags(c.Request.Context(), tenantID, connectorID, cfg)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if res.Flags == nil {
		res.Flags = []recon.MarketplaceVelocityFlag{}
	}
	c.JSON(http.StatusOK, res)
}
