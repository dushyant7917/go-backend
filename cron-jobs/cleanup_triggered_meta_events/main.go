package main

import (
	"log"
	"os"
	"strconv"
	"time"

	"go-backend/internal/apps/meta_event/models"
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

	// Check if days argument is provided
	if len(os.Args) < 2 {
		log.Fatal("Error: days argument is required\nUsage: go run main.go <days>\nExample: go run main.go 30")
	}

	days, err := strconv.Atoi(os.Args[1])
	if err != nil || days <= 0 {
		log.Fatalf("Error: days must be a positive integer, got: %s\n", os.Args[1])
	}

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	log.Printf("[%s] Starting cleanup of triggered meta_events older than %d days\n", timestamp, days)

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

	// Calculate cutoff date
	cutoffDate := time.Now().AddDate(0, 0, -days)
	log.Printf("[%s] Cutoff date: %s (triggered meta_events created before this will be deleted)\n", timestamp, cutoffDate.Format("2006-01-02 15:04:05"))

	// Count triggered meta_events older than cutoff
	var count int64
	err = db.Model(&models.MetaEvent{}).Where("triggered = ? AND created_at < ?", true, cutoffDate).Count(&count).Error
	if err != nil {
		log.Fatalf("[%s] ✗ Failed to count triggered meta_events: %v\n", timestamp, err)
	}

	log.Printf("[%s] Found %d triggered meta_events older than %d days\n", timestamp, count, days)

	if count == 0 {
		log.Printf("[%s] ✓ No triggered meta_events to clean up.\n", timestamp)
		os.Exit(0)
	}

	// Bulk delete triggered meta_events older than cutoff (DB first, no external storage)
	log.Printf("[%s] Deleting %d records from database...\n", timestamp, count)
	tx := db.Begin()
	if tx.Error != nil {
		log.Fatalf("[%s] ✗ Failed to start transaction: %v\n", timestamp, tx.Error)
	}

	result := tx.Where("triggered = ? AND created_at < ?", true, cutoffDate).Delete(&models.MetaEvent{})
	if result.Error != nil {
		tx.Rollback()
		log.Fatalf("[%s] ✗ Failed to delete triggered meta_events: %v\n", timestamp, result.Error)
	}

	// Commit database transaction
	if err := tx.Commit().Error; err != nil {
		log.Fatalf("[%s] ✗ Failed to commit transaction: %v\n", timestamp, err)
	}

	log.Printf("[%s] ✓ Deleted %d triggered meta_events from database\n", timestamp, result.RowsAffected)
	log.Printf("[%s] ✓ Cleanup completed successfully\n", timestamp)

	os.Exit(0)
}
