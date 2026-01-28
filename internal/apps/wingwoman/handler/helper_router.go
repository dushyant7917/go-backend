package handler

import "github.com/gin-gonic/gin"

// RegisterWingWomanRoutes registers all WingWoman app routes
func RegisterWingWomanRoutes(router *gin.RouterGroup, handler *HelperHandler) {
	wingwoman := router.Group("/wingwoman")
	{
		wingwoman.GET("/helpers", handler.ListHelpers)
	}
}
