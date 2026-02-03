package main

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"go-backend/internal/apps/dailystory/models"
	"go-backend/internal/common/database"
	"go-backend/pkg/storage"
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

	// Check if days argument is provided
	if len(os.Args) < 2 {
		log.Fatal("Error: days argument is required\nUsage: go run cleanup_old_posters.go <days>\nExample: go run cleanup_old_posters.go 7")
	}

	days, err := strconv.Atoi(os.Args[1])
	if err != nil || days <= 0 {
		log.Fatalf("Error: days must be a positive integer, got: %s\n", os.Args[1])
	}

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	log.Printf("[%s] Starting cleanup of posters older than %d days\n", timestamp, days)

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

	// Initialize R2 client
	r2Client, err := storage.NewR2ClientFromEnv()
	if err != nil {
		log.Fatalf("[%s] ✗ Failed to initialize R2 client: %v\n", timestamp, err)
	}

	log.Printf("[%s] ✓ R2 client initialized successfully\n", timestamp)

	// Get bucket name for DailyStory posters
	bucketName := os.Getenv("R2_DS_POSTERS_BUCKET_NAME")
	if bucketName == "" {
		log.Fatalf("[%s] ✗ R2_DS_POSTERS_BUCKET_NAME environment variable is required\n", timestamp)
	}

	log.Printf("[%s] Using bucket: %s\n", timestamp, bucketName)

	// Calculate cutoff date
	cutoffDate := time.Now().AddDate(0, 0, -days)
	log.Printf("[%s] Cutoff date: %s (posters created before this will be deleted)\n", timestamp, cutoffDate.Format("2006-01-02 15:04:05"))

	// Fetch old posters
	var posters []models.ImagePoster
	err = db.Where("created_at < ?", cutoffDate).Find(&posters).Error
	if err != nil {
		log.Fatalf("[%s] ✗ Failed to fetch old posters: %v\n", timestamp, err)
	}

	log.Printf("[%s] Found %d posters older than %d days\n", timestamp, len(posters), days)

	if len(posters) == 0 {
		log.Printf("[%s] ✓ No old posters found. Nothing to clean up.\n", timestamp)
		os.Exit(0)
	}

	log.Printf("[%s] ⚠ Found %d old posters:\n", timestamp, len(posters))
	for _, poster := range posters {
		fmt.Println(poster.FileKey)
	}

	// Delete old posters from R2 and database atomically
	log.Printf("[%s] Deleting old posters...\n", timestamp)
	deletedFiles := 0
	failedFiles := 0
	deletedRecords := 0
	failedRecords := 0

	for _, poster := range posters {
		// Start database transaction
		tx := db.Begin()
		if tx.Error != nil {
			log.Printf("[%s] ✗ Failed to start transaction for poster %s: %v\n", timestamp, poster.ID, tx.Error)
			failedRecords++
			failedFiles++
			continue
		}

		// Delete file from R2
		err := r2Client.DeleteFile(bucketName, poster.FileKey)
		if err != nil {
			log.Printf("[%s] ✗ Failed to delete file %s: %v\n", timestamp, poster.FileKey, err)
			tx.Rollback()
			failedFiles++
			failedRecords++
			continue
		}

		// Delete database record
		err = tx.Delete(&models.ImagePoster{}, "id = ?", poster.ID).Error
		if err != nil {
			log.Printf("[%s] ✗ Failed to delete poster record %s: %v\n", timestamp, poster.ID, err)
			tx.Rollback()
			failedRecords++
			// Note: R2 file is already deleted, but we don't count it as success since transaction failed
			failedFiles++
			continue
		}

		// Commit transaction
		if err := tx.Commit().Error; err != nil {
			log.Printf("[%s] ✗ Failed to commit transaction for poster %s: %v\n", timestamp, poster.ID, err)
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
