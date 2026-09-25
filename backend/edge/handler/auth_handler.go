package handler

import (
	"errors"
	"net/http"
	"time"

	"zord-edge/auth/workspacecode"
	"zord-edge/db"
	"zord-edge/middleware"
	"zord-edge/services"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RegisterPublicAuthRoutes attaches /v1/auth/* used by zord-console (signup, login,
// refresh, logout, and Bearer /me). Call once from route setup (e.g. routes/intent_route.go).
//
// credentialLimits (optional) run before signup/login/refresh — the
// unauthenticated credential endpoints — e.g. the per-IP rate limiter.
func RegisterPublicAuthRoutes(router *gin.Engine, credentialLimits ...gin.HandlerFunc) {
	pub := router.Group("/v1/auth")
	{
		pub.POST("/signup", append(append([]gin.HandlerFunc{}, credentialLimits...), Signup)...)
		pub.POST("/login", append(append([]gin.HandlerFunc{}, credentialLimits...), Login)...)
		pub.POST("/refresh", append(append([]gin.HandlerFunc{}, credentialLimits...), Refresh)...)
		pub.POST("/logout", Logout)
	}
	me := router.Group("/v1/auth")
	me.Use(middleware.Authenticate())
	{
		me.GET("/me", Me)
		me.GET("/principal", Principal)
	}
}

// Request shapes — kept compatible with the existing console route handlers.

type signupRequest struct {
	TenantName string `json:"tenant_name" binding:"required"`
	Name       string `json:"name" binding:"required"`
	Email      string `json:"email" binding:"required,email"`
	Password   string `json:"password" binding:"required,min=8"`
}

type loginRequest struct {
	// WorkspaceID / LoginSurface are accepted but unused — the console sends them today.
	WorkspaceID  string `json:"workspace_id"`
	LoginSurface string `json:"login_surface"`
	Email        string `json:"email" binding:"required,email"`
	Password     string `json:"password" binding:"required"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

// Response shapes — match the BackendAuthEnvelope expected by the console.

type userResponse struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	Role          string `json:"role"`
	Name          string `json:"name"`
	TenantID      string `json:"tenant_id"`
	TenantName    string `json:"tenant_name"`
	WorkspaceCode string `json:"workspace_code"`
	Status        string `json:"status"`
	MFAEnabled    bool   `json:"mfa_enabled"`
	LastLoginAt   string `json:"last_login_at,omitempty"`
}

type sessionResponse struct {
	SessionID         string `json:"session_id"`
	TenantID          string `json:"tenant_id"`
	WorkspaceCode     string `json:"workspace_code"`
	Role              string `json:"role"`
	AccessExpiresAt   string `json:"access_expires_at"`
	IdleExpiresAt     string `json:"idle_expires_at"`
	AbsoluteExpiresAt string `json:"absolute_expires_at"`
}

type authEnvelope struct {
	User              userResponse    `json:"user"`
	Session           sessionResponse `json:"session"`
	RequiresMFA       bool            `json:"requires_mfa"`
	AccessToken       string          `json:"access_token,omitempty"`
	RefreshToken      string          `json:"refresh_token,omitempty"`
	AccessExpiresAt   string          `json:"access_expires_at"`
	IdleExpiresAt     string          `json:"idle_expires_at,omitempty"`
	AbsoluteExpiresAt string          `json:"absolute_expires_at,omitempty"`
	// APIKey carries the freshly-issued tenant API key (prefix.secret) on
	// signup only. zord-edge only ever stores the hash; this is the single
	// chance for the console to capture the secret. Empty on login/refresh.
	APIKey string `json:"api_key,omitempty"`
}

func toEnvelope(bundle *services.AuthBundle) authEnvelope {
	accessExp := bundle.AccessExpiresAt.UTC().Format(time.RFC3339)
	idleExp := bundle.IdleExpiresAt.UTC().Format(time.RFC3339)
	absExp := bundle.AbsoluteExpiresAt.UTC().Format(time.RFC3339)
	ws := workspacecode.FromKeyPrefix(bundle.Tenant.KeyPrefix)
	return authEnvelope{
		User: userResponse{
			ID:            bundle.User.UserID.String(),
			Email:         bundle.User.Email,
			Role:          bundle.User.Role,
			Name:          bundle.User.Name,
			TenantID:      bundle.User.TenantID.String(),
			TenantName:    bundle.Tenant.TenantName,
			WorkspaceCode: ws,
			Status:        bundle.User.Status,
			MFAEnabled:    false,
		},
		Session: sessionResponse{
			SessionID:         uuid.NewString(), // fallback if none, but ideally matches refresh/access claim
			TenantID:          bundle.Tenant.TenantID.String(),
			WorkspaceCode:     ws,
			Role:              bundle.User.Role,
			AccessExpiresAt:   accessExp,
			IdleExpiresAt:     idleExp,
			AbsoluteExpiresAt: absExp,
		},
		RequiresMFA:       false,
		AccessToken:       bundle.AccessToken,
		RefreshToken:      bundle.RefreshToken,
		AccessExpiresAt:   accessExp,
		IdleExpiresAt:     idleExp,
		AbsoluteExpiresAt: absExp,
		APIKey:            bundle.Tenant.APIKey,
	}
}

func mapAuthError(err error) (int, string, string) {
	switch {
	case errors.Is(err, services.ErrInvalidCredentials):
		return http.StatusUnauthorized, "INVALID_CREDENTIALS", "Invalid email or password"
	case errors.Is(err, services.ErrAccountLocked):
		return http.StatusLocked, "ACCOUNT_LOCKED", "Account temporarily locked. Try again later."
	case errors.Is(err, services.ErrAccountDisabled):
		return http.StatusForbidden, "ACCOUNT_DISABLED", "This account is disabled."
	case errors.Is(err, services.ErrEmailTaken):
		return http.StatusConflict, "EMAIL_TAKEN", "An account with this email already exists."
	case errors.Is(err, services.ErrTenantNameTaken):
		return http.StatusConflict, "TENANT_NAME_TAKEN", "Tenant name is already in use."
	default:
		return http.StatusInternalServerError, "AUTH_INTERNAL_ERROR", err.Error()
	}
}

func writeAuthError(c *gin.Context, err error) {
	status, code, msg := mapAuthError(err)
	c.JSON(status, gin.H{"code": code, "message": msg})
}

func Signup(c *gin.Context) {
	var req signupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "INVALID_SIGNUP_REQUEST", "message": err.Error()})
		return
	}
	bundle, err := services.SignupNewTenant(
		c.Request.Context(), db.DB,
		req.TenantName, req.Name, req.Email, req.Password,
		c.ClientIP(), c.Request.UserAgent(),
	)
	if err != nil {
		writeAuthError(c, err)
		return
	}
	c.JSON(http.StatusCreated, toEnvelope(bundle))
}

func Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "INVALID_AUTH_REQUEST", "message": err.Error()})
		return
	}
	bundle, err := services.LoginUser(
		c.Request.Context(), db.DB,
		req.Email, req.Password,
		c.ClientIP(), c.Request.UserAgent(),
	)
	if err != nil {
		writeAuthError(c, err)
		return
	}
	c.JSON(http.StatusOK, toEnvelope(bundle))
}

func Refresh(c *gin.Context) {
	var req refreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "REFRESH_TOKEN_REQUIRED", "message": err.Error()})
		return
	}
	bundle, err := services.RefreshSession(
		c.Request.Context(), db.DB,
		req.RefreshToken,
		c.ClientIP(), c.Request.UserAgent(),
	)
	if err != nil {
		writeAuthError(c, err)
		return
	}
	c.JSON(http.StatusOK, toEnvelope(bundle))
}

func Logout(c *gin.Context) {
	var req refreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "REFRESH_TOKEN_REQUIRED", "message": err.Error()})
		return
	}
	_ = services.RevokeRefreshToken(c.Request.Context(), db.DB, req.RefreshToken, c.ClientIP(), c.Request.UserAgent())
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// Principal returns the authenticated tenant for either a JWT (user session)
// or a tenant API key. Used by zord-console BFF to compare session vs pasted key
// without requiring user_id (unlike GET /v1/auth/me).
func Principal(c *gin.Context) {
	tidRaw, ok := c.Get("tenant_id")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"code": "UNAUTHORIZED", "message": "missing tenant context"})
		return
	}
	tenantID, ok := tidRaw.(uuid.UUID)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"code": "INTERNAL", "message": "invalid tenant context"})
		return
	}
	tenantName, _ := c.Get("tenant_name")
	tn, _ := tenantName.(string)

	if uidRaw, ok := c.Get("user_id"); ok {
		if uid, ok := uidRaw.(uuid.UUID); ok {
			email, _ := c.Get("email")
			role, _ := c.Get("role")
			es, _ := email.(string)
			rs, _ := role.(string)
			c.JSON(http.StatusOK, gin.H{
				"principal":   "user",
				"tenant_id":   tenantID.String(),
				"tenant_name": tn,
				"user_id":     uid.String(),
				"email":       es,
				"role":        rs,
			})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"principal":   "api_key",
		"tenant_id":   tenantID.String(),
		"tenant_name": tn,
	})
}

func Me(c *gin.Context) {
	val, ok := c.Get("user_id")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"code": "INVALID_SESSION", "message": "Session expired"})
		return
	}
	userID, ok := val.(uuid.UUID)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"code": "INVALID_SESSION", "message": "Session expired"})
		return
	}
	user, tenant, err := services.GetUserByID(c.Request.Context(), db.DB, userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": "USER_NOT_FOUND", "message": "User not found"})
		return
	}
	ws := workspacecode.FromKeyPrefix(tenant.KeyPrefix)
	c.JSON(http.StatusOK, gin.H{
		"user": userResponse{
			ID:            user.UserID.String(),
			Email:         user.Email,
			Role:          user.Role,
			Name:          user.Name,
			TenantID:      user.TenantID.String(),
			TenantName:    tenant.TenantName,
			WorkspaceCode: ws,
			Status:        user.Status,
			MFAEnabled:    false,
		},
		"session": sessionResponse{
			SessionID:     uuid.NewString(),
			TenantID:      tenant.TenantID.String(),
			WorkspaceCode: ws,
			Role:          user.Role,
		},
	})
}
