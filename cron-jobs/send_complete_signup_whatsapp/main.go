package main

import (
	"context"
	"log"
	"math/rand"
	"os"
	"time"

	"go-backend/internal/apps/dailystory/inngest"
	"go-backend/internal/common/database"
	"go-backend/pkg/notification"
	"go-backend/pkg/utils"

	"github.com/inngest/inngestgo"
	"github.com/joho/godotenv"
)

const (
	maxTotal    = 245
	countryCode = "91"

	fanOutDelay  = 1 * time.Hour
	fanOutWindow = 12 * time.Hour

	templateName = "complete_sign_up_hi"
	templateLang = "hi"
	imageSuffix  = "poster_demo_design1_en.png"
)

func main() {
	env := utils.GetEnv("GO_ENV", "local")
	envFile := ".env." + env
	if err := godotenv.Load(envFile); err != nil {
		if err := godotenv.Load(); err != nil {
			log.Printf("No %s or .env file found, using environment variables", envFile)
		}
	}

	imageBaseURL := mustEnv("WHATSAPP_IMAGE_BASE_URL")
	template := buildTemplate(imageBaseURL)

	db, err := database.NewCronConnection(database.Config{
		Host:     utils.GetEnv("DB_HOST", "localhost"),
		Port:     utils.GetEnv("DB_PORT", "5432"),
		User:     utils.GetEnv("DB_USER", "postgres"),
		Password: utils.GetEnv("DB_PASSWORD", ""),
		DBName:   utils.GetEnv("DB_NAME", "gobackend"),
		SSLMode:  utils.GetEnv("DB_SSL_MODE", "disable"),
	})
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}

	now := time.Now().UTC()
	from := now.Add(-12 * time.Hour)
	to := now.Add(-1 * time.Hour)

	var phones []string
	err = db.Raw(`
		SELECT DISTINCT p.phone
		FROM phone_otps p
		WHERE p.created_at >= ?
		  AND p.created_at <= ?
		  AND p.country_code = ?
		  AND p.app_name = 'DailyStoryApp'
		  AND NOT EXISTS (
		      SELECT 1 FROM users u
		      WHERE u.phone = p.phone
		        AND u.app_name = 'DailyStoryApp'
		        AND u.deleted_at IS NULL
		  )
	`, from, to, countryCode).Scan(&phones).Error
	if err != nil {
		log.Fatalf("failed to query phone numbers: %v", err)
	}
	log.Printf("found %d pending phones", len(phones))

	if len(phones) == 0 {
		log.Println("no pending phones, nothing to do")
		return
	}

	if len(phones) > maxTotal {
		log.Printf("capping at %d (total pending: %d)", maxTotal, len(phones))
		phones = phones[:maxTotal]
	}

	inngestClient, err := inngestgo.NewClient(inngestgo.ClientOpts{
		AppID: utils.GetEnv("INNGEST_APP_ID", "go-backend"),
	})
	if err != nil {
		log.Fatalf("failed to create Inngest client: %v", err)
	}

	windowStart := now.Add(fanOutDelay)

	events := make([]any, len(phones))
	for i, phone := range phones {
		offset := time.Duration(rand.Int63n(int64(fanOutWindow)))
		scheduledAt := windowStart.Add(offset)

		events[i] = inngest.WhatsAppMessageEvent{
			Name: inngest.WhatsAppMessageEventName,
			Data: inngest.WhatsAppMessageEventData{
				Phone:       countryCode + phone,
				Template:    template,
				ScheduledAt: scheduledAt,
			},
		}

		log.Printf("scheduled +%s%s at %s UTC", countryCode, phone, scheduledAt.Format("15:04:05"))
	}

	ids, err := inngestClient.SendMany(context.Background(), events)
	if err != nil {
		log.Fatalf("failed to send Inngest events: %v", err)
	}
	log.Printf("scheduled %d events via Inngest", len(ids))
}

func buildTemplate(imageBaseURL string) notification.WhatsAppTemplate {
	return notification.WhatsAppTemplate{
		Name:     templateName,
		Language: notification.TemplateLanguage{Code: templateLang},
		Components: []notification.TemplateComponent{
			{
				Type: "header",
				Parameters: []notification.TemplateParameter{
					{Type: "image", Image: &notification.TemplateImage{Link: imageBaseURL + "/" + imageSuffix}},
				},
			},
		},
	}
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("required env var %s is not set", key)
	}
	return v
}

