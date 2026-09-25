package middleware

import (
	"crypto/subtle"
	"log/slog"
	"net/http"
	"os"

	"zord-edge/logger"

	"github.com/gin-gonic/gin"
)

// AdminAuthMiddleware secures internal administration endpoints.
// It checks for a valid X-Zord-ADMIN-KEY header.
func AdminAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		adminKey := c.GetHeader("X-Zord-ADMIN-KEY")
		expectedKey := os.Getenv("INTERNAL_ADMIN_KEY")

		// Security Constraint: Ensure the environment variable is actually set.
		if expectedKey == "" {
			logger.Log.Error("INTERNAL_ADMIN_KEY is not set in environment, blocking all admin access")
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code":  "ADMIN_AUTH_NOT_CONFIGURED",
				"error": "Admin authentication is not configured",
			})
			return
		}

		if adminKey == "" || subtle.ConstantTimeCompare([]byte(adminKey), []byte(expectedKey)) != 1 {
			logger.Log.Warn("unauthorized admin access attempt",
				slog.String("ip", c.ClientIP()),
				slog.String("path", c.Request.URL.Path))
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"code":  "INVALID_ADMIN_KEY",
				"error": "Invalid or missing admin key",
			})
			return
		}

		logger.Log.Info("authorized admin access",
			slog.String("path", c.Request.URL.Path),
			slog.String("ip", c.ClientIP()))
		c.Next()
	}
}
