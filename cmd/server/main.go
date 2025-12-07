package main

import (
	"log"
	"os"

	crushHandler "go-backend/internal/apps/crush/handler"
	crushRepository "go-backend/internal/apps/crush/repository"
	crushService "go-backend/internal/apps/crush/service"
	otpHandler "go-backend/internal/apps/otp/handler"
	otpRepository "go-backend/internal/apps/otp/repository"
	otpService "go-backend/internal/apps/otp/service"
	razorpayHandler "go-backend/internal/apps/razorpay/handler"
	razorpayRepository "go-backend/internal/apps/razorpay/repository"
	razorpayService "go-backend/internal/apps/razorpay/service"
	userHandler "go-backend/internal/apps/user/handler"
	userRepository "go-backend/internal/apps/user/repository"
	userService "go-backend/internal/apps/user/service"
	"go-backend/internal/common/database"
	"go-backend/internal/common/middleware"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

func main() {
	// Load environment variables from appropriate file
	env := getEnv("GO_ENV", "local")
	envFile := ".env." + env
	if err := godotenv.Load(envFile); err != nil {
		// Fallback to .env if environment-specific file not found
		if err := godotenv.Load(); err != nil {
			log.Printf("No %s or .env file found, using environment variables", envFile)
		}
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

	subscriptionRepo := razorpayRepository.NewSubscriptionRepository(db)
	subscriptionService := razorpayService.NewSubscriptionService(
		subscriptionRepo,
		razorpayKeyID,
		razorpayKeySecret,
		razorpayWebhookSecret,
	)
	subscriptionHandler := razorpayHandler.NewSubscriptionHandler(subscriptionService)

	// Initialize User management dependencies
	userRepo := userRepository.NewUserRepository(db)
	userSvc := userService.NewUserService(userRepo)
	userH := userHandler.NewUserHandler(userSvc)

	// Initialize Crush Connect dependencies
	crushRepo := crushRepository.NewCrushRepository(db)
	crushSvc := crushService.NewCrushService(crushRepo, userRepo)
	crushH := crushHandler.NewCrushHandler(crushSvc)

	// Initialize OTP dependencies
	// Use AuthKey provider for production, no-op for local/dev
	var otpProvider otpService.OTPProvider
	if env == "prod" {
		authKey := getEnv("AUTHKEY_API_KEY", "")
		authKeyTemplateID := getEnv("AUTHKEY_TEMPLATE_ID", "")

		if authKey == "" || authKeyTemplateID == "" {
			log.Fatal("AUTHKEY_API_KEY and AUTHKEY_TEMPLATE_ID are required in production")
		}

		otpProvider = otpService.NewAuthKeyProvider(authKey, authKeyTemplateID)
		log.Println("Using AuthKey SMS provider (production mode)")
	} else {
		otpProvider = otpService.NewNoOpProvider()
		log.Println("Using No-Op provider - OTP will be logged only (local/dev mode)")
	}

	phoneOTPRepo := otpRepository.NewPhoneOTPRepository(db)
	emailOTPRepo := otpRepository.NewEmailOTPRepository(db)
	phoneOTPSvc := otpService.NewPhoneOTPService(phoneOTPRepo, otpProvider)
	emailOTPSvc := otpService.NewEmailOTPService(emailOTPRepo)
	phoneOTPH := otpHandler.NewPhoneOTPHandler(phoneOTPSvc)
	emailOTPH := otpHandler.NewEmailOTPHandler(emailOTPSvc)

	// Setup Gin router
	ginMode := getEnv("GIN_MODE", "release")
	gin.SetMode(ginMode)

	router := gin.Default()

	// Setup CORS middleware
	router.Use(middleware.SetupCORS(env))

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
		razorpayHandler.RegisterSubscriptionRoutes(v1, subscriptionHandler)

		// Register User management routes
		userHandler.RegisterUserRoutes(v1, userH)

		// Register OTP routes
		otpHandler.RegisterOTPRoutes(v1, phoneOTPH, emailOTPH)

		// Register Crush Connect routes
		crushHandler.RegisterCrushRoutes(v1, crushH)

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
