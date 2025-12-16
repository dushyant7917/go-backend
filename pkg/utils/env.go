package utils

import "os"

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
