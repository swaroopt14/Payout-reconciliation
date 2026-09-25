package handlers

import (
	"context"
	"net/http"
	"strings"
	"time"

	"zord-outcome-engine/internal/poll"

	"github.com/gin-gonic/gin"
)

type BackfillHandler struct {
	Service   *poll.BackfillService
	Freshness *poll.FreshnessService
}

type createBackfillBody struct {
	TenantID       string `json:"tenant_id"`
	ConnectorID    string `json:"connector_id"`
	WindowFrom     string `json:"window_from"`
	WindowTo       string `json:"window_to"`
	TriggerType    string `json:"trigger_type"`
	Mode           string `json:"mode"`
	OverlapMinutes *int   `json:"overlap_minutes"`
}

func (h *BackfillHandler) CreatePayments(c *gin.Context) {
	h.createAndMaybeRun(c, poll.ResourcePayments, true)
}

func (h *BackfillHandler) CreateSettlements(c *gin.Context) {
	h.createAndMaybeRun(c, poll.ResourceSettlements, true)
}

// CreatePayouts is the timer pull of payouts (D26): same payout-truth intake
// as webhooks, de-duplicated by provider payout_id.
func (h *BackfillHandler) CreatePayouts(c *gin.Context) {
	h.createAndMaybeRun(c, poll.ResourcePayouts, true)
}

func (h *BackfillHandler) createAndMaybeRun(c *gin.Context, resource string, run bool) {
	var body createBackfillBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": err.Error()})
		return
	}
	tenantID, ok := relayTenantMustMatch(c, body.TenantID)
	if !ok {
		return
	}
	body.TenantID = tenantID
	if h.Service == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "backfill not configured"})
		return
	}
	from, err := time.Parse(time.RFC3339, body.WindowFrom)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid window_from"})
		return
	}
	to, err := time.Parse(time.RFC3339, body.WindowTo)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid window_to"})
		return
	}
	mode := body.Mode
	if mode == "" {
		mode = "test"
	}
	overlap := poll.DefaultOverlapMinutes
	if body.OverlapMinutes != nil {
		overlap = *body.OverlapMinutes
	}
	job, err := h.Service.CreateJob(c.Request.Context(), poll.CreateBackfillRequest{
		TenantID:       body.TenantID,
		ConnectorID:    body.ConnectorID,
		Provider:       "razorpay",
		Mode:           mode,
		ResourceType:   resource,
		WindowFrom:     from,
		WindowTo:       to,
		OverlapMinutes: overlap,
		TriggerType:    body.TriggerType,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": err.Error()})
		return
	}

	if run && job.Status != poll.JobRunning && job.Status != poll.JobSucceeded {
		jobID := job.ID
		res := resource
		svc := h.Service
		go func() {
			ctx := context.Background()
			if res == poll.ResourceSettlements {
				_, _ = svc.RunSettlements(ctx, jobID)
			} else if res == poll.ResourcePayouts {
				_, _ = svc.RunPayouts(ctx, jobID)
			} else {
				_, _ = svc.RunPayments(ctx, jobID)
			}
		}()
	}

	c.JSON(http.StatusAccepted, gin.H{
		"job_id":        job.ID,
		"status":        job.Status,
		"resource_type": job.ResourceType,
		"window_from":   job.WindowFrom.UTC().Format(time.RFC3339),
		"window_to":     job.WindowTo.UTC().Format(time.RFC3339),
	})
}

func (h *BackfillHandler) bindJobTenant(c *gin.Context) (poll.BackfillJob, bool) {
	if !authorizeRelay(c.Request) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return poll.BackfillJob{}, false
	}
	ctxTenant := strings.TrimSpace(c.GetHeader(headerRelayTenant))
	if ctxTenant == "" {
		c.JSON(http.StatusForbidden, gin.H{"error": "relay_tenant_required"})
		return poll.BackfillJob{}, false
	}
	job, err := h.Service.GetJob(c.Request.Context(), c.Param("job_id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return poll.BackfillJob{}, false
	}
	if !strings.EqualFold(job.TenantID, ctxTenant) {
		c.JSON(http.StatusForbidden, gin.H{"error": "tenant_mismatch"})
		return poll.BackfillJob{}, false
	}
	return job, true
}

func (h *BackfillHandler) GetJob(c *gin.Context) {
	if _, ok := h.bindJobTenant(c); !ok {
		return
	}
	job, cursor, err := h.Service.GetJobWithCursor(c.Request.Context(), c.Param("job_id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found"})
		return
	}
	c.JSON(http.StatusOK, jobToJSON(job, cursor))
}

func (h *BackfillHandler) ResumeJob(c *gin.Context) {
	if _, ok := h.bindJobTenant(c); !ok {
		return
	}
	jobID := c.Param("job_id")
	svc := h.Service
	go func() {
		_, _ = svc.Resume(context.Background(), jobID)
	}()
	c.JSON(http.StatusAccepted, gin.H{"job_id": jobID, "status": "running"})
}

func (h *BackfillHandler) CancelJob(c *gin.Context) {
	if _, ok := h.bindJobTenant(c); !ok {
		return
	}
	if err := h.Service.Cancel(c.Request.Context(), c.Param("job_id")); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"job_id": c.Param("job_id"), "status": poll.JobCancelled})
}

func (h *BackfillHandler) GetFreshness(c *gin.Context) {
	job, ok := h.bindJobTenant(c)
	if !ok {
		return
	}
	if h.Freshness == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "freshness not configured"})
		return
	}
	report, err := h.Freshness.CompareJob(c.Request.Context(), job)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, report)
}

func jobToJSON(job poll.BackfillJob, cursor poll.BackfillCursor) gin.H {
	return gin.H{
		"job_id":                  job.ID,
		"status":                  job.Status,
		"resource_type":           job.ResourceType,
		"tenant_id":               job.TenantID,
		"connector_id":            job.ConnectorID,
		"provider_mode":           job.ProviderMode,
		"window_from":             job.WindowFrom.UTC().Format(time.RFC3339),
		"window_to":               job.WindowTo.UTC().Format(time.RFC3339),
		"fetched_count":           job.FetchedCount,
		"inserted_count":          job.InsertedCount,
		"updated_count":           job.UpdatedCount,
		"skipped_duplicate_count": job.DuplicateCount,
		"missing_webhook_count":   job.MissingWebhookCount,
		"api_error_count":         job.ErrorCount,
		"last_error_code":         job.LastErrorCode,
		"trace_id":                job.TraceID,
		"cursor": gin.H{
			"page_skip":       cursor.PageSkip,
			"pages_completed": cursor.PagesCompleted,
			"status":          cursor.Status,
		},
	}
}
