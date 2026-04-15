package main

import (
	"fmt"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"go-backend/internal/apps/user/models"
	"go-backend/internal/apps/user/repository"
	"go-backend/internal/common/database"
	"go-backend/pkg/notification"
	"go-backend/pkg/utils"

	"github.com/joho/godotenv"
)

// Notification message templates by language code
var notificationMessages = map[string]struct {
	Title string
	Body  string
}{
	"hi": {
		Title: "आपके लिए नए न्यूज़ पोस्टर्स तैयार हैं!",
		Body:  "नए पोस्टर्स शेयर करें और लोगों के बीच वायरल हो जाएं।",
	},
	"gu": {
		Title: "તમારા માટે નવા ન્યૂઝ પોસ્ટર્સ તૈયાર છે!",
		Body:  "નવા પોસ્ટર્સ શેર કરો અને લોકોમાં વાયરલ થઈ જાઓ.",
	},
	"pa": {
		Title: "ਤੁਹਾਡੇ ਲਈ ਨਵੇਂ ਨਿਊਜ਼ ਪੋਸਟਰ ਤਿਆਰ ਹਨ!",
		Body:  "ਨਵੇਂ ਪੋਸਟਰ ਸ਼ੇਅਰ ਕਰੋ ਅਤੇ ਲੋਕਾਂ ਵਿੱਚ ਵਾਇਰਲ ਹੋ ਜਾਓ।",
	},
	"mr": {
		Title: "तुमच्यासाठी नवीन न्यूज पोस्टर्स तयार आहेत!",
		Body:  "नवीन पोस्टर्स शेअर करा आणि लोकांमध्ये व्हायरल व्हा.",
	},
	"bn": {
		Title: "আপনার জন্য নতুন নিউজ পোস্টার তৈরি!",
		Body:  "নতুন পোস্টার শেয়ার করুন এবং মানুষের মধ্যে ভাইরাল হয়ে যান।",
	},
}

// Default language for users without language_code set
const defaultLanguage = "hi"

// userMessage pairs a push token with its localized notification message
type userMessage struct {
	token    string
	message  notification.ExpoMessage
	langCode string
}

// buildUserMessages constructs localized notification messages for users.
func buildUserMessages(users []models.User, timestamp string) []userMessage {
	userMessages := make([]userMessage, 0, len(users))

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

		// Get user's language preference, default to Hindi
		langCode := defaultLanguage
		if lc, ok := user.Metadata["language_code"].(string); ok && lc != "" {
			langCode = lc
		}

		// Get message template
		msgTemplate, exists := notificationMessages[langCode]
		if !exists {
			msgTemplate = notificationMessages[defaultLanguage]
			log.Printf("[%s] Unknown language_code '%s' for user %s, using default\n", timestamp, langCode, user.ID)
		}

		userMessages = append(userMessages, userMessage{
			token:    token,
			langCode: langCode,
			message: notification.ExpoMessage{
				To:       token,
				Title:    msgTemplate.Title,
				Body:     msgTemplate.Body,
				Data:     map[string]interface{}{"title": msgTemplate.Title, "body": msgTemplate.Body},
				Sound:    "default",
				Priority: "high",
			},
		})
	}

	return userMessages
}

// sendBatchNotifications sends notifications in concurrent batches.
func sendBatchNotifications(pushClient *notification.ExpoPushClient, messages []notification.ExpoMessage, targetTokens []string, timestamp string) (int64, int64, []string) {
	const batchSize = 100
	const maxConcurrentBatches = 5

	var totalSuccess, totalFailed int64
	var errorMessages []string
	var errorMutex sync.Mutex
	var wg sync.WaitGroup

	sem := make(chan struct{}, maxConcurrentBatches)

	for i := 0; i < len(messages); i += batchSize {
		end := i + batchSize
		if end > len(messages) {
			end = len(messages)
		}
		batch := messages[i:end]
		batchStartIndex := i

		wg.Add(1)
		go func(b []notification.ExpoMessage, startIndex int) {
			defer wg.Done()

			sem <- struct{}{}
			defer func() { <-sem }()

			results, err := pushClient.SendBatch(b)
			if err != nil {
				atomic.AddInt64(&totalFailed, int64(len(b)))
				errorMutex.Lock()
				errorMessages = append(errorMessages, fmt.Sprintf("Batch failed: %v", err))
				errorMutex.Unlock()
				log.Printf("[%s] Batch failed: %v\n", timestamp, err)
				return
			}

			for j, result := range results {
				if result != nil {
					atomic.AddInt64(&totalFailed, 1)
					errorMutex.Lock()
					errorMessages = append(errorMessages, fmt.Sprintf("Token %s: %v", targetTokens[startIndex+j], result))
					errorMutex.Unlock()
				} else {
					atomic.AddInt64(&totalSuccess, 1)
				}
			}
		}(batch, batchStartIndex)
	}

	wg.Wait()
	return totalSuccess, totalFailed, errorMessages
}

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

	// Build localized notification messages
	userMessages := buildUserMessages(users, timestamp)

	if len(userMessages) == 0 {
		log.Printf("[%s] No valid push notification tokens found\n", timestamp)
		os.Exit(0)
	}

	log.Printf("[%s] Sending notifications to %d users\n", timestamp, len(userMessages))

	// Prepare slices for batch sending
	messages := make([]notification.ExpoMessage, len(userMessages))
	targetTokens := make([]string, len(userMessages))
	messagesByLang := make(map[string]int)

	for i, um := range userMessages {
		messages[i] = um.message
		targetTokens[i] = um.token
		messagesByLang[um.langCode]++
	}

	// Log language distribution
	for lang, count := range messagesByLang {
		log.Printf("[%s] Language %s: %d users\n", timestamp, lang, count)
	}

	// Send notifications concurrently
	totalSuccess, totalFailed, errorMessages := sendBatchNotifications(pushClient, messages, targetTokens, timestamp)

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
	}
	os.Exit(1)
}
