package handler

import "github.com/gin-gonic/gin"

// RegisterSanskaarRoutes registers all Sanskaar routes (devotional music, tones, wallpapers,
// statuses, status pictures, Hindu calendar events) onto the given router group.
func RegisterSanskaarRoutes(
	router *gin.RouterGroup,
	devotionalMusicH *DevotionalMusicHandler,
	toneH *ToneHandler,
	wallpaperH *WallpaperHandler,
	statusH *StatusHandler,
	statusPictureH *StatusPictureHandler,
	userStatusH *UserStatusHandler,
	hinduEventH *HinduEventHandler,
) {
	sanskaar := router.Group("/sanskaar")
	{
		// Combined user status (subscription, free-trial usage and pending meta event)
		sanskaar.GET("/user-status", userStatusH.GetUserStatus)

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

		// Read-only Hindu calendar (embedded JSON data); language_code is required on every route
		hinduEvents := sanskaar.Group("/hindu-events")
		{
			hinduEvents.GET("/today-or-upcoming", hinduEventH.GetTodayOrUpcoming)
			hinduEvents.GET("", hinduEventH.GetList)
			hinduEvents.GET("/:id", hinduEventH.GetByID)
		}

		statusPicture := sanskaar.Group("/status-picture")
		{
			statusPicture.POST("/upload-url", statusPictureH.GetUploadURL)
			statusPicture.GET("/view-url", statusPictureH.GetViewURL)
		}
	}
}
