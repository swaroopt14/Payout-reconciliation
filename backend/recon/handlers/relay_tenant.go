package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	headerRelayTenant = "X-Relay-Tenant-ID"
	defaultPageSize   = 50
	maxPageSize       = 200
)

func relayTenantMustMatch(c *gin.Context, claimed string) (string, bool) {
	if !authorizeRelay(c.Request) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return "", false
	}
	ctxTenant := strings.TrimSpace(c.GetHeader(headerRelayTenant))
	if ctxTenant == "" {
		c.JSON(http.StatusForbidden, gin.H{"error": "relay_tenant_required"})
		return "", false
	}
	claimed = strings.TrimSpace(claimed)
	if claimed != "" && !strings.EqualFold(claimed, ctxTenant) {
		c.JSON(http.StatusForbidden, gin.H{"error": "tenant_mismatch"})
		return "", false
	}
	if claimed != "" {
		return claimed, true
	}
	return ctxTenant, true
}

func pageWindow(c *gin.Context, total int) (offset, limit, page, pageSize int) {
	page, _ = strconv.Atoi(strings.TrimSpace(c.DefaultQuery("page", "1")))
	if page < 1 {
		page = 1
	}
	pageSize, _ = strconv.Atoi(strings.TrimSpace(c.DefaultQuery("page_size", strconv.Itoa(defaultPageSize))))
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}
	offset = (page - 1) * pageSize
	if offset > total {
		offset = total
	}
	limit = pageSize
	if offset+limit > total {
		limit = total - offset
	}
	if limit < 0 {
		limit = 0
	}
	return offset, limit, page, pageSize
}
