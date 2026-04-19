package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"

	configRepo "go-backend/internal/apps/razorpay/config/repository"
	"go-backend/internal/common/database"
	"go-backend/pkg/utils"

	"github.com/joho/godotenv"
	"github.com/razorpay/razorpay-go"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: go run scripts/check_token_status/main.go <token_id> [customer_id]\n")
		fmt.Fprintf(os.Stderr, "\nIf customer_id is not provided, it will be looked up from the recurring_payments table.\n")
		os.Exit(1)
	}

	tokenID := os.Args[1]

	// Load environment variables from appropriate file
	env := utils.GetEnv("GO_ENV", "local")
	envFile := ".env." + env
	if err := godotenv.Load(envFile); err != nil {
		if err := godotenv.Load(); err != nil {
			log.Printf("No %s or .env file found, using environment variables", envFile)
		}
	}

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
		log.Fatalf("Failed to connect to database: %v", err)
	}
	log.Println("Database connected successfully")

	// Fetch Razorpay config for DailyStoryApp and current environment
	razorpayConfigRepo := configRepo.NewRazorpayConfigRepository(db)
	rpEnv := utils.GetRazorpayEnvironment()
	config, err := razorpayConfigRepo.FindByAppNameAndEnv("DailyStoryApp", rpEnv)
	if err != nil {
		log.Fatalf("Failed to find razorpay config for DailyStoryApp (%s): %v", rpEnv, err)
	}

	if !config.IsActive {
		log.Fatalf("Razorpay config for DailyStoryApp (%s) is not active", rpEnv)
	}

	log.Printf("Using Razorpay config: app_name=%s, env=%s", config.AppName, config.Environment)

	// Resolve customer_id
	var customerID string
	if len(os.Args) >= 3 {
		customerID = os.Args[2]
	} else {
		// Look up customer_id from recurring_payments table by token_id
		var cid *string
		result := db.Table("recurring_payments").
			Select("razorpay_customer_id").
			Where("token_id = ? AND deleted_at IS NULL", tokenID).
			Limit(1).
			Scan(&cid)
		if result.Error != nil {
			log.Fatalf("Failed to query recurring_payments for token_id=%s: %v", tokenID, result.Error)
		}
		if cid == nil || *cid == "" {
			log.Fatalf("No recurring payment found with token_id=%s. Provide customer_id as second argument.", tokenID)
		}
		customerID = *cid
		log.Printf("Found customer_id=%s for token_id=%s", customerID, tokenID)
	}

	// Create Razorpay client
	client := razorpay.NewClient(config.RazorpayKeyID, config.RazorpayKeySecret)

	// Fetch token details from Razorpay
	tokenData, err := client.Token.Fetch(customerID, tokenID, nil, nil)
	if err != nil {
		log.Fatalf("Failed to fetch token from Razorpay: %v", err)
	}

	// Pretty-print the full token response
	prettyJSON, err := json.MarshalIndent(tokenData, "", "  ")
	if err != nil {
		log.Fatalf("Failed to format token response: %v", err)
	}
	fmt.Printf("\nToken Details:\n%s\n", string(prettyJSON))

	// Extract and display key status info
	fmt.Println("\n--- Summary ---")
	fmt.Printf("Token ID:     %s\n", tokenID)
	fmt.Printf("Customer ID:  %s\n", customerID)

	if id, ok := tokenData["id"].(string); ok {
		fmt.Printf("ID:           %s\n", id)
	}
	if method, ok := tokenData["method"].(string); ok {
		fmt.Printf("Method:       %s\n", method)
	}
	if recurring, ok := tokenData["recurring"].(bool); ok {
		fmt.Printf("Recurring:    %v\n", recurring)
	}
	if recurringDetails, ok := tokenData["recurring_details"].(map[string]interface{}); ok {
		if status, ok := recurringDetails["status"].(string); ok {
			fmt.Printf("Token Status: %s\n", status)
		}
		if maxAmount, ok := recurringDetails["max_amount"].(float64); ok {
			fmt.Printf("Max Amount:   ₹%.2f\n", maxAmount/100)
		}
		if frequency, ok := recurringDetails["frequency"].(string); ok {
			fmt.Printf("Frequency:    %s\n", frequency)
		}
	}
}
