package main

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"go-backend/internal/apps/dailystory/models"
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

	// Check if days argument is provided
	if len(os.Args) < 2 {
		log.Fatal("Error: days argument is required\nUsage: go run main.go <days>\nExample: go run main.go 7")
	}

	days, err := strconv.Atoi(os.Args[1])
	if err != nil || days <= 0 {
		log.Fatalf("Error: days must be a positive integer, got: %s\n", os.Args[1])
	}

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	log.Printf("[%s] Starting cleanup of news older than %d days\n", timestamp, days)

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

	// Get bucket name for news media
	newsBucketName := os.Getenv("R2_DS_NEWS_BUCKET_NAME")
	if newsBucketName == "" {
		log.Fatalf("[%s] ✗ R2_DS_NEWS_BUCKET_NAME environment variable is required\n", timestamp)
	}

	log.Printf("[%s] Using bucket: %s\n", timestamp, newsBucketName)

	// Calculate cutoff date
	cutoffDate := time.Now().AddDate(0, 0, -days)
	log.Printf("[%s] Cutoff date: %s (news created before this will be deleted)\n", timestamp, cutoffDate.Format("2006-01-02 15:04:05"))

	// Fetch old news
	var newsItems []models.News
	err = db.Where("created_at < ?", cutoffDate).Find(&newsItems).Error
	if err != nil {
		log.Fatalf("[%s] ✗ Failed to fetch old news: %v\n", timestamp, err)
	}

	log.Printf("[%s] Found %d news items older than %d days\n", timestamp, len(newsItems), days)

	if len(newsItems) == 0 {
		log.Printf("[%s] ✓ No old news found. Nothing to clean up.\n", timestamp)
		os.Exit(0)
	}

	log.Printf("[%s] ⚠ Found %d old news items:\n", timestamp, len(newsItems))
	for _, news := range newsItems {
		fmt.Println(news.ID)
	}

	// Collect file keys for R2 deletion
	var fileKeys []string
	for _, news := range newsItems {
		if news.MediaFileKey != nil && *news.MediaFileKey != "" {
			fileKeys = append(fileKeys, *news.MediaFileKey)
		}
	}

	// Delete from database first (in a transaction)
	log.Printf("[%s] Deleting %d records from database...\n", timestamp, len(newsItems))
	tx := db.Begin()
	if tx.Error != nil {
		log.Fatalf("[%s] ✗ Failed to start transaction: %v\n", timestamp, tx.Error)
	}

	var newsIDs []string
	for _, news := range newsItems {
		newsIDs = append(newsIDs, news.ID.String())
	}

	// Bulk delete news records (cascades to news_translations and news_posters)
	err = tx.Delete(&models.News{}, "id IN ?", newsIDs).Error
	if err != nil {
		tx.Rollback()
		log.Fatalf("[%s] ✗ Failed to delete news records: %v\n", timestamp, err)
	}

	// Commit database transaction
	if err := tx.Commit().Error; err != nil {
		log.Fatalf("[%s] ✗ Failed to commit transaction: %v\n", timestamp, err)
	}

	log.Printf("[%s] ✓ Deleted %d records from database\n", timestamp, len(newsItems))

	// Delete media files from R2
	if len(fileKeys) > 0 {
		log.Printf("[%s] Deleting %d media files from R2 with 15 goroutines...\n", timestamp, len(fileKeys))

		// Split into batches of 1000 (R2/S3 limit per request)
		var batches [][]string
		for i := 0; i < len(fileKeys); i += 1000 {
			end := i + 1000
			if end > len(fileKeys) {
				end = len(fileKeys)
			}
			batches = append(batches, fileKeys[i:end])
		}

		// Use 15 goroutines to process batches
		numWorkers := 15
		if len(batches) < numWorkers {
			numWorkers = len(batches)
		}

		var totalDeleted int32
		var totalFailed int32
		var wg sync.WaitGroup

		// Create a channel for batches
		batchChan := make(chan []string, len(batches))
		for _, batch := range batches {
			batchChan <- batch
		}
		close(batchChan)

		// Start workers
		for i := 0; i < numWorkers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for batch := range batchChan {
					failedKeys, err := r2Client.DeleteFiles(newsBucketName, batch)
					if err != nil {
						log.Printf("[%s] ✗ Failed to delete batch from R2: %v\n", timestamp, err)
						atomic.AddInt32(&totalFailed, int32(len(batch)))
						continue
					}

					atomic.AddInt32(&totalDeleted, int32(len(batch)-len(failedKeys)))
					atomic.AddInt32(&totalFailed, int32(len(failedKeys)))

					for _, key := range failedKeys {
						log.Printf("[%s] ✗ Failed to delete file: %s\n", timestamp, key)
					}
				}
			}()
		}

		wg.Wait()
		log.Printf("[%s] ✓ R2 cleanup: %d deleted, %d failed\n", timestamp, totalDeleted, totalFailed)
	} else {
		log.Printf("[%s] ✓ No media files to delete from R2\n", timestamp)
	}

	log.Printf("[%s] ✓ Cleanup completed successfully\n", timestamp)
	os.Exit(0)
}
