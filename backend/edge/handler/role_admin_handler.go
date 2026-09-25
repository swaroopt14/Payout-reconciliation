package handler

import (
	"errors"
	"net/http"

	"zord-edge/db"
	"zord-edge/services"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// roleStoreFactory is overridable in tests; production uses auth_users via db.DB.
var roleStoreFactory = func() services.RoleStore { return services.SQLRoleStore{DB: db.DB} }

type grantRoleRequest struct {
	TenantID string `json:"tenant_id" binding:"required"`
	UserID   string `json:"user_id" binding:"required"`
	Role     string `json:"role" binding:"required"`
}

// GrantRole is POST /v1/admin/roles/grant — the ONLY path that can give a user
// PLATFORM_ADMIN, PAYOUT_APPROVER or CONNECTOR_ADMIN (D39). It is mounted behind
// AdminAuthMiddleware (X-Zord-ADMIN-KEY == INTERNAL_ADMIN_KEY, fail closed).
// The role lands in the user's next access token (login / refresh).
func GrantRole(c *gin.Context) {
	var req grantRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "INVALID_ROLE_GRANT", "message": err.Error()})
		return
	}
	tenantID, err := uuid.Parse(req.TenantID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "INVALID_ROLE_GRANT", "message": "invalid tenant_id"})
		return
	}
	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "INVALID_ROLE_GRANT", "message": "invalid user_id"})
		return
	}
	grant, err := services.GrantUserRole(c.Request.Context(), roleStoreFactory(), tenantID, userID, req.Role, c.ClientIP(), c.Request.UserAgent())
	switch {
	case errors.Is(err, services.ErrRoleNotGrantable):
		c.JSON(http.StatusBadRequest, gin.H{"code": "ROLE_NOT_GRANTABLE", "message": err.Error()})
		return
	case errors.Is(err, services.ErrRoleUserNotFound):
		c.JSON(http.StatusNotFound, gin.H{"code": "USER_NOT_FOUND", "message": err.Error()})
		return
	case err != nil:
		c.JSON(http.StatusInternalServerError, gin.H{"code": "ROLE_GRANT_FAILED", "message": "role grant failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"tenant_id": grant.TenantID.String(),
		"user_id":   grant.UserID.String(),
		"role":      grant.Role,
		"effective": "next_login_or_refresh",
	})
}
