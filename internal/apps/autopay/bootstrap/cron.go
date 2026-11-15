package bootstrap

import (
	"context"
	"log"
	"os"
	"time"

	"go-backend/internal/common/database"
	"go-backend/pkg/utils"

	"github.com/getsentry/sentry-go"
	"github.com/joho/godotenv"
)

// cronTimeout bounds one cron run: far above a night's work, short enough that a hung gateway call cannot pin a
// GitHub Actions runner for hours.
const cronTimeout = 3 * time.Hour

// RunCron is the shared main of every autopay cron: it loads the environment, initializes Sentry, opens the cron
// database connection, builds the autopay system, runs job, flushes analytics events and exits non-zero on failure.
func RunCron(name string, job func(ctx context.Context, a *Autopay) error) {
	env := utils.GetEnv("GO_ENV", "local")
	envFile := ".env." + env
	if err := godotenv.Load(envFile); err != nil {
		if err := godotenv.Load(); err != nil {
			log.Printf("No %s or .env file found, using environment variables", envFile)
		}
	}
	log.Printf("Starting %s\n", name)

	if dsn := utils.GetEnv("SENTRY_DSN", ""); dsn != "" {
		if err := sentry.Init(sentry.ClientOptions{Dsn: dsn, Environment: env}); err != nil {
			log.Printf("Sentry initialization failed: %v\n", err)
		} else {
			log.Printf("✓ Sentry initialized\n")
		}
	}

	db, err := database.NewCronConnection(database.Config{
		Host:     utils.GetEnv("DB_HOST", "localhost"),
		Port:     utils.GetEnv("DB_PORT", "5432"),
		User:     utils.GetEnv("DB_USER", "postgres"),
		Password: utils.GetEnv("DB_PASSWORD", ""),
		DBName:   utils.GetEnv("DB_NAME", "gobackend"),
		SSLMode:  utils.GetEnv("DB_SSL_MODE", "disable"),
	})
	if err != nil {
		sentry.CaptureException(err)
		sentry.Flush(2 * time.Second)
		log.Fatalf("✗ Failed to connect to database: %v\n", err)
	}
	log.Printf("✓ Database connected successfully\n")

	a := New(db, env)
	ctx, cancel := context.WithTimeout(context.Background(), cronTimeout)
	defer cancel()

	jobErr := job(ctx, a)
	a.Service.WaitForEvents() // flush Meta/PostHog events before exiting

	if jobErr != nil {
		sentry.CaptureException(jobErr)
		sentry.Flush(2 * time.Second)
		log.Printf("✗ %s failed: %v\n", name, jobErr)
		cancel()
		os.Exit(1)
	}
	sentry.Flush(2 * time.Second)
	log.Printf("✓ %s completed successfully\n", name)
}
