package main

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"go-backend/internal/apps/user/models"
	"go-backend/internal/common/database"
	"go-backend/pkg/notification"
	"go-backend/pkg/utils"

	"github.com/joho/godotenv"
	"golang.org/x/time/rate"
	"gorm.io/gorm"
)

const (
	maxTotal       = 200
	maxPerMinute   = 50
	dailyStoryApp  = "DailyStoryApp"
	countryCode    = "91"
)

type record struct {
	name            string
	phone           string
	messageSent     bool
	rowIndex        int // 1-based index into allRows (skipping header)
	sentThisRun     bool
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "Usage: go run scripts/send_bulk_whatsapp/main.go <input.csv> <output.csv>")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Required env vars:")
		fmt.Fprintln(os.Stderr, "  WHATSAPP_ACCESS_TOKEN     Meta Graph API Bearer token")
		fmt.Fprintln(os.Stderr, "  WHATSAPP_PHONE_NUMBER_ID  WhatsApp Business phone number ID")
		fmt.Fprintln(os.Stderr, "  WHATSAPP_IMAGE_URL        Public image URL for the template header")
		fmt.Fprintln(os.Stderr, "  WHATSAPP_TEMPLATE_NAME    Template name (default: new_poster_ready_notification_image2)")
		fmt.Fprintln(os.Stderr, "  WHATSAPP_LANGUAGE_CODE    Template language code (default: en)")
		os.Exit(1)
	}

	inputCSV := os.Args[1]
	outputCSV := os.Args[2]

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
	log.Printf("input=%s output=%s", inputCSV, outputCSV)

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

	// Load CSV
	allRows, records, err := loadCSV(inputCSV)
	if err != nil {
		log.Fatalf("failed to read CSV: %v", err)
	}
	log.Printf("loaded %d total rows, %d pending sends", len(allRows)-1, len(records))

	// WhatsApp client and rate limiter (50/min, burst=1)
	waClient := notification.NewWhatsAppClient(accessToken, phoneNumberID)
	limiter := rate.NewLimiter(rate.Every(time.Minute/maxPerMinute), 1)
	ctx := context.Background()

	sent := 0
	for _, rec := range records {
		if sent >= maxTotal {
			log.Printf("reached %d message limit, stopping", maxTotal)
			break
		}

		// Skip if phone is not 10 digits
		if len(rec.phone) != 10 {
			log.Printf("SKIP  [%s] phone %q is not 10 digits", rec.name, rec.phone)
			continue
		}

		// Skip if user already exists in DB
		exists, err := userExistsInDB(db, rec.phone)
		if err != nil {
			log.Printf("SKIP  [%s] DB lookup error: %v", rec.name, err)
			continue
		}
		if exists {
			log.Printf("SKIP  [%s] phone %s already registered in %s", rec.name, rec.phone, dailyStoryApp)
			continue
		}

		// Wait for rate limiter token
		if err := limiter.Wait(ctx); err != nil {
			log.Printf("rate limiter error: %v", err)
			break
		}

		to := countryCode + rec.phone
		tmpl := buildTemplate(templateName, languageCode, imageURL, rec.name, todayDate)
		if err := waClient.SendTemplate(to, tmpl); err != nil {
			log.Printf("FAILED [%s] +%s: %v", rec.name, to, err)
			continue
		}

		rec.sentThisRun = true
		sent++
		log.Printf("SENT %d  [%s] +%s", sent, rec.name, to)
	}

	log.Printf("done — sent %d messages", sent)

	if err := writeCSV(outputCSV, allRows, records, templateName); err != nil {
		log.Fatalf("failed to write output CSV: %v", err)
	}
	log.Printf("results written to %s", outputCSV)
}

// buildTemplate constructs the WhatsApp template for new_poster_ready_notification_image2.
func buildTemplate(name, langCode, imageURL, personName, todayDate string) notification.WhatsAppTemplate {
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
					{Type: "text", ParameterName: "name", Text: personName},
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

// userExistsInDB checks whether a user with app_name=DailyStoryApp, country_code="91",
// phone=phone is present in the database.
func userExistsInDB(db *gorm.DB, phone string) (bool, error) {
	var user models.User
	err := db.Where("app_name = ? AND country_code = ? AND phone = ?", dailyStoryApp, countryCode, phone).
		First(&user).Error
	if err == nil {
		return true, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return false, err
}

// loadCSV reads the CSV and returns all raw rows plus a slice of records whose
// Message_Sent is not already "true".
func loadCSV(path string) ([][]string, []*record, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()

	r := csv.NewReader(f)
	allRows, err := r.ReadAll()
	if err != nil {
		return nil, nil, err
	}
	if len(allRows) < 2 {
		return allRows, nil, nil
	}

	header := allRows[0]
	colIdx := make(map[string]int, len(header))
	for i, h := range header {
		colIdx[strings.ToLower(strings.TrimSpace(h))] = i
	}

	nameCol, okN := colIdx["person name"]
	phoneCol, okP := colIdx["phone"]
	sentCol, okS := colIdx["message_sent"]
	if !okN || !okP || !okS {
		return nil, nil, fmt.Errorf("CSV must have 'Person Name', 'Phone', 'Message_Sent' columns (got: %v)", header)
	}

	var records []*record
	for i, row := range allRows[1:] {
		if len(row) <= phoneCol {
			continue
		}

		sentVal := strings.TrimSpace(strings.ToLower(row[sentCol]))
		if sentVal == "true" {
			continue
		}

		phone := strings.TrimSpace(row[phoneCol])
		if phone == "" {
			continue
		}

		name := strings.TrimSpace(row[nameCol])
		if name == "" {
			name = phone
		}

		records = append(records, &record{
			name:        name,
			phone:       phone,
			messageSent: false,
			rowIndex:    i + 1, // offset into allRows (skipping header row)
		})
	}

	return allRows, records, nil
}

// writeCSV writes all original rows to path, patching Message_Sent and
// Message_Template_Name for records that were sent in this run.
func writeCSV(path string, allRows [][]string, records []*record, templateName string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	header := allRows[0]
	colIdx := make(map[string]int, len(header))
	for i, h := range header {
		colIdx[strings.ToLower(strings.TrimSpace(h))] = i
	}
	sentCol := colIdx["message_sent"]
	templateCol := colIdx["message_template_name"]

	// Build lookup rowIndex → record for O(1) patch
	byRow := make(map[int]*record, len(records))
	for _, rec := range records {
		byRow[rec.rowIndex] = rec
	}

	w := csv.NewWriter(f)
	_ = w.Write(header)

	for i, row := range allRows[1:] {
		rowNum := i + 1
		rec, ok := byRow[rowNum]
		if ok && rec.sentThisRun {
			patched := make([]string, len(row))
			copy(patched, row)
			if len(patched) > sentCol {
				patched[sentCol] = "true"
			}
			if len(patched) > templateCol {
				patched[templateCol] = templateName
			}
			_ = w.Write(patched)
		} else {
			_ = w.Write(row)
		}
	}

	w.Flush()
	return w.Error()
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("required env var %s is not set", key)
	}
	return v
}
