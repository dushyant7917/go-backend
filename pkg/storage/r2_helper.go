package storage

import (
	"fmt"
	"os"
)

// NewR2ClientFromEnv creates a new R2 client using environment variables
//
// Required environment variables:
//   - R2_ACCOUNT_ID: Cloudflare account ID
//   - R2_ACCESS_KEY_ID: R2 access key ID
//   - R2_SECRET_ACCESS_KEY: R2 secret access key
func NewR2ClientFromEnv() (*R2Client, error) {
	accountID := os.Getenv("R2_ACCOUNT_ID")
	accessKeyID := os.Getenv("R2_ACCESS_KEY_ID")
	secretAccessKey := os.Getenv("R2_SECRET_ACCESS_KEY")

	if accountID == "" {
		return nil, fmt.Errorf("R2_ACCOUNT_ID environment variable is required")
	}
	if accessKeyID == "" {
		return nil, fmt.Errorf("R2_ACCESS_KEY_ID environment variable is required")
	}
	if secretAccessKey == "" {
		return nil, fmt.Errorf("R2_SECRET_ACCESS_KEY environment variable is required")
	}

	cfg := R2Config{
		AccountID:       accountID,
		AccessKeyID:     accessKeyID,
		SecretAccessKey: secretAccessKey,
	}

	return NewR2Client(cfg)
}
