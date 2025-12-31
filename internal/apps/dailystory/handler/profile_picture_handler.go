package handler

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go-backend/pkg/storage"
	"go-backend/pkg/utils"

	"github.com/gin-gonic/gin"
)

// ProfilePictureHandler handles HTTP requests for profile picture operations
type ProfilePictureHandler struct {
	r2Client *storage.R2Client
}

// NewProfilePictureHandler creates a new instance of ProfilePictureHandler
func NewProfilePictureHandler(r2Client *storage.R2Client) *ProfilePictureHandler {
	return &ProfilePictureHandler{
		r2Client: r2Client,
	}
}

// GetUploadURL handles POST /api/v1/dailystory/profile-picture/upload-url
func (h *ProfilePictureHandler) GetUploadURL(c *gin.Context) {
	var req struct {
		Filename string `json:"filename" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate filename
	if strings.TrimSpace(req.Filename) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "filename cannot be empty"})
		return
	}

	// Extract file extension and name without extension
	ext := filepath.Ext(req.Filename)
	if ext == "" {
		ext = ".png" // Default to PNG
	}
	filenameWithoutExt := strings.TrimSuffix(req.Filename, ext)

	// Get content type dynamically based on extension
	contentType := utils.GetContentTypeFromExtension(ext)

	// Validate extension is an image type
	if !strings.HasPrefix(contentType, "image/") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "only image files are supported"})
		return
	}

	// Generate file key in format: profile-pictures/<filename_without_extension>_<timestamp>.<extension>
	timestamp := time.Now().UTC().Unix()
	fileKey := fmt.Sprintf("profile-pictures/%s_%d%s", filenameWithoutExt, timestamp, ext)

	// Get bucket name from environment (using the users bucket)
	bucketName := os.Getenv("R2_DS_USERS_BUCKET_NAME")
	if bucketName == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "R2 bucket configuration missing"})
		return
	}

	// Use the reusable R2 client from handler (initialized at startup)
	// Generate presigned upload URL (valid for 5 minutes)
	presignedURL, err := h.r2Client.GetPresignedUploadURL(bucketName, fileKey, contentType, 5)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate upload URL"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"presigned_url": presignedURL,
		"file_key":      fileKey,
		"upload_headers": gin.H{
			"Content-Type": contentType,
		},
		"instructions": fmt.Sprintf("MUST send Content-Type: %s header when uploading. The presigned URL signature requires this exact header.", contentType),
	})
}

// GetViewURL handles GET /api/v1/dailystory/profile-picture/view-url
func (h *ProfilePictureHandler) GetViewURL(c *gin.Context) {
	fileKey := c.Query("file_key")
	if fileKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file_key query parameter is required"})
		return
	}

	// Validate file_key starts with profile-pictures/ prefix
	if !strings.HasPrefix(fileKey, "profile-pictures/") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid file_key: must be a profile picture"})
		return
	}

	// Get bucket name from environment (using the users bucket)
	bucketName := os.Getenv("R2_DS_USERS_BUCKET_NAME")
	if bucketName == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "R2 bucket configuration missing"})
		return
	}

	// Generate presigned view URL (valid for 60 minutes by default)
	expirationMinutes := 60
	presignedURL, err := h.r2Client.GetPresignedViewURL(bucketName, fileKey, expirationMinutes)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate view URL"})
		return
	}

	// Calculate expiry time as Unix timestamp in milliseconds
	expiryTime := time.Now().Add(time.Duration(expirationMinutes) * time.Minute).UnixMilli()

	c.JSON(http.StatusOK, gin.H{
		"view_url":    presignedURL,
		"file_key":    fileKey,
		"expiry_time": expiryTime,
	})
}
