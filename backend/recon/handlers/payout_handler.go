package handlers

import (
	"net/http"

	"zord-outcome-engine/internal/payouttruth"

	"github.com/gin-gonic/gin"
)

type PayoutHandler struct {
	Store payouttruth.Store
}

func (h *PayoutHandler) Get(c *gin.Context) {
	tenantID, ok := relayTenantMustMatch(c, c.Query("tenant_id"))
	if !ok {
		return
	}
	if h == nil || h.Store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "payout store not configured"})
		return
	}
	payoutID := c.Param("payout_id")
	connectorID := c.Query("connector_id")
	if payoutID == "" || tenantID == "" || connectorID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "payout_id, tenant_id and connector_id are required"})
		return
	}
	pay, ok, err := h.Store.GetCanonicalPayout(c.Request.Context(), tenantID, connectorID, payoutID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "lookup_failed"})
		return
	}
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	}
	events, err := h.Store.ListPayoutObservationEvents(c.Request.Context(), tenantID, connectorID, payoutID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "lookup_failed"})
		return
	}
	obs := make([]gin.H, 0, len(events))
	for _, ev := range events {
		obs = append(obs, gin.H{
			"source": ev.Source, "provider_status": ev.ProviderStatus,
			"source_event_id": ev.SourceEventID, "observed_at": ev.ObservedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"payout_id": pay.PayoutID, "status": pay.ProviderStatus, "provider_status": pay.ProviderStatus,
		"amount_minor": pay.AmountMinor, "currency": pay.Currency, "utr": pay.UTR, "mode": pay.Mode,
		"status_reason": pay.StatusReason, "observations": obs,
	})
}
