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
//
// Optional environment variables for public bucket URLs:
//   - R2_DS_TEMPLATES_PUBLIC_URL: Public URL for templates bucket
//   - R2_DS_POSTERS_PUBLIC_URL: Public URL for posters bucket
//   - R2_DS_TEMPLATES_BUCKET_NAME: Templates bucket name
//   - R2_DS_POSTERS_BUCKET_NAME: Posters bucket name
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

	// Build bucket public URLs map
	bucketPublicURLs := make(map[string]string)

	// Templates bucket
	if templatesURL := os.Getenv("R2_DS_TEMPLATES_PUBLIC_URL"); templatesURL != "" {
		if bucketName := os.Getenv("R2_DS_TEMPLATES_BUCKET_NAME"); bucketName != "" {
			bucketPublicURLs[bucketName] = templatesURL
		}
	}

	// Posters bucket
	if postersURL := os.Getenv("R2_DS_POSTERS_PUBLIC_URL"); postersURL != "" {
		if bucketName := os.Getenv("R2_DS_POSTERS_BUCKET_NAME"); bucketName != "" {
			bucketPublicURLs[bucketName] = postersURL
		}
	}

	cfg := R2Config{
		AccountID:        accountID,
		AccessKeyID:      accessKeyID,
		SecretAccessKey:  secretAccessKey,
		BucketPublicURLs: bucketPublicURLs,
	}

	return NewR2Client(cfg)
}
