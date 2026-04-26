package utils

import "os"

// GetEnv retrieves environment variable or returns default value
func GetEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
