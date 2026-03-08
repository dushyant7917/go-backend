package handler

import "github.com/gin-gonic/gin"

// RegisterProfilePictureRoutes registers all Chemistry profile picture-related routes
func RegisterProfilePictureRoutes(router *gin.RouterGroup, handler *ProfilePictureHandler) {
	chemistry := router.Group("/chemistry")
	{
		profilePicture := chemistry.Group("/profile-picture")
		{
			profilePicture.POST("/upload-url", handler.GetUploadURL)
			profilePicture.GET("/view-url", handler.GetViewURL)
		}
	}
}
