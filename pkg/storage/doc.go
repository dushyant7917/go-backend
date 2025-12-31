/*
Package storage provides utilities for interacting with Cloudflare R2 storage.

Example Usage:

	import "go-backend/pkg/storage"

	// Initialize R2 client from environment variables
	r2Client, err := storage.NewR2ClientFromEnv()
	if err != nil {
		log.Fatal(err)
	}

	// OR initialize with explicit configuration
	r2Client, err := storage.NewR2Client(storage.R2Config{
		AccountID:       "your-account-id",
		AccessKeyID:     "your-access-key-id",
		SecretAccessKey: "your-secret-access-key",
		BucketPublicURLs: map[string]string{
			"daily-story-templates": "https://pub-xxxxx.r2.dev",
			"daily-story-posters":   "https://pub-yyyyy.r2.dev",
		},
	})

	// 1. Get pre-signed URL for uploading a file
	uploadURL, err := r2Client.GetPresignedUploadURL(
		"my-bucket",           // bucket name
		"uploads/image.png",   // file key
		"image/png",           // content type
		60,                    // expiration in minutes
	)
	if err != nil {
		log.Fatal(err)
	}
	// Use uploadURL with HTTP PUT request to upload the file

	// 2. Get public URL for viewing a file (requires public bucket)
	publicURL, err := r2Client.GetPublicFileURL(
		"my-public-bucket",    // bucket name
		"images/photo.jpg",    // file key
	)
	if err != nil {
		log.Fatal(err)
	}
	// Use publicURL directly in <img> tags or share with users

	// 3. Get pre-signed URL for viewing a private file
	viewURL, err := r2Client.GetPresignedViewURL(
		"my-private-bucket",   // bucket name
		"documents/report.pdf", // file key
		30,                    // expiration in minutes
	)
	if err != nil {
		log.Fatal(err)
	}
	// Use viewURL to allow temporary access to private files

	// 4. Delete a file from a bucket
	err = r2Client.DeleteFile(
		"my-bucket",           // bucket name
		"old-files/temp.txt",  // file key
	)
	if err != nil {
		log.Fatal(err)
	}

Environment Variables (when using NewR2ClientFromEnv):

	R2_ACCOUNT_ID          - Required: Your Cloudflare account ID
	R2_ACCESS_KEY_ID       - Required: R2 access key ID
	R2_SECRET_ACCESS_KEY   - Required: R2 secret access key
	R2_PUBLIC_BASE_URL     - Optional: Base URL for public bucket (e.g., https://pub-xxxxx.r2.dev)
*/
package storage
