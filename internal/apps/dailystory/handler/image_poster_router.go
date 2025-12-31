package handler

import "github.com/gin-gonic/gin"

// RegisterImagePosterRoutes registers routes for image poster operations
func RegisterImagePosterRoutes(router *gin.RouterGroup, handler *ImagePosterHandler) {
	posters := router.Group("/dailystory/posters")
	{
		posters.POST("/generate", handler.GeneratePoster)
	}
}
