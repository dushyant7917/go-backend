package middleware

import (
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

// SetupCORS configures CORS middleware with environment-specific settings
func SetupCORS(env string) gin.HandlerFunc {
	var allowOrigins []string

	if env == "prod" {
		// Production: only allow specific domains
		allowOrigins = []string{
			"https://nanotv.site",
			"https://www.nanotv.site",
			"https://krushconnect.site",
			"https://www.krushconnect.site",
			"https://jobsfeed.in",
			"https://www.jobsfeed.in",
			"https://ai-bestie-six.vercel.app",
			"https://www.ai-bestie-six.vercel.app",
			"https://womenpov.site",
			"https://www.womenpov.site",
		}
	} else {
		// Development/local: allow all origins
		allowOrigins = []string{"*"}
	}

	return cors.New(cors.Config{
		AllowOrigins:     allowOrigins,
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	})
}
