package main

import (
	"log"
	"time"

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
	log.Printf("[%s] Starting retry failed billing cycles job (Cron B)\n", timestamp)

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

	db, err := database.NewConnection(dbConfig)
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
	recurringPaymentService := service.NewRecurringPaymentService(recurringPaymentRepository, razorpayConfigRepo, userRepository, metaDatasetConfigRepo, posthogConfigRepo)

	// Cron B: Retry failed billing cycles - send notification for retry attempt
	// Filters billing cycles with pending status and next_attempt_at 25-50 hours away, max 8 attempts
	if err := recurringPaymentService.RetryFailedBillingCycles(); err != nil {
		log.Printf("[%s] ✗ Retry failed billing cycles job failed: %v\n", timestamp, err)
	} else {
		log.Printf("[%s] ✓ Retry failed billing cycles job completed successfully\n", timestamp)
	}
}
