package handler

import "github.com/gin-gonic/gin"

// RegisterPostHogConfigRoutes registers all PostHog config-related routes
// Note: POST route is registered separately in main.go (exempt from CORS)
func RegisterPostHogConfigRoutes(router *gin.RouterGroup, handler *PostHogConfigHandler) {
	configs := router.Group("/posthog-configs")
	{
		// POST route is registered in main.go before CORS middleware
		configs.GET("", handler.GetAllPostHogConfigs)
		configs.GET("/by-app", handler.GetPostHogConfigByAppNameAndEnv)
		configs.GET("/:id", handler.GetPostHogConfigByID)
		configs.PUT("/:id", handler.UpdatePostHogConfig)
		configs.DELETE("/:id", handler.DeletePostHogConfig)
	}
}
