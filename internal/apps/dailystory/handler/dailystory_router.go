package handler

import (
	"github.com/gin-gonic/gin"
)

// SetupDailystoryRouter sets up routes for dailystory-related APIs
func SetupDailystoryRouter(router *gin.RouterGroup, handler *DailystoryHandler) {
	dailystory := router.Group("/dailystory")
	{
		// Get combined status (subscriptions, recurring payments, and pending meta events)
		dailystory.GET("/status", handler.GetCombinedStatus)
		// Deprecated: old endpoint for backward compatibility
		dailystory.GET("/subscription/status", handler.GetCombinedStatus)
	}
}
