package handlers

import (
	"net/http"

	"zord-outcome-engine/internal/paymenttruth"

	"github.com/gin-gonic/gin"
)

type PaymentHandler struct {
	Store paymenttruth.Store
}

func (h *PaymentHandler) Get(c *gin.Context) {
	tenantID, ok := relayTenantMustMatch(c, c.Query("tenant_id"))
	if !ok {
		return
	}
	if h == nil || h.Store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "payment store not configured"})
		return
	}
	paymentID := c.Param("payment_id")
	connectorID := c.Query("connector_id")
	if paymentID == "" || tenantID == "" || connectorID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "payment_id, tenant_id and connector_id are required"})
		return
	}
	pay, ok, err := h.Store.GetCanonicalPayment(c.Request.Context(), tenantID, connectorID, paymentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "lookup_failed"})
		return
	}
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	}
	events, err := h.Store.ListObservationEvents(c.Request.Context(), tenantID, connectorID, paymentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "lookup_failed"})
		return
	}
	observations := make([]gin.H, 0, len(events))
	for _, ev := range events {
		observations = append(observations, gin.H{
			"source":           ev.Source,
			"provider_status":  ev.ProviderStatus,
			"canonical_status": ev.CanonicalStatus,
			"source_event_id":  ev.SourceEventID,
			"observed_at":      ev.ObservedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"payment_id":         pay.PaymentID,
		"tenant_id":          pay.TenantID,
		"connector_id":       pay.ConnectorID,
		"provider":           pay.Provider,
		"order_id":           pay.OrderID,
		"amount_minor":       pay.AmountMinor,
		"currency":           pay.Currency,
		"method":             pay.Method,
		"provider_status":    pay.ProviderStatus,
		"canonical_status":   pay.CanonicalStatus,
		"captured":           pay.Captured,
		"fee_minor":          pay.FeeMinor,
		"tax_minor":          pay.TaxMinor,
		"sources":            pay.Sources,
		"intent_id":          pay.IntentID,
		"intent_link":        pay.IntentLink,
		"first_observed_at":  pay.FirstObservedAt,
		"last_observed_at":   pay.LastObservedAt,
		"observations":       observations,
	})
}
