package main

import (
	"fmt"
	"log"
	"time"

	"go-backend/internal/common/database"
	"go-backend/pkg/notification"
	"go-backend/pkg/utils"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
)

// Configuration - Update these values as needed
const (
	MessageTitle = "सिर्फ आपके लिए खास छूट"
	MessageBody  = "सिर्फ ₹99 में Daily Story App इस्तेमाल करें।"
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
	log.Printf("[%s] Starting push notification for unauthenticated subscriptions\n", timestamp)

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

	// Query for users who have subscription records but never had authenticated subscription
	var results []struct {
		UserID                uuid.UUID
		Phone                 string
		PushNotificationToken *string
	}

	query := `
		SELECT DISTINCT
			u.id as user_id,
			u.phone,
			u.metadata->>'push_notification_token' as push_notification_token
		FROM users u
		INNER JOIN subscriptions s ON u.id = s.user_id AND s.app_name = 'DailyStoryApp'
		WHERE u.app_name = 'DailyStoryApp'
		AND NOT EXISTS (
			SELECT 1
			FROM subscriptions s2
			WHERE s2.user_id = u.id
			AND s2.app_name = 'DailyStoryApp'
			AND (s2.metadata->>'authenticated_at') IS NOT NULL
		)
		AND u.phone IS NOT NULL
		AND u.deleted_at IS NULL
		AND u.metadata ? 'push_notification_token'
		ORDER BY u.phone
	`

	if err := db.Raw(query).Scan(&results).Error; err != nil {
		log.Fatalf("[%s] ✗ Failed to query users: %v\n", timestamp, err)
	}

	if len(results) == 0 {
		log.Printf("[%s] ✓ No users found with unauthenticated subscriptions and push tokens\n", timestamp)
		return
	}

	log.Printf("[%s] Found %d users with push notification tokens\n", timestamp, len(results))

	// Initialize push notification client
	pushClient := notification.NewExpoPushClient()

	// Prepare notification messages
	var messages []notification.ExpoMessage
	var targetTokens []string

	for _, result := range results {
		if result.PushNotificationToken == nil || *result.PushNotificationToken == "" {
			continue
		}

		token := *result.PushNotificationToken

		// Validate token format
		if !notification.ValidatePushToken(token) {
			log.Printf("[%s] Skipping invalid token for user %s\n", timestamp, result.UserID)
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
		return
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

	log.Printf("[%s] ✓ Push notifications sent!\n", timestamp)
	log.Printf("[%s]   Success: %d, Failed: %d, Total: %d\n", timestamp, totalSuccess, totalFailed, len(messages))

	if len(errorMessages) > 0 && totalFailed > 0 {
		log.Printf("[%s]   Errors:\n", timestamp)
		for _, errMsg := range errorMessages {
			log.Printf("[%s]     - %s\n", timestamp, errMsg)
		}
	}

	log.Printf("[%s] ✓ Notification process completed successfully\n", timestamp)
}
