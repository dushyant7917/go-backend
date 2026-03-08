package handler

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	r2ConfigService "go-backend/internal/apps/r2/config/service"
	"go-backend/internal/common/constants"
	"go-backend/pkg/utils"

	"github.com/gin-gonic/gin"
)

// ProfilePictureHandler handles HTTP requests for Chemistry profile picture operations
type ProfilePictureHandler struct {
	r2ClientFactory *r2ConfigService.R2ClientFactory
}

// NewProfilePictureHandler creates a new instance of ProfilePictureHandler
func NewProfilePictureHandler(r2ClientFactory *r2ConfigService.R2ClientFactory) *ProfilePictureHandler {
	return &ProfilePictureHandler{
		r2ClientFactory: r2ClientFactory,
	}
}

// GetUploadURL handles POST /api/v1/chemistry/profile-picture/upload-url
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

	// Get bucket name from environment for Chemistry app
	bucketName := os.Getenv("R2_CHEMISTRY_BUCKET_NAME")
	if bucketName == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "R2 bucket configuration missing"})
		return
	}

	// Get R2 client dynamically from database config
	r2Client, err := h.r2ClientFactory.GetClient(constants.AppNameChemistry)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "R2 configuration not found for Chemistry app"})
		return
	}

	// Generate presigned upload URL (valid for 5 minutes)
	presignedURL, err := r2Client.GetPresignedUploadURL(bucketName, fileKey, contentType, 5)
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

// GetViewURL handles GET /api/v1/chemistry/profile-picture/view-url
// Returns a public URL since Chemistry profile pictures are publicly accessible
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

	// Get public URL base from environment for Chemistry app
	publicURLBase := os.Getenv("R2_CHEMISTRY_PUBLIC_URL")
	if publicURLBase == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "R2 bucket configuration missing"})
		return
	}

	// Get R2 client dynamically from database config
	r2Client, err := h.r2ClientFactory.GetClient(constants.AppNameChemistry)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "R2 configuration not found for Chemistry app"})
		return
	}

	// Construct public URL
	publicURL, err := r2Client.GetPublicFileURL(publicURLBase, fileKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate view URL"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"view_url": publicURL,
		"file_key": fileKey,
	})
}
