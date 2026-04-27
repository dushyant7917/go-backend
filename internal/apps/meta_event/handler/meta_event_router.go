package handler

import (
	"github.com/gin-gonic/gin"
)

// RegisterMetaEventRoutes registers routes for meta event operations
func RegisterMetaEventRoutes(router *gin.RouterGroup, handler *MetaEventHandler) {
	metaEvents := router.Group("/meta-events")
	{
		metaEvents.PUT("/:id", handler.UpdateMetaEvent)
	}
}
