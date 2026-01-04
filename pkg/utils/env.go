package utils

import "os"

// GetEnv retrieves environment variable or returns default value
func GetEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// GetRazorpayEnvironment returns the Razorpay environment (test/live) based on GO_ENV
// GO_ENV=prod or production → live
// Any other value → test
func GetRazorpayEnvironment() string {
	goEnv := os.Getenv("GO_ENV")
	if goEnv == "prod" || goEnv == "production" {
		return "live"
	}
	return "test"
}
