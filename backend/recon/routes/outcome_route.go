package routes

import (
	"zord-outcome-engine/handlers"
	"zord-outcome-engine/internal/auth"

	"github.com/gin-gonic/gin"
)

func Routes(router *gin.Engine, h *handlers.Handler) {

	router.GET("/v1/health", handlers.HealthCheck)
	router.GET("/v1/settlement/supported-psps", handlers.GetSupportedPSPs)

	// OUT-01: tenant-scoped settlement routes require verified JWT + tenant match.
	protected := router.Group("/v1")
	protected.Use(auth.GinProtect())
	{
		protected.POST("/settlement/upload", h.SettlementUploadHandler)
		protected.GET("/settlement/jobs/:job_id", h.GetSettlementJobHandler)
		protected.GET("/settlement/errors", handlers.SettlementParseErrors)
		protected.GET("/settlement/observations/batches", h.GetSettlementObservationBatchesHandler)
	}
}

// OutboxRoutes registers the internal relay-facing endpoints that zord-relay
// polls to lease, acknowledge, and nack outcome_outbox events.
// All three handlers are net/http compatible and wrapped via gin.WrapF.
func OutboxRoutes(router *gin.Engine, h *handlers.OutboxHandler) {
	internal := router.Group("/internal/outbox")
	{
		// GET /internal/outbox/lease?limit=500&lease_ttl_seconds=120
		internal.GET("/lease", gin.WrapF(h.Lease))
		// POST /internal/outbox/ack  body: { lease_id, event_ids }
		internal.POST("/ack", gin.WrapF(h.Ack))
		// POST /internal/outbox/nack body: { lease_id, event_ids }
		internal.POST("/nack", gin.WrapF(h.Nack))
	}
}

func ReconRoutes(router *gin.Engine, h *handlers.ReconHandler, imp *handlers.ImportHandler, bank *handlers.BankIngestHandler, fin *handlers.FinancialHandler, closeH *handlers.CloseHandler) {
	if fin != nil {
		router.GET("/v1/cash/instruments", fin.ListInstruments)
	}
	protected := router.Group("/v1")
	protected.Use(auth.GinProtect())
	{
		protected.POST("/bank-statements/upload", imp.OneShotBankUpload)
		protected.GET("/bank-statements/:upload_id", h.GetUpload)
		protected.POST("/merchant/imports", imp.Upload)
		protected.GET("/merchant/imports/:import_id", imp.Get)
		protected.POST("/merchant/imports/:import_id/validate", imp.Validate)
		protected.GET("/merchant/imports/:import_id/rows", imp.Rows)
		protected.POST("/merchant/imports/:import_id/commit", imp.Commit)
		protected.POST("/merchant/imports/:import_id/cancel", imp.Cancel)
		protected.GET("/merchant/transactions/:payment_id/proof", h.GetProof)
		protected.GET("/merchant/transactions", h.Transactions)
		protected.GET("/merchant/reconciliation/summary", h.Summary)
		protected.GET("/merchant/reconciliation/gaps", h.Gaps)
		protected.GET("/merchant/settlements/:settlement_id/breakdown", h.Breakdown)
		protected.GET("/merchant/freshness", h.Freshness)
		protected.GET("/merchant/evidence/:id/verify", h.VerifyEvidence)
		protected.GET("/merchant/ask/proof", h.AskProof)
		if fin != nil {
			protected.GET("/reconciliation/payments/:payment_id", fin.GetPayment)
			protected.GET("/reconciliation/payments/:payment_id/evidence", fin.GetEvidence)
			protected.GET("/reconciliation/payments/:payment_id/timeline", fin.GetPaymentTimeline)
			protected.GET("/reconciliation/payouts/:payout_id", fin.GetPayout)
			protected.GET("/reconciliation/payouts/:payout_id/evidence", fin.GetPayoutEvidence)
			protected.GET("/reconciliation/payouts/:payout_id/timeline", fin.GetPayoutTimeline)
			protected.GET("/reconciliation/sla-policy", fin.SLAPolicy)
			protected.GET("/reconciliation/results", fin.ListResults)
			protected.GET("/reconciliation/evaluation", fin.GetEvaluation)
			protected.GET("/reconciliation/summary", fin.GetFinanceSummary)
			protected.GET("/reconciliation/cash-position", fin.GetCashPosition)
			protected.GET("/reconciliation/cash-schedule", fin.GetCashSchedule)
			protected.GET("/reconciliation/tax-breakdown/:payment_id", fin.GetTaxBreakdown)
			protected.GET("/reconciliation/ledger", fin.GetLedger)
			protected.GET("/reconciliation/refunds", fin.ListRefunds)
			protected.GET("/reconciliation/exceptions", fin.ListExceptions)
			protected.GET("/reconciliation/exceptions/:id", fin.GetException)
			protected.POST("/reconciliation/run", fin.Run)
			protected.GET("/reconciliation/runs/:id", fin.GetRun)
			protected.POST("/reconciliation/batches/:batch_id/reconcile", fin.ReconcileBatch)
			protected.GET("/reconciliation/batches/:batch_id", fin.GetBatch)
			protected.POST("/reconciliation/investigations", fin.CreateInvestigation)
			protected.GET("/reconciliation/investigations", fin.ListInvestigations)
			protected.GET("/reconciliation/investigations/:id", fin.GetInvestigation)
			protected.GET("/reconciliation/settlements", fin.SearchSettlements)
			protected.GET("/reconciliation/bank-transactions", fin.SearchBank)
			protected.GET("/reconciliation/bank-transactions/:id", fin.GetBank)
		}
		if closeH != nil {
			protected.POST("/finance-close/run", closeH.Run)
			protected.GET("/finance-close/:id", closeH.Get)
			protected.GET("/finance-close/:id/accuracy", closeH.Accuracy)
		}
	}
	internal := router.Group("/internal")
	{
		internal.POST("/recon/run", h.Run)
		internal.GET("/recon/gaps", h.InternalGaps)
		if bank != nil {
			internal.POST("/bank-statements/ingest", bank.Ingest)
		}
		if fin != nil {
			internal.POST("/reconciliation/run", fin.InternalRun)
		}
	}
}

func ObservationRoutes(router *gin.Engine, h *handlers.ObservationHandler) {
	internal := router.Group("/internal")
	{
		internal.POST("/observations/provider", h.Ingest)
	}
}

func PaymentRoutes(router *gin.Engine, h *handlers.PaymentHandler) {
	internal := router.Group("/internal")
	{
		internal.GET("/payments/:payment_id", h.Get)
	}
}

func PayoutRoutes(router *gin.Engine, h *handlers.PayoutHandler) {
	internal := router.Group("/internal")
	{
		internal.GET("/payouts/:payout_id", h.Get)
	}
}

func BackfillRoutes(router *gin.Engine, h *handlers.BackfillHandler) {
	internal := router.Group("/internal")
	{
		internal.POST("/backfill/payments", h.CreatePayments)
		internal.POST("/backfill/settlements", h.CreateSettlements)
		internal.GET("/backfill/jobs/:job_id", h.GetJob)
		internal.POST("/backfill/jobs/:job_id/resume", h.ResumeJob)
		internal.POST("/backfill/jobs/:job_id/cancel", h.CancelJob)
		internal.GET("/freshness/:job_id", h.GetFreshness)
	}
}
