package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

func RegisterEcosystemRoutes(
	v1 *gin.RouterGroup,
	h *handler.EcosystemHandler,
	auditLog middleware.AuditLogMiddleware,
	panelRateLimiter *middleware.PanelRateLimiter,
) {
	if h == nil {
		return
	}
	ecosystem := v1.Group("/ecosystem")
	if panelRateLimiter != nil {
		ecosystem.Use(panelRateLimiter.PublicIP())
	}
	ecosystem.Use(gin.HandlerFunc(auditLog))
	{
		ecosystem.GET("/me", h.Me)
		ecosystem.GET("/groups", h.Groups)
		ecosystem.GET("/models", h.Models)
		ecosystem.GET("/keys", h.Keys)
	}
}
