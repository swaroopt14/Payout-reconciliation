package routes

import (
	"zord-prompt-layer/agents/askzord"
	"zord-prompt-layer/agents/briefing"
	"zord-prompt-layer/agents/investigate"
	"zord-prompt-layer/handler"
	plmiddleware "zord-prompt-layer/middleware"

	"github.com/gin-gonic/gin"
)

func Register(router *gin.Engine, healthHandler *handler.HealthHandler, queryHandler *handler.QueryHandler, askHandler *askzord.Handler, invHandler *investigate.Handler, briefHandler *briefing.Handler, authCfg plmiddleware.AuthConfig) {
	router.GET("/health", healthHandler.Health)

	protected := router.Group("/")
	protected.Use(plmiddleware.TenantContextMiddleware(authCfg))
	{
		protected.POST("/query", queryHandler.Query)
		if askHandler != nil {
			protected.POST("/v1/ask-zord/finance/query", askHandler.Query)
			protected.GET("/v1/ask-zord/finance/answers/:id", askHandler.GetAudit)
		}
		if invHandler != nil {
			protected.POST("/v1/investigations/batch", invHandler.BatchHTTP)
			protected.POST("/v1/investigations", invHandler.Create)
			protected.GET("/v1/investigations/:id", invHandler.Get)
			protected.POST("/v1/investigations/:id/run", invHandler.Run)
			protected.GET("/v1/investigations/:id/trace", invHandler.Trace)
		}
		if briefHandler != nil {
			protected.POST("/v1/finance/briefing", briefHandler.Create)
		}
	}
}
