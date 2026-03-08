package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// R2Client wraps the S3 client for Cloudflare R2 operations
type R2Client struct {
	client    *s3.Client
	accountID string
}

// R2Config holds configuration for Cloudflare R2
type R2Config struct {
	AccountID       string // Cloudflare account ID
	AccessKeyID     string // R2 access key ID
	SecretAccessKey string // R2 secret access key
}

// NewR2Client creates a new R2 client with the provided configuration
func NewR2Client(cfg R2Config) (*R2Client, error) {
	if cfg.AccountID == "" || cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
		return nil, fmt.Errorf("account ID, access key ID, and secret access key are required")
	}

	// Cloudflare R2 endpoint
	endpoint := fmt.Sprintf("https://%s.r2.cloudflarestorage.com", cfg.AccountID)

	// Create AWS config with custom endpoint resolver for R2
	awsCfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			cfg.AccessKeyID,
			cfg.SecretAccessKey,
			"",
		)),
		config.WithRegion("auto"), // R2 uses 'auto' as the region
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	// Create S3 client with custom endpoint
	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = false // R2 uses virtual-hosted-style URLs
	})

	return &R2Client{
		client:    client,
		accountID: cfg.AccountID,
	}, nil
}

// GetPresignedUploadURL generates a pre-signed URL for uploading a file to R2
//
// Parameters:
//   - bucketName: Name of the R2 bucket
//   - fileKey: Object key (path) in the bucket
//   - contentType: MIME type of the file (e.g., "image/png", "application/pdf")
//   - expirationMinutes: URL expiration time in minutes
//
// Returns:
//   - Pre-signed URL string that can be used to upload the file
//   - Error if generation fails
func (r *R2Client) GetPresignedUploadURL(bucketName, fileKey, contentType string, expirationMinutes int) (string, error) {
	if bucketName == "" || fileKey == "" {
		return "", fmt.Errorf("bucket name and file key are required")
	}

	if expirationMinutes <= 0 {
		expirationMinutes = 60 // Default to 60 minutes
	}

	// Default content type if not provided
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	presignClient := s3.NewPresignClient(r.client)

	// Create PutObject request WITH ContentType
	// ContentType will be embedded in the presigned URL as a query parameter
	// This ensures R2 stores the file with the correct content-type
	putObjectInput := &s3.PutObjectInput{
		Bucket:      aws.String(bucketName),
		Key:         aws.String(fileKey),
		ContentType: aws.String(contentType),
	}

	// Generate pre-signed URL
	// The ContentType in PutObjectInput locks the signature to require this exact Content-Type header
	presignedReq, err := presignClient.PresignPutObject(
		context.Background(),
		putObjectInput,
		s3.WithPresignExpires(time.Duration(expirationMinutes)*time.Minute),
	)
	if err != nil {
		return "", fmt.Errorf("failed to generate pre-signed upload URL: %w", err)
	}

	// Return the URL exactly as generated - do NOT modify it
	// The signature is locked to the ContentType specified above
	return presignedReq.URL, nil
}

// GetPublicFileURL constructs a public URL for accessing a file in a public R2 bucket
//
// Parameters:
//   - publicURLBase: The public base URL of the bucket (e.g., "https://pub-xxxxx.r2.dev")
//   - fileKey: Object key (path) in the bucket
//
// Returns:
//   - Public URL string
//
// Note: This requires the bucket to have public access enabled and a custom domain
// or R2.dev subdomain configured.
func (r *R2Client) GetPublicFileURL(publicURLBase, fileKey string) (string, error) {
	if publicURLBase == "" || fileKey == "" {
		return "", fmt.Errorf("public URL base and file key are required")
	}

	// Construct public URL
	// Format: https://pub-xxxxx.r2.dev/file-key or https://custom-domain.com/file-key
	return fmt.Sprintf("%s/%s", publicURLBase, fileKey), nil
}

// GetPresignedViewURL generates a pre-signed URL for viewing/downloading a private file from R2
//
// Parameters:
//   - bucketName: Name of the R2 bucket
//   - fileKey: Object key (path) in the bucket
//   - expirationMinutes: URL expiration time in minutes
//
// Returns:
//   - Pre-signed URL string that can be used to view/download the file
//   - Error if generation fails
func (r *R2Client) GetPresignedViewURL(bucketName, fileKey string, expirationMinutes int) (string, error) {
	if bucketName == "" || fileKey == "" {
		return "", fmt.Errorf("bucket name and file key are required")
	}

	if expirationMinutes <= 0 {
		expirationMinutes = 60 // Default to 60 minutes
	}

	presignClient := s3.NewPresignClient(r.client)

	// Create GetObject request
	getObjectInput := &s3.GetObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(fileKey),
	}

	// Generate pre-signed URL
	presignedReq, err := presignClient.PresignGetObject(
		context.Background(),
		getObjectInput,
		s3.WithPresignExpires(time.Duration(expirationMinutes)*time.Minute),
	)
	if err != nil {
		return "", fmt.Errorf("failed to generate pre-signed view URL: %w", err)
	}

	return presignedReq.URL, nil
}

// DeleteFile deletes a file from an R2 bucket
//
// Parameters:
//   - bucketName: Name of the R2 bucket
//   - fileKey: Object key (path) in the bucket to delete
//
// Returns:
//   - Error if deletion fails, nil on success
func (r *R2Client) DeleteFile(bucketName, fileKey string) error {
	if bucketName == "" || fileKey == "" {
		return fmt.Errorf("bucket name and file key are required")
	}

	// Create DeleteObject request
	deleteObjectInput := &s3.DeleteObjectInput{
		Bucket: aws.String(bucketName),
		Key:    aws.String(fileKey),
	}

	// Delete the object
	_, err := r.client.DeleteObject(context.Background(), deleteObjectInput)
	if err != nil {
		return fmt.Errorf("failed to delete file: %w", err)
	}

	return nil
}

// GetClient returns the underlying S3 client for advanced operations
func (r *R2Client) GetClient() *s3.Client {
	return r.client
}
