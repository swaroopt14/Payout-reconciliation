package handlers

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"

	"zord-outcome-engine/internal/bankingest"

	"github.com/gin-gonic/gin"
)

type BankIngestHandler struct {
	Service *bankingest.Service
}

type bankIngestBody struct {
	TenantID    string `json:"tenant_id"`
	ConnectorID string `json:"connector_id"`
	AccountID   string `json:"account_id"`
	FileName    string `json:"filename"`
	Profile     string `json:"profile"`
	Currency    string `json:"currency"`
	AmountUnit  string `json:"amount_unit"`
	Timezone    string `json:"timezone"`
	Payload     string `json:"payload"`
	IngestID    string `json:"ingest_id"`
	FileSHA256  string `json:"file_sha256"`
	StorageURI  string `json:"storage_uri"`
}

func (h *BankIngestHandler) Ingest(c *gin.Context) {
	if h == nil || h.Service == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "bank ingest not configured"})
		return
	}
	req, err := parseBankIngest(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	tenantID, ok := relayTenantMustMatch(c, req.TenantID)
	if !ok {
		return
	}
	req.TenantID = tenantID
	res, err := h.Service.IngestAndMatch(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "ingest_failed"})
		return
	}
	states := map[string]int{}
	for _, d := range res.Decisions {
		states[d.State]++
	}
	c.JSON(http.StatusAccepted, gin.H{
		"import_id":     res.Import.ID,
		"status":        res.Import.Status,
		"inserted_rows": res.Import.InsertedRows,
		"match_states":  states,
		"proof_count":   res.ProofCount,
	})
}

func parseBankIngest(c *gin.Context) (bankingest.IngestRequest, error) {
	ct := c.ContentType()
	if strings.HasPrefix(ct, "multipart/") {
		in, err := readUpload(c)
		if err != nil {
			return bankingest.IngestRequest{}, err
		}
		return bankingest.IngestRequest{
			TenantID:    in.TenantID,
			ConnectorID: in.ConnectorID,
			AccountID:   in.AccountID,
			FileName:    in.FileName,
			Profile:     c.Query("profile"),
			Currency:    c.Query("currency"),
			AmountUnit:  c.DefaultQuery("amount_unit", "paise"),
			Timezone:    c.DefaultQuery("timezone", "Asia/Kolkata"),
			Payload:     in.Payload,
		}, nil
	}
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return bankingest.IngestRequest{}, err
	}
	var body bankIngestBody
	if err := json.Unmarshal(raw, &body); err != nil {
		return bankingest.IngestRequest{}, err
	}
	name := body.FileName
	if name == "" {
		name = "bank.csv"
	}
	return bankingest.IngestRequest{
		TenantID:    body.TenantID,
		ConnectorID: body.ConnectorID,
		AccountID:   body.AccountID,
		FileName:    name,
		Profile:     body.Profile,
		Currency:    body.Currency,
		AmountUnit:  body.AmountUnit,
		Timezone:    body.Timezone,
		Payload:     []byte(body.Payload),
	}, nil
}

var bankIngestService *bankingest.Service

func SetBankIngestService(s *bankingest.Service) {
	bankIngestService = s
}

func HandleBankStatementReceived(msg []byte) error {
	if bankIngestService == nil {
		return nil
	}
	var body bankIngestBody
	if err := json.Unmarshal(msg, &body); err != nil {
		return nil
	}
	if len(body.Payload) == 0 {
		log.Printf("bank.statement.received ingest_id=%s storage_uri=%s (no payload; skip parse)", body.IngestID, body.StorageURI)
		return nil
	}
	_, err := bankIngestService.IngestAndMatch(context.Background(), bankingest.IngestRequest{
		TenantID:    body.TenantID,
		ConnectorID: body.ConnectorID,
		AccountID:   body.AccountID,
		FileName:    body.FileName,
		Profile:     body.Profile,
		Currency:    body.Currency,
		AmountUnit:  body.AmountUnit,
		Timezone:    body.Timezone,
		Payload:     []byte(body.Payload),
	})
	return err
}
