package main

import (
	"context"
	"encoding/csv"
	"fmt"
	"log"
	"math/rand"
	"os"
	"strings"
	"time"

	"go-backend/internal/apps/dailystory/inngest"
	"go-backend/pkg/notification"
	"go-backend/pkg/utils"

	"github.com/inngest/inngestgo"
	"github.com/joho/godotenv"
)

const (
	maxTotal    = 150
	countryCode = "91"

	fanOutDelay  = 1 * time.Hour
	fanOutWindow = 12 * time.Hour
)

// southIndianLangCodes is the set of language codes that map to the English template.
var southIndianLangCodes = map[string]bool{
	"en": true,
	"te": true,
	"ta": true,
	"ml": true,
	"kn": true,
}

type record struct {
	phone    string
	rowIndex int // 1-based index into allRows (skipping header)
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: go run scripts/send_scheduled_whatsapp/main.go <csv_path>")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Required env vars:")
		fmt.Fprintln(os.Stderr, "  WHATSAPP_IMAGE_BASE_URL  Base URL for template images (e.g. https://cdn.example.com)")
		fmt.Fprintln(os.Stderr, "  INNGEST_APP_ID        Inngest application ID (default: go-backend)")
		fmt.Fprintln(os.Stderr, "  INNGEST_EVENT_KEY     Inngest event API key")
		os.Exit(1)
	}

	csvPath := os.Args[1]

	env := utils.GetEnv("GO_ENV", "local")
	envFile := ".env." + env
	if err := godotenv.Load(envFile); err != nil {
		if err := godotenv.Load(); err != nil {
			log.Printf("No %s or .env file found, using environment variables", envFile)
		}
	}

	imageBaseURL := mustEnv("WHATSAPP_IMAGE_BASE_URL")

	allRows, records, langCodes, err := loadCSV(csvPath)
	if err != nil {
		log.Fatalf("failed to read CSV: %v", err)
	}
	log.Printf("loaded %d total rows, %d pending sends", len(allRows)-1, len(records))

	if len(records) == 0 {
		log.Println("no pending rows, nothing to do")
		return
	}

	if len(records) > maxTotal {
		log.Printf("capping at %d (total pending: %d)", maxTotal, len(records))
		records = records[:maxTotal]
		langCodes = langCodes[:maxTotal]
	}

	inngestClient, err := inngestgo.NewClient(inngestgo.ClientOpts{
		AppID: utils.GetEnv("INNGEST_APP_ID", "go-backend"),
	})
	if err != nil {
		log.Fatalf("failed to create Inngest client: %v", err)
	}

	now := time.Now().UTC()
	windowStart := now.Add(fanOutDelay)

	events := make([]any, len(records))
	for i, rec := range records {
		offset := time.Duration(rand.Int63n(int64(fanOutWindow)))
		scheduledAt := windowStart.Add(offset)

		events[i] = inngest.WhatsAppMessageEvent{
			Name: inngest.WhatsAppMessageEventName,
			Data: inngest.WhatsAppMessageEventData{
				Phone:       countryCode + rec.phone,
				Template:    buildAreaTemplate(langCodes[i], imageBaseURL),
				ScheduledAt: scheduledAt,
			},
		}

		log.Printf("scheduled +%s%s (lang=%s) at %s UTC",
			countryCode, rec.phone, langCodes[i], scheduledAt.Format("15:04:05"))
	}

	ids, err := inngestClient.SendMany(context.Background(), events)
	if err != nil {
		log.Fatalf("failed to send Inngest events: %v", err)
	}
	log.Printf("scheduled %d events via Inngest", len(ids))

	if err := writeCSV(csvPath, allRows, records); err != nil {
		log.Fatalf("failed to write output CSV: %v", err)
	}
	log.Printf("CSV updated — marked %d rows as message_sent=true", len(records))
}

// buildAreaTemplate constructs the WhatsApp template based on the user's language code.
// South Indian languages and English use the English template; all others use Hindi.
// The image URL is derived as imageBaseURL/area_poster_announcement_<waLangCode>.png.
func buildAreaTemplate(langCode, imageBaseURL string) notification.WhatsAppTemplate {
	templateName := "area_update_hi"
	waLangCode := "hi"
	if southIndianLangCodes[langCode] {
		templateName = "area_update_en"
		waLangCode = "en"
	}
	imageURL := imageBaseURL + "/area_poster_announcement_" + waLangCode + ".png"
	return notification.WhatsAppTemplate{
		Name:     templateName,
		Language: notification.TemplateLanguage{Code: waLangCode},
		Components: []notification.TemplateComponent{
			{
				Type: "header",
				Parameters: []notification.TemplateParameter{
					{Type: "image", Image: &notification.TemplateImage{Link: imageURL}},
				},
			},
		},
	}
}

// loadCSV reads the CSV and returns all raw rows, the pending records (message_sent != "true"),
// and the parallel slice of language codes for those records.
func loadCSV(path string) (allRows [][]string, records []record, langCodes []string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, nil, err
	}
	defer f.Close()

	r := csv.NewReader(f)
	allRows, err = r.ReadAll()
	if err != nil {
		return nil, nil, nil, err
	}
	if len(allRows) < 2 {
		return allRows, nil, nil, nil
	}

	header := allRows[0]
	colIdx := make(map[string]int, len(header))
	for i, h := range header {
		colIdx[strings.ToLower(strings.TrimSpace(h))] = i
	}

	phoneCol, okP := colIdx["user_phone"]
	sentCol, okS := colIdx["message_sent"]
	langCol, okL := colIdx["language_code"]
	if !okP || !okS || !okL {
		return nil, nil, nil, fmt.Errorf("CSV must have 'user_phone', 'message_sent', 'language_code' columns (got: %v)", header)
	}

	for i, row := range allRows[1:] {
		if len(row) <= phoneCol {
			continue
		}

		sentVal := strings.TrimSpace(strings.ToLower(row[sentCol]))
		if sentVal == "true" {
			continue
		}

		phone := strings.TrimSpace(row[phoneCol])
		if len(phone) != 10 {
			log.Printf("SKIP row %d: phone %q is not 10 digits", i+2, phone)
			continue
		}

		lang := strings.TrimSpace(strings.ToLower(row[langCol]))
		if lang == "" {
			lang = "hi"
		}

		records = append(records, record{phone: phone, rowIndex: i + 1})
		langCodes = append(langCodes, lang)
	}

	return allRows, records, langCodes, nil
}

// writeCSV overwrites path with all original rows, patching message_sent=true
// for every record that was scheduled in this run.
func writeCSV(path string, allRows [][]string, scheduled []record) error {
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

	byRow := make(map[int]bool, len(scheduled))
	for _, rec := range scheduled {
		byRow[rec.rowIndex] = true
	}

	w := csv.NewWriter(f)
	_ = w.Write(header)

	for i, row := range allRows[1:] {
		rowNum := i + 1
		if byRow[rowNum] {
			patched := make([]string, len(row))
			copy(patched, row)
			if len(patched) > sentCol {
				patched[sentCol] = "true"
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
