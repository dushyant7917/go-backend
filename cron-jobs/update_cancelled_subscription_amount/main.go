package main

import (
	"log"
	"time"

	userModels "go-backend/internal/apps/user/models"
	"go-backend/internal/common/database"
	"go-backend/pkg/utils"

	"github.com/google/uuid"
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
	log.Printf("[%s] Starting subscription amount update for users with cancelled subscriptions (no active)\n", timestamp)

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

	// Query for users who have at least one cancelled subscription but no active subscription
	// Using a raw SQL query for efficiency
	var results []struct {
		UserID                uuid.UUID
		Phone                 string
		PushNotificationToken *string
		Category              *string
		CancelledCount        int
	}

	query := `
		SELECT DISTINCT
			u.id as user_id,
			u.phone,
			u.metadata->>'push_notification_token' as push_notification_token,
			u.metadata->>'category' as category,
			COUNT(s.id) OVER (PARTITION BY u.id) as cancelled_count
		FROM users u
		INNER JOIN subscriptions s ON u.id = s.user_id AND s.app_name = 'DailyStoryApp'
		WHERE u.app_name = 'DailyStoryApp'
		AND s.status = 'cancelled'
		AND NOT EXISTS (
			SELECT 1
			FROM subscriptions s2
			WHERE s2.user_id = u.id
			AND s2.app_name = 'DailyStoryApp'
			AND s2.status = 'active'
		)
		AND u.phone IS NOT NULL
		AND u.deleted_at IS NULL
		AND u.created_at >= NOW() - INTERVAL '2 days'
		ORDER BY u.phone
	`

	if err := db.Raw(query).Scan(&results).Error; err != nil {
		log.Fatalf("[%s] ✗ Failed to query users: %v\n", timestamp, err)
	}

	if len(results) == 0 {
		log.Printf("[%s] ✓ No users found with cancelled subscriptions (no active)\n", timestamp)
		return
	}

	log.Printf("[%s] Found %d users with cancelled subscriptions (no active):\n", timestamp, len(results))
	log.Printf("[%s] ==================================================================================\n", timestamp)

	// Print results in a formatted way
	for i, result := range results {
		log.Printf("[%s] User %d:\n", timestamp, i+1)
		log.Printf("[%s]   User ID: %s\n", timestamp, result.UserID)
		log.Printf("[%s]   Phone: %s\n", timestamp, result.Phone)

		if result.PushNotificationToken != nil && *result.PushNotificationToken != "" {
			log.Printf("[%s]   Push Notification Token: %s\n", timestamp, *result.PushNotificationToken)
		} else {
			log.Printf("[%s]   Push Notification Token: (not set)\n", timestamp)
		}

		if result.Category != nil && *result.Category != "" {
			log.Printf("[%s]   Category: %s\n", timestamp, *result.Category)
		} else {
			log.Printf("[%s]   Category: (not set)\n", timestamp)
		}

		log.Printf("[%s]   Cancelled Subscription Count: %d\n", timestamp, result.CancelledCount)
		log.Printf("[%s] ==================================================================================\n", timestamp)
	}

	log.Printf("[%s] Total users found: %d\n", timestamp, len(results))

	// Update user metadata for these users
	log.Printf("[%s] \n", timestamp)
	log.Printf("[%s] Updating user metadata with subscription_plan_id and subscription_amount...\n", timestamp)
	log.Printf("[%s] ==================================================================================\n", timestamp)

	const targetPlanID = "plan_SIk4ZMNqGdrnD4"
	const targetAmount = 99 // 99 rupees

	for i, result := range results {
		// Get the user
		var user userModels.User
		if err := db.First(&user, "id = ?", result.UserID).Error; err != nil {
			log.Printf("[%s] ✗ Failed to get user %s: %v\n", timestamp, result.UserID, err)
			continue
		}

		// Update metadata fields
		if user.Metadata == nil {
			user.Metadata = make(utils.Metadata)
		}
		user.Metadata["subscription_plan_id"] = targetPlanID
		user.Metadata["subscription_amount"] = targetAmount

		// Save the user
		if err := db.Save(&user).Error; err != nil {
			log.Printf("[%s] ✗ Failed to update user metadata for user %s: %v\n", timestamp, result.UserID, err)
			continue
		}

		log.Printf("[%s] ✓ User %d (Phone: %s):\n", timestamp, i+1, result.Phone)
		log.Printf("[%s]   Set metadata.subscription_plan_id=%s\n", timestamp, targetPlanID)
		log.Printf("[%s]   Set metadata.subscription_amount=%d INR\n", timestamp, targetAmount)
	}

	log.Printf("[%s] ==================================================================================\n", timestamp)
	log.Printf("[%s] ✓ User metadata updates completed\n", timestamp)

	log.Printf("[%s] ✓ Cancelled subscription amount update completed successfully\n", timestamp)
}
