package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"go-backend/internal/apps/dailystory/models"
	"go-backend/internal/apps/dailystory/repository"
	r2ConfigRepository "go-backend/internal/apps/r2/config/repository"
	r2ConfigService "go-backend/internal/apps/r2/config/service"
	"go-backend/internal/common/constants"
	"go-backend/internal/common/database"
	"go-backend/pkg/utils"

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
	log.Printf("[%s] Starting rejected image templates cleanup\n", timestamp)

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

	// Initialize R2 client factory for dynamic config from database
	r2ConfigRepo := r2ConfigRepository.NewR2ConfigRepository(db)
	r2ClientFactory := r2ConfigService.NewR2ClientFactory(r2ConfigRepo)

	// Get R2 client for dailystory app
	r2Client, err := r2ClientFactory.GetClient(constants.AppNameDailyStory)
	if err != nil {
		log.Fatalf("[%s] ✗ Failed to initialize R2 client: %v\n", timestamp, err)
	}

	log.Printf("[%s] ✓ R2 client initialized successfully\n", timestamp)

	// Get bucket name for DailyStory templates
	bucketName := os.Getenv("R2_DS_TEMPLATES_BUCKET_NAME")
	if bucketName == "" {
		log.Fatalf("[%s] ✗ R2_DS_TEMPLATES_BUCKET_NAME environment variable is required\n", timestamp)
	}

	log.Printf("[%s] Using bucket: %s\n", timestamp, bucketName)

	// Initialize repository
	templateRepo := repository.NewImageTemplateRepository(db)

	// Fetch all rejected templates
	rejectedStatus := "rejected"
	templates, total, err := templateRepo.FindWithFilters("", "", nil, &rejectedStatus, 1, 10000)
	if err != nil {
		log.Fatalf("[%s] ✗ Failed to fetch rejected templates: %v\n", timestamp, err)
	}

	log.Printf("[%s] Found %d rejected templates (total: %d)\n", timestamp, len(templates), total)

	if len(templates) == 0 {
		log.Printf("[%s] ✓ No rejected templates found. Nothing to clean up.\n", timestamp)
		os.Exit(0)
	}

	log.Printf("[%s] ⚠ Found %d rejected templates:\n", timestamp, len(templates))
	for _, template := range templates {
		fmt.Println(template.FileKey)
	}

	// Delete rejected templates from R2 and database
	log.Printf("[%s] Deleting rejected templates...\n", timestamp)
	deletedFiles := 0
	failedFiles := 0
	deletedRecords := 0
	failedRecords := 0

	for _, template := range templates {
		// Start database transaction
		tx := db.Begin()
		if tx.Error != nil {
			log.Printf("[%s] ✗ Failed to start transaction for template %s: %v\n", timestamp, template.ID, tx.Error)
			failedRecords++
			failedFiles++
			continue
		}

		// Delete file from R2
		err := r2Client.DeleteFile(bucketName, template.FileKey)
		if err != nil {
			log.Printf("[%s] ✗ Failed to delete file %s: %v\n", timestamp, template.FileKey, err)
			tx.Rollback()
			failedFiles++
			failedRecords++
			continue
		}

		// Delete database record
		err = tx.Delete(&models.ImageTemplate{}, "id = ?", template.ID).Error
		if err != nil {
			log.Printf("[%s] ✗ Failed to delete template record %s: %v\n", timestamp, template.ID, err)
			tx.Rollback()
			failedRecords++
			// Note: R2 file is already deleted, but we don't count it as success since transaction failed
			failedFiles++
			continue
		}

		// Commit transaction
		if err := tx.Commit().Error; err != nil {
			log.Printf("[%s] ✗ Failed to commit transaction for template %s: %v\n", timestamp, template.ID, err)
			failedRecords++
			failedFiles++
			continue
		}

		deletedFiles++
		deletedRecords++
	}

	log.Printf("[%s] ✓ Cleanup completed:\n", timestamp)
	log.Printf("[%s]   Files: %d deleted, %d failed\n", timestamp, deletedFiles, failedFiles)
	log.Printf("[%s]   Records: %d deleted, %d failed\n", timestamp, deletedRecords, failedRecords)

	os.Exit(0)
}
