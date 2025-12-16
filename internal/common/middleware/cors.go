package middleware

import (
	"log"
	"os"
	"strings"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

// SetupCORS configures CORS middleware with environment-specific settings
func SetupCORS(env string) gin.HandlerFunc {
	allowOrigins := getAllowedOrigins(env)

	return cors.New(cors.Config{
		AllowOrigins:     allowOrigins,
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	})
}

func getAllowedOrigins(env string) []string {
	if originsEnv := os.Getenv("CORS_ALLOWED_ORIGINS"); originsEnv != "" {
		return parseOrigins(originsEnv)
	}

	if env == "prod" {
		log.Fatal("CORS_ALLOWED_ORIGINS must be set in production")
	}

	return []string{"*"}
}

func parseOrigins(origins string) []string {
	parts := strings.Split(origins, ",")
	var result []string

	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}

	return result
}
