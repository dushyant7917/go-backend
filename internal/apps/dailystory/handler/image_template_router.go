package handler

import "github.com/gin-gonic/gin"

// RegisterImageTemplateRoutes registers all image template-related routes
func RegisterImageTemplateRoutes(router *gin.RouterGroup, handler *ImageTemplateHandler) {
	dailystory := router.Group("/dailystory")
	{
		imageTemplates := dailystory.Group("/image-templates")
		{
			imageTemplates.POST("", handler.CreateImageTemplate)
			imageTemplates.POST("/upload-url", handler.GetUploadURL)
			imageTemplates.GET("", handler.GetImageTemplates)
			imageTemplates.GET("/designer-stats", handler.GetDesignerStats)
			imageTemplates.GET("/poster-count-by-count", handler.GetPosterCountByCount)
			imageTemplates.GET("/poster-count-by-date", handler.GetPosterCountByDate)
			imageTemplates.GET("/:id", handler.GetImageTemplate)
			imageTemplates.GET("/:id/view-url", handler.GetImageTemplateViewURL)
			imageTemplates.PUT("/:id", handler.UpdateImageTemplate)
		}
	}
}
