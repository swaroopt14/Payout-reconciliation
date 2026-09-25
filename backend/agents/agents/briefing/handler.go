package briefing

import (
	"net/http"
	"strings"

	plmiddleware "zord-prompt-layer/middleware"
	"zord-prompt-layer/tools"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	Client      *tools.OutcomeClient
	ConnectorID string
	Rewrite     Rewriter
}

func NewHandler(c *tools.OutcomeClient, connectorID string, rewrite Rewriter) *Handler {
	return &Handler{Client: c, ConnectorID: connectorID, Rewrite: rewrite}
}

func (h *Handler) Create(c *gin.Context) {
	ctxTenant, ok := c.Get(plmiddleware.TenantIDContextKey)
	if !ok {
		plmiddleware.SafeError(c, http.StatusUnauthorized, "unauthorized", "Missing tenant context.")
		return
	}
	tenantID, _ := ctxTenant.(string)
	var body struct {
		TenantID    string `json:"tenant_id"`
		ConnectorID string `json:"connector_id"`
		CloseRunID  string `json:"close_run_id"`
		HoldEnabled bool   `json:"hold_enabled"`
	}
	_ = c.ShouldBindJSON(&body)
	if body.TenantID == "" {
		body.TenantID = tenantID
	}
	if body.ConnectorID == "" {
		body.ConnectorID = h.ConnectorID
	}
	// Query override: hold_enabled=true|1
	if q := strings.TrimSpace(c.Query("hold_enabled")); q != "" {
		body.HoldEnabled = strings.EqualFold(q, "true") || q == "1"
	}

	in := OpsInputs{Close: Report{}}
	if h.Client != nil {
		sum, _ := h.Client.GetReconSummary(body.TenantID, body.ConnectorID)
		if !softMissing(sum) {
			in.Close.Records = asInt(sum["scored_count"])
			in.Close.Matched = asInt(sum["matched_count"])
			in.Close.UnresolvedExposureMinor = asInt64(sum["exposure_minor"])
			if in.Close.Records > 0 {
				in.Close.MatchRate = float64(in.Close.Matched) / float64(in.Close.Records)
				in.Close.Exceptions = in.Close.Records - in.Close.Matched
			}
		}

		sch, _ := h.Client.GetCashSchedule(body.TenantID, body.ConnectorID)
		applyCashSchedule(&in, sch)

		rg, _ := h.Client.GetMarketplaceRefundGraphExceptions(body.TenantID, body.ConnectorID)
		applyRefundGraph(&in, rg)

		vf, _ := h.Client.GetMarketplaceVelocityFlags(body.TenantID, body.ConnectorID, body.HoldEnabled)
		applyVelocity(&in, vf, body.HoldEnabled)
	}

	c.JSON(http.StatusOK, WriteOps(in, h.Rewrite))
}

func softMissing(body map[string]any) bool {
	if body == nil {
		return true
	}
	err, _ := body["error"].(string)
	return err == "not_found" || err == "none" || err == "source_not_in_this_phase"
}

func applyCashSchedule(in *OpsInputs, sch map[string]any) {
	if softMissing(sch) {
		return
	}
	in.ScheduleAvailable = true
	if kind, _ := sch["kind"].(string); kind != "" {
		in.CashScheduleKind = kind
	}
	days, _ := sch["days"].([]any)
	if len(days) == 0 {
		return
	}
	day, ok := days[0].(map[string]any)
	if !ok {
		return
	}
	if v, ok := day["expected_credit_minor"]; ok {
		in.NextDayCreditMinor = asInt64(v)
	}
	if v, ok := day["expected_debit_minor"]; ok {
		in.NextDayDebitMinor = asInt64(v)
	}
}

func applyRefundGraph(in *OpsInputs, body map[string]any) {
	if softMissing(body) {
		return
	}
	in.RefundGraphAvailable = true
	signals, _ := body["signals"].([]any)
	in.RefundGraphCount = len(signals)
	for _, raw := range signals {
		sig, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		reason, _ := sig["reason"].(string)
		if reason == "" {
			continue
		}
		in.RefundGraphReasons = append(in.RefundGraphReasons, reason)
		if reason == ReasonRefundWithoutReverse {
			in.HasRefundWithoutReverse = true
		}
	}
}

func applyVelocity(in *OpsInputs, body map[string]any, holdEnabled bool) {
	if softMissing(body) {
		return
	}
	in.VelocityAvailable = true
	in.HoldEnabledOnFetch = holdEnabled
	flags, _ := body["flags"].([]any)
	in.VelocityFlagCount = len(flags)
	if !holdEnabled {
		// HoldRecommended only when HoldEnabled; never count counsel holds otherwise.
		in.HoldRecommendedCount = 0
		return
	}
	for _, raw := range flags {
		f, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if hr, _ := f["hold_recommended"].(bool); hr {
			in.HoldRecommendedCount++
		}
	}
}

func asInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	default:
		return 0
	}
}

func asInt64(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int:
		return int64(n)
	case int64:
		return n
	default:
		return 0
	}
}
