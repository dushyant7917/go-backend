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
		fmt.Fprintf(os.Stderr, "Usage: go run scripts/fetch_token_by_payment/main.go <payment_id>\n")
		fmt.Fprintf(os.Stderr, "\nFetches token details from Razorpay using a payment ID.\n")
		fmt.Fprintf(os.Stderr, "Ref: https://razorpay.com/docs/api/payments/recurring-payments/upi/tokens/#21-fetch-token-by-payment-id\n")
		os.Exit(1)
	}

	paymentID := os.Args[1]

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

	db, err := database.NewCronConnection(dbConfig)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	log.Println("Database connected successfully")

	// Fetch Razorpay config for DailyStoryApp and current environment
	razorpayConfigRepo := configRepo.NewRazorpayConfigRepository(db)
	rpEnv := utils.GetEnv("GO_ENV", "local")
	config, err := razorpayConfigRepo.FindByAppNameAndEnv("DailyStoryApp", rpEnv)
	if err != nil {
		log.Fatalf("Failed to find razorpay config for DailyStoryApp (%s): %v", rpEnv, err)
	}

	if !config.IsActive {
		log.Fatalf("Razorpay config for DailyStoryApp (%s) is not active", rpEnv)
	}

	log.Printf("Using Razorpay config: app_name=%s, env=%s", config.AppName, config.Environment)

	// Create Razorpay client
	client := razorpay.NewClient(config.RazorpayKeyID, config.RazorpayKeySecret)

	// Fetch payment details from Razorpay (GET /payments/:id)
	// This returns the payment entity which includes token_id
	paymentData, err := client.Payment.Fetch(paymentID, nil, nil)
	if err != nil {
		log.Fatalf("Failed to fetch payment from Razorpay: %v", err)
	}

	// Pretty-print the full payment response
	prettyJSON, err := json.MarshalIndent(paymentData, "", "  ")
	if err != nil {
		log.Fatalf("Failed to format payment response: %v", err)
	}
	fmt.Printf("\nPayment Details:\n%s\n", string(prettyJSON))

	// Extract and display key token-related fields
	fmt.Println("\n--- Token Summary ---")
	fmt.Printf("Payment ID:   %s\n", paymentID)

	if id, ok := paymentData["id"].(string); ok {
		fmt.Printf("ID:           %s\n", id)
	}
	if entity, ok := paymentData["entity"].(string); ok {
		fmt.Printf("Entity:       %s\n", entity)
	}
	if status, ok := paymentData["status"].(string); ok {
		fmt.Printf("Status:       %s\n", status)
	}
	if method, ok := paymentData["method"].(string); ok {
		fmt.Printf("Method:       %s\n", method)
	}
	if amount, ok := paymentData["amount"].(float64); ok {
		fmt.Printf("Amount:       ₹%.2f\n", amount/100)
	}
	if currency, ok := paymentData["currency"].(string); ok {
		fmt.Printf("Currency:     %s\n", currency)
	}
	if tokenID, ok := paymentData["token_id"].(string); ok {
		fmt.Printf("Token ID:     %s\n", tokenID)
	} else {
		fmt.Println("Token ID:     (not found in payment)")
	}
	if customerID, ok := paymentData["customer_id"].(string); ok {
		fmt.Printf("Customer ID:  %s\n", customerID)
	}
	if orderID, ok := paymentData["order_id"].(string); ok {
		fmt.Printf("Order ID:     %s\n", orderID)
	}
	if captured, ok := paymentData["captured"].(bool); ok {
		fmt.Printf("Captured:     %v\n", captured)
	}
	if createdAt, ok := paymentData["created_at"].(float64); ok {
		fmt.Printf("Created At:   %.0f (unix)\n", createdAt)
	}

	// If token_id is present, also fetch the full token details
	if tokenID, ok := paymentData["token_id"].(string); ok && tokenID != "" {
		if customerID, ok := paymentData["customer_id"].(string); ok && customerID != "" {
			fmt.Println("\n--- Fetching Full Token Details ---")
			tokenData, err := client.Token.Fetch(customerID, tokenID, nil, nil)
			if err != nil {
				log.Printf("Warning: Failed to fetch token details: %v", err)
			} else {
				tokenJSON, err := json.MarshalIndent(tokenData, "", "  ")
				if err != nil {
					log.Printf("Warning: Failed to format token response: %v", err)
				} else {
					fmt.Printf("\nToken Details:\n%s\n", string(tokenJSON))
				}

				// Show recurring_details if present
				if recurringDetails, ok := tokenData["recurring_details"].(map[string]interface{}); ok {
					fmt.Println("\n--- Recurring Details ---")
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
		}
	}
}
