package main

import (
	"fmt"
	"log"
	"os"
	"time"

	commonResponse "go-backend/internal/common/response"

	"github.com/getsentry/sentry-go"
	sentrygin "github.com/getsentry/sentry-go/gin"

	agoraChatHandler "go-backend/internal/apps/agora/chat/handler"
	agoraChatService "go-backend/internal/apps/agora/chat/service"
	chemistryHandler "go-backend/internal/apps/chemistry/handler"
	crushHandler "go-backend/internal/apps/crush/handler"
	dailystoryInngest "go-backend/internal/apps/dailystory/inngest"
	crushRepository "go-backend/internal/apps/crush/repository"
	crushService "go-backend/internal/apps/crush/service"
	dailystoryHandler "go-backend/internal/apps/dailystory/handler"
	dailystoryRepository "go-backend/internal/apps/dailystory/repository"
	dailystoryService "go-backend/internal/apps/dailystory/service"
	metaEventHandler "go-backend/internal/apps/meta_event/handler"
	metaEventRepository "go-backend/internal/apps/meta_event/repository"
	metaEventService "go-backend/internal/apps/meta_event/service"
	metaDatasetRepository "go-backend/internal/apps/metadataset/config/repository"
	otpHandler "go-backend/internal/apps/otp/handler"
	otpRepository "go-backend/internal/apps/otp/repository"
	otpService "go-backend/internal/apps/otp/service"
	posthogConfigHandler "go-backend/internal/apps/posthog/config/handler"
	posthogConfigRepository "go-backend/internal/apps/posthog/config/repository"
	posthogConfigService "go-backend/internal/apps/posthog/config/service"
	r2ConfigHandler "go-backend/internal/apps/r2/config/handler"
	r2ConfigRepository "go-backend/internal/apps/r2/config/repository"
	r2ConfigService "go-backend/internal/apps/r2/config/service"
	configHandler "go-backend/internal/apps/razorpay/config/handler"
	configRepository "go-backend/internal/apps/razorpay/config/repository"
	configService "go-backend/internal/apps/razorpay/config/service"
	recurringPaymentHandler "go-backend/internal/apps/razorpay/recurring_payment/handler"
	recurringPaymentRepository "go-backend/internal/apps/razorpay/recurring_payment/repository"
	recurringPaymentService "go-backend/internal/apps/razorpay/recurring_payment/service"
	razorpayHandler "go-backend/internal/apps/razorpay/subscription/handler"
	razorpayRepository "go-backend/internal/apps/razorpay/subscription/repository"
	razorpayService "go-backend/internal/apps/razorpay/subscription/service"
	streamChatHandler "go-backend/internal/apps/stream/chat/handler"
	streamChatService "go-backend/internal/apps/stream/chat/service"
	userHandler "go-backend/internal/apps/user/handler"
	userRepository "go-backend/internal/apps/user/repository"
	userService "go-backend/internal/apps/user/service"
	wingwomanHandler "go-backend/internal/apps/wingwoman/handler"
	wingwomanRepository "go-backend/internal/apps/wingwoman/repository"
	wingwomanService "go-backend/internal/apps/wingwoman/service"
	"go-backend/internal/common/database"
	"go-backend/internal/common/middleware"
	"go-backend/pkg/notification"

	"github.com/gin-gonic/gin"
	"github.com/inngest/inngestgo"
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

	// Initialize Sentry
	sentryDsn := getEnv("SENTRY_DSN", "")
	if sentryDsn != "" {
		err := sentry.Init(sentry.ClientOptions{
			Dsn:              sentryDsn,
			Environment:      env,
			TracesSampleRate: 1.0,
		})
		if err != nil {
			log.Printf("Sentry initialization failed: %v", err)
		} else {
			log.Println("Sentry initialized successfully")
		}
		defer sentry.Flush(2 * time.Second)
	} else {
		log.Println("SENTRY_DSN not set, skipping Sentry initialization")
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
	// Note: With multi-client support, Razorpay credentials are now stored per config in the database
	// The old environment variables are no longer used for subscription operations
	configRepo := configRepository.NewRazorpayConfigRepository(db)
	configSvc := configService.NewRazorpayConfigService(configRepo)
	configH := configHandler.NewRazorpayConfigHandler(configSvc)

	// Initialize Meta dataset dependencies
	metaDatasetRepo := metaDatasetRepository.NewMetaDatasetConfigRepository(db)

	// Initialize R2 Config dependencies
	r2ConfigRepo := r2ConfigRepository.NewR2ConfigRepository(db)
	r2ConfigSvc := r2ConfigService.NewR2ConfigService(r2ConfigRepo)
	r2ConfigH := r2ConfigHandler.NewR2ConfigHandler(r2ConfigSvc)

	// Initialize R2 Client Factory for dynamic per-app R2 clients
	r2ClientFactory := r2ConfigService.NewR2ClientFactory(r2ConfigRepo)

	subscriptionRepo := razorpayRepository.NewSubscriptionRepository(db)
	subscriptionService := razorpayService.NewSubscriptionService(
		subscriptionRepo,
		configRepo,
		metaDatasetRepo,
	)
	subscriptionHandler := razorpayHandler.NewSubscriptionHandler(subscriptionService)

	// Initialize repositories
	userRepo := userRepository.NewUserRepository(db)
	crushRepo := crushRepository.NewCrushRepository(db)

	// Initialize Recurring Payment dependencies
	recurringPaymentRepo := recurringPaymentRepository.NewRecurringPaymentRepository(db)
	posthogConfigRepo := posthogConfigRepository.NewPostHogConfigRepository(db)
	// Initialize Meta Event dependencies
	metaEventRepo := metaEventRepository.NewMetaEventRepository(db)
	metaEventSvc := metaEventService.NewMetaEventService(metaEventRepo)
	metaEventH := metaEventHandler.NewMetaEventHandler(metaEventSvc)

	recurringPaymentSvc := recurringPaymentService.NewRecurringPaymentService(recurringPaymentRepo, configRepo, userRepo, metaDatasetRepo, posthogConfigRepo, metaEventSvc)
	recurringPaymentH := recurringPaymentHandler.NewRecurringPaymentHandler(recurringPaymentSvc)

	// Initialize PostHog Config dependencies
	posthogConfigSvc := posthogConfigService.NewPostHogConfigService(posthogConfigRepo)
	posthogConfigH := posthogConfigHandler.NewPostHogConfigHandler(posthogConfigSvc)

	// Initialize services
	crushSvc := crushService.NewCrushService(crushRepo, userRepo)
	userSvc := userService.NewUserService(userRepo, crushRepo, r2ClientFactory)

	// Initialize handlers
	crushH := crushHandler.NewCrushHandler(crushSvc)
	userH := userHandler.NewUserHandler(userSvc)

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

	// Initialize DailyStory dependencies
	imageTemplateRepo := dailystoryRepository.NewImageTemplateRepository(db)
	imageTemplateSvc := dailystoryService.NewImageTemplateService(imageTemplateRepo)
	imageTemplateH := dailystoryHandler.NewImageTemplateHandler(imageTemplateSvc, r2ClientFactory)

	// Initialize DailyStory Profile Picture handler
	profilePictureH := dailystoryHandler.NewProfilePictureHandler(r2ClientFactory)

	// Initialize Chemistry Profile Picture handler
	chemistryProfilePictureH := chemistryHandler.NewProfilePictureHandler(r2ClientFactory)

	// Initialize DailyStory Image Poster dependencies
	imagePosterRepo := dailystoryRepository.NewImagePosterRepository(db)
	imagePosterSvc := dailystoryService.NewImagePosterService(imagePosterRepo, imageTemplateRepo, userRepo)
	imagePosterH := dailystoryHandler.NewImagePosterHandler(imagePosterSvc, r2ClientFactory)

	// Initialize WingWoman dependencies
	helperRepo := wingwomanRepository.NewHelperRepository(db)
	helperSvc := wingwomanService.NewHelperService(helperRepo)
	helperH := wingwomanHandler.NewHelperHandler(helperSvc)

	// Initialize News dependencies (within DailyStory)
	newsRepo := dailystoryRepository.NewNewsRepository(db)
	newsSvc := dailystoryService.NewNewsService(newsRepo)
	newsH := dailystoryHandler.NewNewsHandler(newsSvc)

	// Initialize News Poster dependencies (within DailyStory)
	newsPosterRepo := dailystoryRepository.NewNewsPosterRepository(db)
	newsPosterSvc := dailystoryService.NewNewsPosterService(newsPosterRepo)
	newsPosterH := dailystoryHandler.NewNewsPosterHandler(newsPosterSvc, recurringPaymentRepo, r2ClientFactory)

	// Initialize Combined Subscription Status handler (dailystory)
	dailystoryH := dailystoryHandler.NewDailystoryHandler(subscriptionRepo, recurringPaymentRepo, metaEventSvc)

	// Initialize Agora Chat dependencies
	chatSvc := agoraChatService.NewChatService()
	chatH := agoraChatHandler.NewChatHandler(chatSvc)

	// Initialize Stream Chat dependencies
	streamChatSvc := streamChatService.NewChatService()
	streamChatH := streamChatHandler.NewChatHandler(streamChatSvc)

	// Setup Gin router
	ginMode := getEnv("GIN_MODE", "release")
	gin.SetMode(ginMode)

	router := gin.Default()

	// Add Sentry middleware
	if sentryDsn != "" {
		router.Use(sentrygin.New(sentrygin.Options{
			Repanic: true,
		}))
	}

	// Health check endpoint (before CORS middleware to allow access from any client)
	router.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"status":  "ok",
			"message": "Server is running",
		})
	})

	// Sentry test endpoint - triggers a 500 error to verify Sentry capture
	router.GET("/test-sentry", func(c *gin.Context) {
		commonResponse.Error(c, 500, fmt.Errorf("test sentry error: %s", c.Query("message")), "intentional test error for sentry")
	})

	// Razorpay config creation endpoint (before CORS middleware for admin access)
	router.POST("/api/v1/razorpay-configs", configH.CreateRazorpayConfig)

	// PostHog config creation endpoint (before CORS middleware for admin access)
	router.POST("/api/v1/posthog-configs", posthogConfigH.CreatePostHogConfig)

	// Inngest function handler — server-to-server, no CORS needed
	pushClient := notification.NewExpoPushClient()
	inngestClient, err := inngestgo.NewClient(inngestgo.ClientOpts{
		AppID: getEnv("INNGEST_APP_ID", "go-backend"),
	})
	if err != nil {
		log.Fatalf("Failed to create Inngest client: %v", err)
	}
	if regErr := dailystoryInngest.RegisterFunctions(inngestClient, pushClient); regErr != nil {
		log.Fatalf("Failed to register Inngest functions: %v", regErr)
	}
	router.Any("/api/inngest", gin.WrapH(inngestClient.Serve()))

	// Setup CORS middleware
	router.Use(middleware.SetupCORS(env))

	// API v1 routes
	v1 := router.Group("/api/v1")
	{
		// Register Razorpay Config management routes
		configHandler.RegisterRazorpayConfigRoutes(v1, configH)

		// Register R2 Config management routes
		r2ConfigHandler.RegisterR2ConfigRoutes(v1, r2ConfigH)

		// Register PostHog Config management routes
		posthogConfigHandler.RegisterPostHogConfigRoutes(v1, posthogConfigH)

		// Register Razorpay subscription routes
		razorpayHandler.RegisterSubscriptionRoutes(v1, subscriptionHandler)

		// Register Recurring Payment routes
		recurringPaymentHandler.RegisterRecurringPaymentRoutes(v1, recurringPaymentH)

		// Register User management routes
		userHandler.RegisterUserRoutes(v1, userH)

		// Register OTP routes
		otpHandler.RegisterOTPRoutes(v1, phoneOTPH, emailOTPH)

		// Register Crush Connect routes
		crushHandler.RegisterCrushRoutes(v1, crushH)

		// Register DailyStory routes
		dailystoryHandler.RegisterImageTemplateRoutes(v1, imageTemplateH)
		dailystoryHandler.RegisterProfilePictureRoutes(v1, profilePictureH)
		dailystoryHandler.RegisterImagePosterRoutes(v1, imagePosterH)

		// Register Chemistry routes
		chemistryHandler.RegisterProfilePictureRoutes(v1, chemistryProfilePictureH)

		// Register WingWoman routes
		wingwomanHandler.RegisterWingWomanRoutes(v1, helperH)

		// Register News routes (within DailyStory)
		dailystoryHandler.RegisterNewsRoutes(v1, newsH, newsPosterH)

		// Register Combined Subscription Status routes (dailystory)
		dailystoryHandler.SetupDailystoryRouter(v1, dailystoryH)

		// Register Meta Event routes
		metaEventHandler.RegisterMetaEventRoutes(v1, metaEventH)

		// Register Agora routes
		agoraChatHandler.RegisterChatRoutes(v1, chatH)

		// Register Stream routes
		streamChatHandler.RegisterChatRoutes(v1, streamChatH)

		// Future apps can register their routes here
		// Example: handler.RegisterUserRoutes(v1, userHandler)
	}

	// Start server
	port := getEnv("PORT", "8082")
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
