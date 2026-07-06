package main

import (
	"encoding/csv"
	"fmt"
	"log"
	"os"
	"strings"

	"go-backend/internal/apps/user/models"
	"go-backend/internal/common/database"
	"go-backend/pkg/utils"

	"github.com/joho/godotenv"
	"gorm.io/gorm"
)

const (
	dailyStoryApp = "DailyStoryApp"
	countryCode   = "91"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: go run scripts/count_whatsapp_conversions/main.go <input.csv>")
		os.Exit(1)
	}

	inputCSV := os.Args[1]

	env := utils.GetEnv("GO_ENV", "local")
	envFile := ".env." + env
	if err := godotenv.Load(envFile); err != nil {
		if err := godotenv.Load(); err != nil {
			log.Printf("No %s or .env file found, using environment variables", envFile)
		}
	}

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

	phones, err := loadSentPhones(inputCSV)
	if err != nil {
		log.Fatalf("failed to read CSV: %v", err)
	}
	log.Printf("found %d rows with message_sent=true", len(phones))

	inDB, err := countRegisteredUsers(db, phones)
	if err != nil {
		log.Fatalf("DB lookup error: %v", err)
	}
	notInDB := len(phones) - inDB

	fmt.Printf("\nResults for app_name=%s, country_code=%s\n", dailyStoryApp, countryCode)
	fmt.Printf("  Registered in users table     : %d\n", inDB)
	fmt.Printf("  Not registered in users table : %d\n", notInDB)
	fmt.Printf("  Total with message_sent=true  : %d\n", len(phones))
}

// loadSentPhones reads the CSV and returns phones where Message_Sent is "true".
func loadSentPhones(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := csv.NewReader(f)
	allRows, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(allRows) < 2 {
		return nil, nil
	}

	header := allRows[0]
	colIdx := make(map[string]int, len(header))
	for i, h := range header {
		colIdx[strings.ToLower(strings.TrimSpace(h))] = i
	}

	phoneCol, okP := colIdx["phone"]
	sentCol, okS := colIdx["message_sent"]
	if !okP || !okS {
		return nil, fmt.Errorf("CSV must have 'phone' and 'message_sent' columns (got: %v)", header)
	}

	var phones []string
	for _, row := range allRows[1:] {
		if len(row) <= phoneCol {
			continue
		}
		sentVal := strings.TrimSpace(strings.ToLower(row[sentCol]))
		if sentVal != "true" {
			continue
		}
		phone := strings.TrimSpace(row[phoneCol])
		if phone != "" {
			phones = append(phones, phone)
		}
	}
	return phones, nil
}

func countRegisteredUsers(db *gorm.DB, phones []string) (int, error) {
	var count int64
	err := db.Model(&models.User{}).
		Where("app_name = ? AND country_code = ? AND phone IN ?", dailyStoryApp, countryCode, phones).
		Count(&count).Error
	return int(count), err
}
