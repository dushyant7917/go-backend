package handler

import "github.com/gin-gonic/gin"

// RegisterSanskaarRoutes registers all Sanskaar routes (devotional music, tones, wallpapers,
// statuses, status pictures) onto the given router group.
func RegisterSanskaarRoutes(
	router *gin.RouterGroup,
	devotionalMusicH *DevotionalMusicHandler,
	toneH *ToneHandler,
	wallpaperH *WallpaperHandler,
	statusH *StatusHandler,
	statusPictureH *StatusPictureHandler,
) {
	sanskaar := router.Group("/sanskaar")
	{
		devotionalMusic := sanskaar.Group("/devotional-music")
		{
			devotionalMusic.POST("", devotionalMusicH.CreateDevotionalMusic)
			devotionalMusic.POST("/upload-url", devotionalMusicH.GetUploadURL)
			devotionalMusic.GET("", devotionalMusicH.GetDevotionalMusicList)
			devotionalMusic.GET("/:id", devotionalMusicH.GetDevotionalMusic)
			devotionalMusic.PUT("/:id", devotionalMusicH.UpdateDevotionalMusic)
		}

		tones := sanskaar.Group("/tones")
		{
			tones.POST("", toneH.CreateTone)
			tones.POST("/upload-url", toneH.GetUploadURL)
			tones.GET("", toneH.GetToneList)
			tones.GET("/:id", toneH.GetTone)
			tones.PUT("/:id", toneH.UpdateTone)
		}

		wallpapers := sanskaar.Group("/wallpapers")
		{
			wallpapers.POST("", wallpaperH.CreateWallpaper)
			wallpapers.POST("/upload-url", wallpaperH.GetUploadURL)
			wallpapers.GET("", wallpaperH.GetWallpaperList)
			wallpapers.GET("/:id", wallpaperH.GetWallpaper)
			wallpapers.PUT("/:id", wallpaperH.UpdateWallpaper)
		}

		statuses := sanskaar.Group("/statuses")
		{
			statuses.POST("", statusH.CreateStatus)
			statuses.POST("/upload-url", statusH.GetUploadURL)
			statuses.GET("", statusH.GetStatusList)
			statuses.GET("/:id", statusH.GetStatus)
			statuses.PUT("/:id", statusH.UpdateStatus)
		}

		statusPicture := sanskaar.Group("/status-picture")
		{
			statusPicture.POST("/upload-url", statusPictureH.GetUploadURL)
			statusPicture.GET("/view-url", statusPictureH.GetViewURL)
		}
	}
}
