package handler

import "github.com/gin-gonic/gin"

// RegisterR2ConfigRoutes registers all R2 config-related routes
func RegisterR2ConfigRoutes(router *gin.RouterGroup, handler *R2ConfigHandler) {
	configs := router.Group("/r2-configs")
	{
		configs.POST("", handler.CreateR2Config)
		configs.GET("", handler.GetR2Configs)
		configs.GET("/:id", handler.GetR2Config)
		configs.PUT("/:id", handler.UpdateR2Config)
		configs.DELETE("/:id", handler.DeleteR2Config)
	}
}
