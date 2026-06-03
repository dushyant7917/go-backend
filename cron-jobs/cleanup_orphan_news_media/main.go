package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	r2ConfigRepository "go-backend/internal/apps/r2/config/repository"
	r2ConfigService "go-backend/internal/apps/r2/config/service"
	"go-backend/internal/common/constants"
	"go-backend/internal/common/database"
	"go-backend/pkg/storage"
	"go-backend/pkg/utils"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
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
	log.Printf("[%s] Starting orphaned news media cleanup\n", timestamp)

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
	bucketName := os.Getenv("R2_DS_NEWS_BUCKET_NAME")
	if bucketName == "" {
		log.Fatalf("[%s] ✗ R2_DS_NEWS_BUCKET_NAME environment variable is required\n", timestamp)
	}

	log.Printf("[%s] Using bucket: %s\n", timestamp, bucketName)

	// Fetch all media_file_key from news table
	var fileKeys []string
	err = db.Table("news").Where("media_file_key IS NOT NULL").Pluck("media_file_key", &fileKeys).Error
	if err != nil {
		log.Fatalf("[%s] ✗ Failed to fetch news media file keys: %v\n", timestamp, err)
	}

	log.Printf("[%s] Found %d news media file keys in database\n", timestamp, len(fileKeys))

	// Build a set of active file keys
	activeKeys := make(map[string]bool)
	for _, key := range fileKeys {
		activeKeys[key] = true
	}

	// List all objects in the R2 bucket with "media/" prefix and find orphans
	orphanedKeys, err := findOrphanedMedia(r2Client, bucketName, activeKeys)
	if err != nil {
		log.Fatalf("[%s] ✗ Failed to find orphaned media: %v\n", timestamp, err)
	}

	if len(orphanedKeys) == 0 {
		log.Printf("[%s] ✓ No orphaned media found. All files are referenced in database.\n", timestamp)
		os.Exit(0)
	}

	log.Printf("[%s] ⚠ Found %d orphaned media file(s):\n", timestamp, len(orphanedKeys))
	for _, key := range orphanedKeys {
		fmt.Println(key)
	}

	// Delete orphaned files in bulk with 5 goroutines
	log.Printf("[%s] Deleting %d orphaned files with 5 goroutines...\n", timestamp, len(orphanedKeys))

	// Split into batches of 1000 (R2/S3 limit per request)
	var batches [][]string
	for i := 0; i < len(orphanedKeys); i += 1000 {
		end := i + 1000
		if end > len(orphanedKeys) {
			end = len(orphanedKeys)
		}
		batches = append(batches, orphanedKeys[i:end])
	}

	// Use 5 goroutines to process batches
	numWorkers := 5
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
				failedKeys, err := r2Client.DeleteFiles(bucketName, batch)
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
	log.Printf("[%s] ✓ Cleanup completed: %d deleted, %d failed\n", timestamp, totalDeleted, totalFailed)

	os.Exit(0)
}

// findOrphanedMedia lists all objects in the R2 bucket with media prefix
// and returns keys that are not in the activeKeys map
func findOrphanedMedia(r2Client *storage.R2Client, bucketName string, activeKeys map[string]bool) ([]string, error) {
	var orphanedKeys []string

	// Get the internal S3 client from R2Client
	client := r2Client.GetClient()

	// List objects with "media/" prefix (news media are stored under media/)
	prefix := "media/"
	paginator := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucketName),
		Prefix: aws.String(prefix),
	})

	totalObjects := 0
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(context.Background())
		if err != nil {
			return nil, fmt.Errorf("failed to list objects: %w", err)
		}

		for _, obj := range page.Contents {
			totalObjects++
			key := aws.ToString(obj.Key)

			// Skip directory markers (keys ending with / or exactly matching prefix)
			if key == "" || key == prefix || strings.HasSuffix(key, "/") {
				continue
			}

			// Check if this key is NOT in the active keys map
			if !activeKeys[key] {
				orphanedKeys = append(orphanedKeys, key)
			}
		}
	}

	log.Printf("Scanned %d total objects in bucket with prefix '%s'\n", totalObjects, prefix)

	return orphanedKeys, nil
}
