package main

import (
	"log"
	"time"

	metaEventRepo "go-backend/internal/apps/meta_event/repository"
	metaEventSvc "go-backend/internal/apps/meta_event/service"
	metaDatasetRepo "go-backend/internal/apps/metadataset/config/repository"
	posthogConfigRepository "go-backend/internal/apps/posthog/config/repository"
	configRepo "go-backend/internal/apps/razorpay/config/repository"
	recurringPaymentRepo "go-backend/internal/apps/razorpay/recurring_payment/repository"
	"go-backend/internal/apps/razorpay/recurring_payment/service"
	userRepo "go-backend/internal/apps/user/repository"
	"go-backend/internal/common/database"
	"go-backend/pkg/utils"

	"github.com/getsentry/sentry-go"
	"github.com/joho/godotenv"
)

func main() {
	// Load environment variables from appropriate file
	env := utils.GetEnv("GO_ENV", "local")
	envFile := ".env." + env
	if err := godotenv.Load(envFile); err != nil {
		// Fallback to .env if environment-specific file not found
		if err := godotenv.Load(); err != nil {
			log.Printf("No %s or .env file found, using environment variables", envFile)
		}
	}

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	log.Printf("[%s] Starting process new billing cycles job (Cron A)\n", timestamp)

	// Initialize Sentry
	sentryDsn := utils.GetEnv("SENTRY_DSN", "")
	if sentryDsn != "" {
		if err := sentry.Init(sentry.ClientOptions{
			Dsn:         sentryDsn,
			Environment: env,
		}); err != nil {
			log.Printf("[%s] Sentry initialization failed: %v\n", timestamp, err)
		} else {
			log.Printf("[%s] ✓ Sentry initialized\n", timestamp)
		}
		defer sentry.Flush(2 * time.Second)
	}

	// Connect to database
	dbConfig := database.Config{
		Host:     utils.GetEnv("DB_HOST", "localhost"),
		Port:     utils.GetEnv("DB_PORT", "5432"),
		User:     utils.GetEnv("DB_USER", "postgres"),
		Password: utils.GetEnv("DB_PASSWORD", ""),
		DBName:   utils.GetEnv("DB_NAME", "gobackend"),
		SSLMode:  utils.GetEnv("DB_SSL_MODE", "disable"),
	}

	db, err := database.NewCronConnection(dbConfig)
	if err != nil {
		log.Fatalf("[%s] ✗ Failed to connect to database: %v\n", timestamp, err)
	}

	log.Printf("[%s] ✓ Database connected successfully\n", timestamp)

	// Initialize repositories and service
	razorpayConfigRepo := configRepo.NewRazorpayConfigRepository(db)
	recurringPaymentRepository := recurringPaymentRepo.NewRecurringPaymentRepository(db)
	userRepository := userRepo.NewUserRepository(db)
	metaDatasetConfigRepo := metaDatasetRepo.NewMetaDatasetConfigRepository(db)
	posthogConfigRepo := posthogConfigRepository.NewPostHogConfigRepository(db)
	metaEventRepository := metaEventRepo.NewMetaEventRepository(db)
	metaEventService := metaEventSvc.NewMetaEventService(metaEventRepository)
	recurringPaymentService := service.NewRecurringPaymentService(recurringPaymentRepository, razorpayConfigRepo, userRepository, metaDatasetConfigRepo, posthogConfigRepo, metaEventService)

	// Cron A: Process new billing cycles - create billing cycle and send notification
	// Filters recurring payments where next_charge_at is 25-50 hours away and no pending billing cycle
	if err := recurringPaymentService.ProcessNewBillingCycles(); err != nil {
		log.Printf("[%s] ✗ Process new billing cycles job failed: %v\n", timestamp, err)
	} else {
		log.Printf("[%s] ✓ Process new billing cycles job completed successfully\n", timestamp)
	}
}
