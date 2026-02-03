package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"go-backend/internal/apps/user/repository"
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
	log.Printf("[%s] Starting orphaned profile pictures cleanup for DailyStoryApp\n", timestamp)

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

	// Get bucket name for DailyStoryApp users
	bucketName := os.Getenv("R2_DS_USERS_BUCKET_NAME")
	if bucketName == "" {
		log.Fatalf("[%s] ✗ R2_DS_USERS_BUCKET_NAME environment variable is required\n", timestamp)
	}

	log.Printf("[%s] Using bucket: %s\n", timestamp, bucketName)

	// Initialize repository
	userRepo := repository.NewUserRepository(db)

	// Fetch all users from DailyStoryApp and extract their profile_picture_key
	users, err := userRepo.FindByApp("DailyStoryApp")
	if err != nil {
		log.Fatalf("[%s] ✗ Failed to fetch users: %v\n", timestamp, err)
	}

	log.Printf("[%s] Found %d users in DailyStoryApp\n", timestamp, len(users))

	// Build a set of active profile picture keys
	activeKeys := make(map[string]bool)
	for _, user := range users {
		if user.Metadata != nil {
			if profilePicKey, ok := user.Metadata["profile_picture_key"].(string); ok && profilePicKey != "" {
				activeKeys[profilePicKey] = true
			}
		}
	}

	log.Printf("[%s] Found %d active profile picture keys in database\n", timestamp, len(activeKeys))

	// List all objects in the R2 bucket with "profile-pictures/" prefix
	orphanedKeys, err := findOrphanedProfilePictures(r2Client, bucketName, activeKeys)
	if err != nil {
		log.Fatalf("[%s] ✗ Failed to find orphaned profile pictures: %v\n", timestamp, err)
	}

	if len(orphanedKeys) == 0 {
		log.Printf("[%s] ✓ No orphaned profile pictures found. All files are assigned to users.\n", timestamp)
		os.Exit(0)
	}

	log.Printf("[%s] ⚠ Found %d orphaned profile picture:\n", timestamp, len(orphanedKeys))
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

// findOrphanedProfilePictures lists all objects in the R2 bucket with profile-pictures prefix
// and returns keys that are not in the activeKeys map
func findOrphanedProfilePictures(r2Client *storage.R2Client, bucketName string, activeKeys map[string]bool) ([]string, error) {
	var orphanedKeys []string

	// Get the internal S3 client from R2Client
	client := r2Client.GetClient()

	// List objects with "profile-pictures/" prefix
	prefix := "profile-pictures/"
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
