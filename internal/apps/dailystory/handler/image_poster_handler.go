package handler

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go-backend/internal/apps/dailystory/models"
	"go-backend/internal/apps/dailystory/service"
	r2ConfigService "go-backend/internal/apps/r2/config/service"
	"go-backend/internal/common/constants"
	"go-backend/pkg/utils"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ImagePosterHandler handles HTTP requests for image poster operations
type ImagePosterHandler struct {
	service         service.ImagePosterService
	r2ClientFactory *r2ConfigService.R2ClientFactory
}

// NewImagePosterHandler creates a new instance of ImagePosterHandler
func NewImagePosterHandler(service service.ImagePosterService, r2ClientFactory *r2ConfigService.R2ClientFactory) *ImagePosterHandler {
	return &ImagePosterHandler{
		service:         service,
		r2ClientFactory: r2ClientFactory,
	}
}

// CreatePoster handles POST /api/v1/dailystory/posters
func (h *ImagePosterHandler) CreatePoster(c *gin.Context) {
	var req models.CreatePosterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate UUIDs
	if req.TemplateID == uuid.Nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "template_id is required"})
		return
	}
	if req.UserID == uuid.Nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_id is required"})
		return
	}

	poster, err := h.service.CreatePoster(&req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"data": poster})
}

// GetPosterUploadURL handles POST /api/v1/dailystory/posters/upload-url
func (h *ImagePosterHandler) GetPosterUploadURL(c *gin.Context) {
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

	// Generate file key in format: images/<filename_without_extension>_<timestamp>.<extension>
	timestamp := time.Now().UTC().Unix()
	fileKey := fmt.Sprintf("images/%s_%d%s", filenameWithoutExt, timestamp, ext)

	// Get bucket name from environment
	bucketName := os.Getenv("R2_DS_POSTERS_BUCKET_NAME")
	if bucketName == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "R2 bucket configuration missing"})
		return
	}

	// Get R2 client dynamically from database config
	r2Client, err := h.r2ClientFactory.GetClient(constants.AppNameDailyStory)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "R2 configuration not found for dailystory app"})
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

// GetUserPosterStats handles GET /api/v1/dailystory/posters/user-stats
// Returns paginated user poster statistics with flexible sorting options
func (h *ImagePosterHandler) GetUserPosterStats(c *gin.Context) {
	// Get required query parameters
	appName := c.Query("app_name")
	if appName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "app_name query parameter is required"})
		return
	}

	sortBy := c.Query("sort_by")
	if sortBy == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "sort_by query parameter is required. Valid options: most_active, least_active, power_users, new_engaged",
		})
		return
	}

	// Get optional pagination parameters
	page := 1
	if pageStr := c.Query("page"); pageStr != "" {
		if parsedPage, err := parseInt(pageStr); err == nil && parsedPage > 0 {
			page = parsedPage
		}
	}

	pageSize := 10
	if pageSizeStr := c.Query("page_size"); pageSizeStr != "" {
		if parsedSize, err := parseInt(pageSizeStr); err == nil && parsedSize > 0 {
			pageSize = parsedSize
		}
	}

	// Get stats from service
	resp, err := h.service.GetUserPosterStatsByAppName(appName, sortBy, page, pageSize)
	if err != nil {
		// Check if it's a validation error
		if errMsg := err.Error(); len(errMsg) > 0 && errMsg[:7] == "invalid" {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// parseInt is a helper function to parse string to int
func parseInt(s string) (int, error) {
	var result int
	_, err := fmt.Sscanf(s, "%d", &result)
	return result, err
}
