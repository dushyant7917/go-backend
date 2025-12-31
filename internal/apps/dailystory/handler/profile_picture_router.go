package handler

import "github.com/gin-gonic/gin"

// RegisterProfilePictureRoutes registers all profile picture-related routes
func RegisterProfilePictureRoutes(router *gin.RouterGroup, handler *ProfilePictureHandler) {
	dailystory := router.Group("/dailystory")
	{
		profilePicture := dailystory.Group("/profile-picture")
		{
			profilePicture.POST("/upload-url", handler.GetUploadURL)
			profilePicture.GET("/view-url", handler.GetViewURL)
		}
	}
}
