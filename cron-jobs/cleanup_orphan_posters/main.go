package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
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
	log.Printf("[%s] Starting orphaned posters cleanup for DailyStoryApp\n", timestamp)

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

	// Get bucket name for DailyStory posters
	bucketName := os.Getenv("R2_DS_POSTERS_BUCKET_NAME")
	if bucketName == "" {
		log.Fatalf("[%s] ✗ R2_DS_POSTERS_BUCKET_NAME environment variable is required\n", timestamp)
	}

	log.Printf("[%s] Using bucket: %s\n", timestamp, bucketName)

	// Fetch all poster file_keys from database
	var fileKeys []string
	err = db.Table("image_posters").Pluck("file_key", &fileKeys).Error
	if err != nil {
		log.Fatalf("[%s] ✗ Failed to fetch poster file keys: %v\n", timestamp, err)
	}

	log.Printf("[%s] Found %d poster file keys in database\n", timestamp, len(fileKeys))

	// Build a set of active file keys
	activeKeys := make(map[string]bool)
	for _, key := range fileKeys {
		activeKeys[key] = true
	}

	// List all objects in the R2 bucket with "images/" prefix and find orphans
	orphanedKeys, err := findOrphanedPosters(r2Client, bucketName, activeKeys)
	if err != nil {
		log.Fatalf("[%s] ✗ Failed to find orphaned posters: %v\n", timestamp, err)
	}

	if len(orphanedKeys) == 0 {
		log.Printf("[%s] ✓ No orphaned posters found. All files are referenced in database.\n", timestamp)
		os.Exit(0)
	}

	log.Printf("[%s] ⚠ Found %d orphaned poster file(s):\n", timestamp, len(orphanedKeys))
	for _, key := range orphanedKeys {
		fmt.Println(key)
	}

	// Delete orphaned files
	log.Printf("[%s] Deleting orphaned files...\n", timestamp)
	deleted := 0
	failed := 0
	for _, key := range orphanedKeys {
		err := r2Client.DeleteFile(bucketName, key)
		if err != nil {
			log.Printf("[%s] ✗ Failed to delete %s: %v\n", timestamp, key, err)
			failed++
		} else {
			deleted++
		}
	}

	log.Printf("[%s] ✓ Cleanup completed: %d deleted, %d failed\n", timestamp, deleted, failed)
	log.Printf("[%s] ✓ Cleanup scan completed successfully\n", timestamp)

	os.Exit(0)
}

// findOrphanedPosters lists all objects in the R2 bucket with images prefix
// and returns keys that are not in the activeKeys map
func findOrphanedPosters(r2Client *storage.R2Client, bucketName string, activeKeys map[string]bool) ([]string, error) {
	var orphanedKeys []string

	// Get the internal S3 client from R2Client
	client := r2Client.GetClient()

	// List objects with "images/" prefix (posters are stored under images/)
	prefix := "images/"
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
