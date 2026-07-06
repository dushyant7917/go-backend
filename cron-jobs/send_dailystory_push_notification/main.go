package main

import (
	"context"
	"log"
	"os"
	"time"

	"go-backend/internal/apps/dailystory/inngest"
	"go-backend/internal/apps/user/models"
	"go-backend/internal/apps/user/repository"
	"go-backend/internal/common/database"
	"go-backend/pkg/notification"
	"go-backend/pkg/utils"

	"github.com/inngest/inngestgo"
	"github.com/joho/godotenv"
)

const (
	targetAppName = "DailyStoryApp"
	userAgeDays     = 7
	batchSize       = 10

	// Notification delivery window: now+1h – 9pm IST (3:30pm UTC)
	windowEndHourUTC   = 15
	windowEndMinuteUTC = 30
)

// userMessage pairs a push token with its localized notification message
type userMessage struct {
	token    string
	langCode string
}

// buildUserMessages constructs token+langCode pairs for users with valid push tokens.
func buildUserMessages(users []models.User, timestamp string) []userMessage {
	msgs := make([]userMessage, 0, len(users))

	for _, user := range users {
		if user.Metadata == nil {
			continue
		}

		token, ok := user.Metadata["push_notification_token"].(string)
		if !ok || token == "" {
			continue
		}

		if !notification.ValidatePushToken(token) {
			log.Printf("[%s] Skipping invalid token for user %s\n", timestamp, user.ID)
			continue
		}

		langCode := inngest.DefaultLanguage
		if lc, ok := user.Metadata["language_code"].(string); ok && lc != "" {
			if _, supported := inngest.NotificationMessages[lc]; supported {
				langCode = lc
			}
		}

		msgs = append(msgs, userMessage{token: token, langCode: langCode})
	}

	return msgs
}

// scheduleBatchTimes returns M evenly-spaced times within the now+1h – 9pm IST window.
// If M == 1 the single batch fires at windowStart (now+1h).
func scheduleBatchTimes(m int, today time.Time) []time.Time {
	d := today.UTC()
	windowStart := today.UTC().Add(time.Hour).Truncate(time.Minute)
	windowEnd := time.Date(d.Year(), d.Month(), d.Day(), windowEndHourUTC, windowEndMinuteUTC, 0, 0, time.UTC)

	times := make([]time.Time, m)
	if m == 1 {
		times[0] = windowStart
		return times
	}
	interval := windowEnd.Sub(windowStart) / time.Duration(m-1)
	for i := range times {
		times[i] = windowStart.Add(time.Duration(i) * interval)
	}
	return times
}

// buildBatchEvents divides userMessages into groups of batchSize and returns
// one Inngest event per group with its scheduled delivery time.
func buildBatchEvents(msgs []userMessage, batchTimes []time.Time) []any {
	events := make([]any, 0, len(batchTimes))
	for i, t := range batchTimes {
		start := i * batchSize
		end := min(start+batchSize, len(msgs))
		group := msgs[start:end]

		tokens := make([]string, len(group))
		langCodes := make([]string, len(group))
		for j, m := range group {
			tokens[j] = m.token
			langCodes[j] = m.langCode
		}

		events = append(events, inngest.BatchNotificationEvent{
			Name: inngest.BatchNotificationEventName,
			Data: inngest.BatchNotificationEventData{
				Tokens:      tokens,
				LangCodes:   langCodes,
				ScheduledAt: t,
				BatchIndex:  i,
			},
		})
	}
	return events
}

func main() {
	env := utils.GetEnv("GO_ENV", "local")
	envFile := ".env." + env
	if err := godotenv.Load(envFile); err != nil {
		if err := godotenv.Load(); err != nil {
			log.Printf("No %s or .env file found, using environment variables", envFile)
		}
	}

	timestamp := time.Now().Format("2006-01-02 15:04:05")
	log.Printf("[%s] Starting push notification scheduler for app: %s\n", timestamp, targetAppName)

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
	log.Printf("[%s] Database connected\n", timestamp)

	userRepo := repository.NewUserRepository(db)

	users, err := userRepo.FindUsersEligibleForPushNotification(targetAppName, userAgeDays)
	if err != nil {
		log.Fatalf("[%s] ✗ Failed to find eligible users: %v\n", timestamp, err)
	}

	if len(users) == 0 {
		log.Printf("[%s] No eligible users found\n", timestamp)
		os.Exit(0)
	}
	log.Printf("[%s] Found %d eligible users\n", timestamp, len(users))

	msgs := buildUserMessages(users, timestamp)
	if len(msgs) == 0 {
		log.Printf("[%s] No valid push notification tokens found\n", timestamp)
		os.Exit(0)
	}
	log.Printf("[%s] Scheduling notifications for %d users in batches of %d\n", timestamp, len(msgs), batchSize)

	numBatches := (len(msgs) + batchSize - 1) / batchSize
	batchTimes := scheduleBatchTimes(numBatches, time.Now())
	log.Printf("[%s] %d batches scheduled from %s to %s (IST)\n",
		timestamp, numBatches,
		batchTimes[0].Format("15:04"),
		batchTimes[len(batchTimes)-1].Format("15:04"),
	)

	events := buildBatchEvents(msgs, batchTimes)

	inngestClient, err := inngestgo.NewClient(inngestgo.ClientOpts{
		AppID: utils.GetEnv("INNGEST_APP_ID", "go-backend"),
	})
	if err != nil {
		log.Fatalf("[%s] ✗ Failed to create Inngest client: %v\n", timestamp, err)
	}

	ids, err := inngestClient.SendMany(context.Background(), events)
	if err != nil {
		log.Fatalf("[%s] ✗ Failed to send Inngest events: %v\n", timestamp, err)
	}

	log.Printf("[%s] ✓ Scheduled %d batch events via Inngest\n", timestamp, len(ids))
	for i, id := range ids {
		log.Printf("[%s]   batch %d → event id %s (scheduled at %s IST)\n",
			timestamp, i, id, batchTimes[i].Format("15:04"))
	}

	os.Exit(0)
}
