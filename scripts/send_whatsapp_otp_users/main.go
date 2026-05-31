package main

import (
	"context"
	"log"
	"os"
	"time"

	otpModels "go-backend/internal/apps/otp/models"
	"go-backend/internal/apps/user/models"
	"go-backend/internal/common/database"
	"go-backend/pkg/notification"
	"go-backend/pkg/utils"

	"github.com/joho/godotenv"
	"golang.org/x/time/rate"
)

const (
	maxTotal     = 200
	maxPerMinute = 50
	appName      = "DailyStoryApp"
)

type otpCandidate struct {
	CountryCode string
	Phone       string
}

func main() {
	// Load .env file
	env := utils.GetEnv("GO_ENV", "local")
	envFile := ".env." + env
	if err := godotenv.Load(envFile); err != nil {
		if err := godotenv.Load(); err != nil {
			log.Printf("No %s or .env file found, using environment variables", envFile)
		}
	}

	accessToken := mustEnv("WHATSAPP_ACCESS_TOKEN")
	phoneNumberID := mustEnv("WHATSAPP_PHONE_NUMBER_ID")
	imageURL := mustEnv("WHATSAPP_IMAGE_URL")
	templateName := utils.GetEnv("WHATSAPP_TEMPLATE_NAME", "new_poster_ready_notification_image2")
	languageCode := utils.GetEnv("WHATSAPP_LANGUAGE_CODE", "en")
	todayDate := time.Now().Format("2006-01-02")

	log.Printf("template=%s language=%s today=%s", templateName, languageCode, todayDate)

	// Connect to DB
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
		log.Fatalf("failed to connect to database: %v", err)
	}

	// Fetch distinct (country_code, phone) from phone_otp where app_name=DailyStoryApp
	// and no matching row exists in users with the same app_name + country_code + phone.
	var candidates []otpCandidate
	err = db.Model(&otpModels.PhoneOTP{}).
		Select("DISTINCT country_code, phone").
		Where("app_name = ?", appName).
		Where("NOT EXISTS (?)",
			db.Model(&models.User{}).
				Select("1").
				Where("app_name = ? AND country_code = phone_otp.country_code AND phone = phone_otp.phone AND deleted_at IS NULL", appName),
		).
		Scan(&candidates).Error
	if err != nil {
		log.Fatalf("failed to query candidates: %v", err)
	}
	log.Printf("found %d candidates (in OTP, not in users)", len(candidates))

	waClient := notification.NewWhatsAppClient(accessToken, phoneNumberID)
	limiter := rate.NewLimiter(rate.Every(time.Minute/maxPerMinute), 1)
	ctx := context.Background()

	sent := 0
	for _, c := range candidates {
		if sent >= maxTotal {
			log.Printf("reached %d message limit, stopping", maxTotal)
			break
		}

		if err := limiter.Wait(ctx); err != nil {
			log.Printf("rate limiter error: %v", err)
			break
		}

		to := c.CountryCode + c.Phone
		tmpl := buildTemplate(templateName, languageCode, imageURL, todayDate)
		if err := waClient.SendTemplate(to, tmpl); err != nil {
			log.Printf("FAILED +%s: %v", to, err)
			continue
		}

		sent++
		log.Printf("SENT %d  +%s", sent, to)
	}

	log.Printf("done — sent %d messages", sent)
}

// buildTemplate constructs the WhatsApp template. No name is available from
// the OTP table so the body name parameter is sent as an empty string.
func buildTemplate(name, langCode, imageURL, todayDate string) notification.WhatsAppTemplate {
	return notification.WhatsAppTemplate{
		Name:     name,
		Language: notification.TemplateLanguage{Code: langCode},
		Components: []notification.TemplateComponent{
			{
				Type: "header",
				Parameters: []notification.TemplateParameter{
					{Type: "image", Image: &notification.TemplateImage{Link: imageURL}},
				},
			},
			{
				Type: "body",
				Parameters: []notification.TemplateParameter{
					{Type: "text", ParameterName: "name", Text: ""},
				},
			},
			{
				Type:    "button",
				SubType: "url",
				Index:   "0",
				Parameters: []notification.TemplateParameter{
					{Type: "text", Text: todayDate},
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

