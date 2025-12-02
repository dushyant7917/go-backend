package main

import (
	"log"
	"os"

	"go-backend/internal/apps/razorpay/handler"
	"go-backend/internal/apps/razorpay/repository"
	"go-backend/internal/apps/razorpay/service"
	"go-backend/internal/common/database"
	"go-backend/internal/common/middleware"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

func main() {
	// Load environment variables
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using environment variables")
	}

	// Database configuration
	dbConfig := database.Config{
		Host:     getEnv("DB_HOST", "localhost"),
		Port:     getEnv("DB_PORT", "5432"),
		User:     getEnv("DB_USER", "postgres"),
		Password: getEnv("DB_PASSWORD", "postgres"),
		DBName:   getEnv("DB_NAME", "go_backend"),
		SSLMode:  getEnv("DB_SSL_MODE", "disable"),
	}

	// Connect to database
	db, err := database.NewConnection(dbConfig)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	// Initialize Razorpay dependencies
	razorpayKeyID := getEnv("RAZORPAY_KEY_ID", "")
	razorpayKeySecret := getEnv("RAZORPAY_KEY_SECRET", "")
	razorpayWebhookSecret := getEnv("RAZORPAY_WEBHOOK_SECRET", "")

	if razorpayKeyID == "" || razorpayKeySecret == "" {
		log.Fatal("Razorpay credentials not configured")
	}

	subscriptionRepo := repository.NewSubscriptionRepository(db)
	subscriptionService := service.NewSubscriptionService(
		subscriptionRepo,
		razorpayKeyID,
		razorpayKeySecret,
		razorpayWebhookSecret,
	)
	subscriptionHandler := handler.NewSubscriptionHandler(subscriptionService)

	// Setup Gin router
	ginMode := getEnv("GIN_MODE", "release")
	gin.SetMode(ginMode)

	router := gin.Default()

	// Setup CORS middleware
	router.Use(middleware.SetupCORS())

	// Health check endpoint
	router.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"status":  "ok",
			"message": "Server is running",
		})
	})

	// API v1 routes
	v1 := router.Group("/api/v1")
	{
		// Register Razorpay subscription routes
		handler.RegisterSubscriptionRoutes(v1, subscriptionHandler)

		// Future apps can register their routes here
		// Example: handler.RegisterUserRoutes(v1, userHandler)
	}

	// Start server
	port := getEnv("PORT", "8080")
	log.Printf("Server starting on port %s", port)
	if err := router.Run(":" + port); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

// getEnv retrieves environment variable or returns default value
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
