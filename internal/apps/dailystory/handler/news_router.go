package handler

import "github.com/gin-gonic/gin"

// RegisterNewsRoutes registers all news routes
func RegisterNewsRoutes(router *gin.RouterGroup, handler *NewsHandler, newsPosterHandler *NewsPosterHandler) {
	news := router.Group("/dailystory/news")
	{
		news.GET("", handler.ListNews)
		news.PATCH("/media-file-key", handler.BulkUpdateNewsMediaFileKey)
		news.PUT("/:id", handler.UpdateNews)
	}

	// News poster routes
	newsPosters := router.Group("/dailystory/news-posters")
	{
		newsPosters.POST("", newsPosterHandler.CreateNewsPoster)
	}
}
