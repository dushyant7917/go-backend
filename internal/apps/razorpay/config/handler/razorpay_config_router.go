package handler

import "github.com/gin-gonic/gin"

// RegisterRazorpayConfigRoutes registers all razorpay config-related routes
// Note: POST route is registered separately in main.go (exempt from CORS)
func RegisterRazorpayConfigRoutes(router *gin.RouterGroup, handler *RazorpayConfigHandler) {
	configs := router.Group("/razorpay-configs")
	{
		// POST route is registered in main.go before CORS middleware
		configs.GET("", handler.GetAllRazorpayConfigs)
		configs.GET("/by-app", handler.GetRazorpayConfigByAppNameAndEnv)
		configs.GET("/:id", handler.GetRazorpayConfigByID)
		configs.PUT("/:id", handler.UpdateRazorpayConfig)
		configs.DELETE("/:id", handler.DeleteRazorpayConfig)
	}
}
