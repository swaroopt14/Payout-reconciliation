package routes

import (
	"github.com/gin-gonic/gin"

	"zord-edge/config"
	"zord-edge/handler"
	"zord-edge/middleware"
	"zord-edge/validator"
)

func Routes(router *gin.Engine, h *handler.Handler) {
	handler.RegisterPublicAuthRoutes(router)

	public := router.Group("/v1")
	{
		public.GET("/health", handler.HealthCheck)
	}

	// Admin routes (Secured by AdminAuthMiddleware)
	admin := router.Group("/v1/admin")
	admin.Use(middleware.AdminAuthMiddleware())
	{
		admin.POST("/tenantReg", handler.Tenant_Registry)
		admin.GET("/tenants", handler.ListTenants)
		admin.GET("/tenants/:tenant_id", handler.GetTenantByID)
	}

	// Webhook routes
	webhooks := router.Group("/v1/raw/envelopes")
	webhooks.Use(
		middleware.VerifyWebhookSignature(),
		middleware.TransportValidation(),
		middleware.MaxRequestBodyBytes(middleware.MaxIngestBodyBytes),
	)
	{
		webhooks.POST("/webhooks/:provider/:connectorID", h.WebhookHandler)
	}

	if err := validator.InitSchemaValidator(); err != nil {
		panic("Failed to initialize schema validator: " + err.Error())
	}

	protected := router.Group("/v1")
	protected.Use(
		middleware.Authenticate(),
		//middleware.TraceMiddleware(),
		middleware.TransportValidation(),
	)

	// JSON ingest (needs JSON validation + idempotency header)
	protected.POST(
		"/ingest",
		middleware.MaxRequestBodyBytes(middleware.MaxIngestBodyBytes),
		middleware.ValidateIntentRequest(),
		middleware.GetIdempotencyKey(),
		h.IntentHandler,
	)

	// Bulk ingest (multipart, so no JSON validation, no header idempotency)
	protected.POST(
		"/bulk-ingest",
		middleware.MaxRequestBodyBytes(config.MaxBulkUploadBytes),
		h.BulkIntentHandler,
	)
	protected.POST(
		"/bank-statements",
		middleware.MaxRequestBodyBytes(config.MaxBulkUploadBytes),
		h.PostBankStatement,
	)
	protected.GET("/bank-statements/:ingest_id", h.GetBankStatement)

	// Razorpay connector routes (authenticated, tenant-scoped)
	connectorHandler := handler.NewConnectorHandler()
	handler.RegisterConnectorRoutes(protected, connectorHandler)

	protected.GET("/webhooks/razorpay/receipt/:receiptID", h.HandleRazorpayWebhookStatus)
	protected.GET("/webhooks/razorpay/receipts/:connectorID", h.HandleRazorpayWebhookList)

	// Razorpay webhook ingest stays public (HMAC-verified).
	razorpayWebhooks := router.Group("/v1/webhooks/razorpay")
	{
		razorpayWebhooks.POST("/:connectorID", h.HandleRazorpayWebhook)
	}

	jwtProtected := router.Group("/v1")
	jwtProtected.Use(
		middleware.JWTAuthenticate(),
		middleware.SessionActivityMiddleware(),
	)
	{
		handler.RegisterSessionRoutes(jwtProtected)
	}
}
