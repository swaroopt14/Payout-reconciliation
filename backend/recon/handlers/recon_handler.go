package handlers

import (
	"io"
	"net/http"
	"strings"

	"zord-outcome-engine/internal/recon"
	"zord-outcome-engine/services"

	"github.com/gin-gonic/gin"
)

type ReconHandler struct {
	Service *recon.Service
	Parser  services.BankStatementParser
}

func (h *ReconHandler) requireRelay(c *gin.Context) bool {
	if !authorizeRelay(c.Request) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return false
	}
	return true
}

func (h *ReconHandler) UploadBankStatement(c *gin.Context) {
	tenantID := strings.TrimSpace(c.Query("tenant_id"))
	connectorID := strings.TrimSpace(c.Query("connector_id"))
	accountID := strings.TrimSpace(c.Query("account_id"))
	if tenantID == "" || connectorID == "" || accountID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "tenant_id, connector_id, and account_id are required"})
		return
	}
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file is required"})
		return
	}
	defer file.Close()
	if !strings.HasSuffix(strings.ToLower(header.Filename), ".csv") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "csv required"})
		return
	}
	raw, err := io.ReadAll(file)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read file"})
		return
	}
	parsed, err := h.Parser.Parse(raw, accountID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	up, err := h.Service.Store.InsertUpload(c.Request.Context(), recon.BankUpload{
		TenantID: tenantID, ConnectorID: connectorID, AccountID: accountID,
		Filename: header.Filename, FileHash: parsed.FileHash, RowCount: parsed.RowCount, Status: "succeeded",
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := h.Service.Store.InsertBankTxns(c.Request.Context(), tenantID, connectorID, up.ID, parsed.Rows); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"upload_id": up.ID, "row_count": parsed.RowCount, "file_hash": parsed.FileHash, "status": "succeeded"})
}

func (h *ReconHandler) GetUpload(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"upload_id": c.Param("upload_id")})
}

type reconRunBody struct {
	TenantID    string   `json:"tenant_id"`
	ConnectorID string   `json:"connector_id"`
	AccountID   string   `json:"account_id"`
	BatchID     string   `json:"batch_id"`
	PayoutIDs   []string `json:"payout_ids"`
}

func (h *ReconHandler) Run(c *gin.Context) {
	if !h.requireRelay(c) {
		return
	}
	var body reconRunBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	subjects, err := h.Service.Run(c.Request.Context(), body.TenantID, body.ConnectorID, body.AccountID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"count": len(subjects), "rule_version": recon.RuleVersion})
}

func (h *ReconHandler) GetProof(c *gin.Context) {
	tenantID := strings.TrimSpace(c.Query("tenant_id"))
	connectorID := strings.TrimSpace(c.Query("connector_id"))
	body, err := h.Service.GetProof(c.Request.Context(), tenantID, connectorID, c.Param("payment_id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	}
	c.JSON(http.StatusOK, body)
}

func (h *ReconHandler) Summary(c *gin.Context) {
	counts, err := h.Service.Summary(c.Request.Context(), c.Query("tenant_id"), c.Query("connector_id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"counts": counts})
}

func (h *ReconHandler) Transactions(c *gin.Context) {
	list, err := h.Service.Store.ListProofs(c.Request.Context(), c.Query("tenant_id"), c.Query("connector_id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"transactions": list})
}

func (h *ReconHandler) Breakdown(c *gin.Context) {
	wf, err := h.Service.Breakdown(c.Request.Context(), c.Query("tenant_id"), c.Query("connector_id"), c.Param("settlement_id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"settlement_id": c.Param("settlement_id"), "waterfall": wf})
}

func (h *ReconHandler) Gaps(c *gin.Context) {
	gaps, err := h.Service.Gaps(c.Request.Context(), c.Query("tenant_id"), c.Query("connector_id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	counts := map[string]int{}
	for _, g := range gaps {
		counts[g.ReconciliationState]++
	}
	c.JSON(http.StatusOK, gin.H{"gaps": gaps, "counts": counts})
}

func (h *ReconHandler) InternalGaps(c *gin.Context) {
	if !h.requireRelay(c) {
		return
	}
	h.Gaps(c)
}

func (h *ReconHandler) Freshness(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"connector_id": c.Query("connector_id"), "status": "fresh"})
}

func (h *ReconHandler) VerifyEvidence(c *gin.Context) {
	body, err := h.Service.VerifyEvidence(c.Request.Context(), c.Query("tenant_id"), c.Query("connector_id"), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, body)
}

func (h *ReconHandler) AskProof(c *gin.Context) {
	body, err := h.Service.GetProof(c.Request.Context(), c.Query("tenant_id"), c.Query("connector_id"), c.Query("payment_id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	}
	q := strings.ToLower(c.Query("question"))
	var answer string
	if strings.Contains(q, "bank") || strings.Contains(q, "money") {
		answer = recon.GetBankMatchAnswer(body)
	} else {
		answer = recon.GetTransactionProofAnswer(body)
	}
	c.JSON(http.StatusOK, gin.H{"answer": answer, "proof": body})
}
