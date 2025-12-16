package handler

import "github.com/gin-gonic/gin"

// RegisterCrushRoutes registers all crush-related routes
func RegisterCrushRoutes(router *gin.RouterGroup, handler *CrushHandler) {
	crushes := router.Group("/crushes")
	{
		crushes.POST("", handler.CreateCrush)
		crushes.GET("/all", handler.ListAllCrushes)
		crushes.GET("/:id", handler.GetCrush)
		crushes.PUT("/:id", handler.UpdateCrush)
		crushes.GET("", handler.ListCrushes)
		crushes.GET("/on-user", handler.ListCrushesOnUser)
	}
}
