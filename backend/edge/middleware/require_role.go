package middleware

import (
	"log/slog"
	"net/http"
	"strings"

	"zord-edge/logger"

	"github.com/gin-gonic/gin"
)

// RequireRole allows the request only if Authenticate()/JWTAuthenticate()
// stamped a tenant_id and a "role" that is in roles. It must run after one of
// those middlewares. Tenant API keys carry no role, so they are refused (403):
// role-gated routes need a signed-in user session.
//
// Missing tenant context is 401 (not authenticated); an authenticated
// principal without an allowed role is 403.
func RequireRole(roles ...string) gin.HandlerFunc {
	allowed := make(map[string]struct{}, len(roles))
	for _, r := range roles {
		allowed[strings.ToUpper(strings.TrimSpace(r))] = struct{}{}
	}
	return func(c *gin.Context) {
		if strings.TrimSpace(c.GetString("tenant_id")) == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": gin.H{"code": "UNAUTHORIZED", "message": "tenant context required"},
			})
			return
		}
		role, _ := c.Get("role")
		rs, _ := role.(string)
		if _, ok := allowed[strings.ToUpper(strings.TrimSpace(rs))]; !ok {
			logger.Log.Warn("role check failed",
				slog.String("path", c.FullPath()),
				slog.String("tenant_id", c.GetString("tenant_id")),
				slog.String("role", rs))
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": gin.H{"code": "FORBIDDEN", "message": "insufficient role"},
			})
			return
		}
		c.Next()
	}
}
