package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"go-backend/internal/apps/user/repository"
	"go-backend/internal/common/database"
	"go-backend/pkg/notification"
	"go-backend/pkg/utils"

	"github.com/joho/godotenv"
)

// Configuration - Update these values as needed
const (
	MessageTitle = "आपके लिए नई स्टोरी तैयार है!"
	MessageBody  = "रेलवे का बढ़िया फैसला है! अब हर ट्रेन का अनाउंसमेंट होगा - रुके या न रुके। पूरे देश के स्टेशनों पर दुर्घटनाएं कम होंगी"
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

	// Check if app_name is provided
	if len(os.Args) < 2 {
		log.Fatal("Error: app_name is required\nUsage: go run send_push_notification.go <app_name>\nExample: go run send_push_notification.go dailystory")
	}

	appName := os.Args[1]
	timestamp := time.Now().Format("2006-01-02 15:04:05")

	log.Printf("[%s] Starting push notification for app: %s\n", timestamp, appName)

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

	log.Printf("[%s] Database connected successfully\n", timestamp)

	// Initialize repository and notification client
	userRepo := repository.NewUserRepository(db)
	pushClient := notification.NewExpoPushClient()

	// Find users with push notification tokens
	users, err := userRepo.FindByAppWithPushToken(appName)
	if err != nil {
		log.Fatalf("[%s] ✗ Failed to find users: %v\n", timestamp, err)
	}

	if len(users) == 0 {
		log.Printf("[%s] No users found with push notification tokens for app: %s\n", timestamp, appName)
		os.Exit(0)
	}

	log.Printf("[%s] Found %d users with push notification tokens\n", timestamp, len(users))

	// Prepare notification messages
	var messages []notification.ExpoMessage
	var targetTokens []string

	for _, user := range users {
		if user.Metadata == nil {
			continue
		}

		// Get push notification token from metadata
		token, ok := user.Metadata["push_notification_token"].(string)
		if !ok || token == "" {
			continue
		}

		// Validate token format
		if !notification.ValidatePushToken(token) {
			log.Printf("[%s] Skipping invalid token for user %s\n", timestamp, user.ID)
			continue
		}

		targetTokens = append(targetTokens, token)
		messages = append(messages, notification.ExpoMessage{
			To:    token,
			Title: MessageTitle,
			Body:  MessageBody,
			Data: map[string]interface{}{
				"title": MessageTitle,
				"body":  MessageBody,
			},
			Sound:    "default",
			Priority: "high",
		})
	}

	if len(messages) == 0 {
		log.Printf("[%s] No valid push notification tokens found\n", timestamp)
		os.Exit(0)
	}

	log.Printf("[%s] Sending notifications to %d users\n", timestamp, len(messages))

	// Send notifications in batches (Expo allows max 100 per request)
	const batchSize = 100
	var totalSuccess, totalFailed int
	var errorMessages []string

	for i := 0; i < len(messages); i += batchSize {
		end := i + batchSize
		if end > len(messages) {
			end = len(messages)
		}
		batch := messages[i:end]

		results, err := pushClient.SendBatch(batch)
		if err != nil {
			// If entire batch fails
			totalFailed += len(batch)
			errorMessages = append(errorMessages, fmt.Sprintf("Batch failed: %v", err))
			log.Printf("[%s] Batch failed: %v\n", timestamp, err)
			continue
		}

		// Process individual results
		for j, result := range results {
			if result != nil {
				totalFailed++
				errorMessages = append(errorMessages, fmt.Sprintf("Token %s: %v", targetTokens[i+j], result))
			} else {
				totalSuccess++
			}
		}
	}

	log.Printf("[%s] ✓ Push notifications sent successfully!\n", timestamp)
	log.Printf("[%s]   Success: %d, Failed: %d, Total: %d\n", timestamp, totalSuccess, totalFailed, len(messages))

	if len(errorMessages) > 0 && totalFailed > 0 {
		log.Printf("[%s]   Errors:\n", timestamp)
		for _, errMsg := range errorMessages {
			log.Printf("[%s]     - %s\n", timestamp, errMsg)
		}
	}

	// Exit with success code if at least one notification was sent
	if totalSuccess > 0 {
		os.Exit(0)
	} else if len(messages) == 0 {
		os.Exit(0)
	} else {
		os.Exit(1)
	}
}
