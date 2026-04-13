package handler

import (
	"github.com/gin-gonic/gin"
)

// SetupDailystoryRouter sets up routes for dailystory-related APIs
func SetupDailystoryRouter(router *gin.RouterGroup, handler *DailystoryHandler) {
	dailystory := router.Group("/dailystory")
	{
		// Subscription status routes
		status := dailystory.Group("/subscription")
		{
			// Get combined subscription status (old subscriptions + new recurring payments)
			status.GET("/status", handler.GetCombinedStatus)
		}
	}
}
