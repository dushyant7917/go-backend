package handler

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go-backend/internal/apps/dailystory/models"
	"go-backend/internal/apps/dailystory/service"
	"go-backend/pkg/storage"
	"go-backend/pkg/utils"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ImageTemplateHandler handles HTTP requests for image template operations
type ImageTemplateHandler struct {
	service  service.ImageTemplateService
	r2Client *storage.R2Client
}

// NewImageTemplateHandler creates a new instance of ImageTemplateHandler
func NewImageTemplateHandler(service service.ImageTemplateService, r2Client *storage.R2Client) *ImageTemplateHandler {
	return &ImageTemplateHandler{
		service:  service,
		r2Client: r2Client,
	}
}

// CreateImageTemplate handles POST /api/v1/dailystory/image-templates
func (h *ImageTemplateHandler) CreateImageTemplate(c *gin.Context) {
	var req models.CreateImageTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := h.service.CreateImageTemplate(req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"data": resp})
}

// GetImageTemplate handles GET /api/v1/dailystory/image-templates/:id
func (h *ImageTemplateHandler) GetImageTemplate(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid image template id"})
		return
	}

	resp, err := h.service.GetImageTemplateByID(id)
	if err != nil {
		status := http.StatusInternalServerError
		if err.Error() == "image template not found" {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": resp})
}

// UpdateImageTemplate handles PUT /api/v1/dailystory/image-templates/:id
func (h *ImageTemplateHandler) UpdateImageTemplate(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid image template id"})
		return
	}

	var req models.UpdateImageTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := h.service.UpdateImageTemplate(id, req)
	if err != nil {
		status := http.StatusBadRequest
		if err.Error() == "image template not found" {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": resp})
}

// GetImageTemplates handles GET /api/v1/dailystory/image-templates
func (h *ImageTemplateHandler) GetImageTemplates(c *gin.Context) {
	// Default pagination values
	page := 1
	pageSize := 10

	// Parse query parameters for filters
	category := c.Query("category")
	subCategory := c.Query("sub_category")
	authorIDStr := c.Query("author_id")
	statusStr := c.Query("status")

	var authorID *uuid.UUID
	if authorIDStr != "" {
		parsed, err := uuid.Parse(authorIDStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid author_id"})
			return
		}
		authorID = &parsed
	}

	var status *string
	if statusStr != "" {
		// Validate status values
		if statusStr != "published" && statusStr != "approved" && statusStr != "rejected" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "status must be 'published', 'approved', or 'rejected'"})
			return
		}
		status = &statusStr
	}

	// Parse page parameter
	if pageStr := c.Query("page"); pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}

	// Parse page_size parameter
	if pageSizeStr := c.Query("page_size"); pageSizeStr != "" {
		if ps, err := strconv.Atoi(pageSizeStr); err == nil && ps > 0 {
			pageSize = ps
		}
	}

	resp, err := h.service.GetImageTemplatesWithFilters(category, subCategory, authorID, status, page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// GetUploadURL handles POST /api/v1/dailystory/image-templates/upload-url
func (h *ImageTemplateHandler) GetUploadURL(c *gin.Context) {
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
	bucketName := os.Getenv("R2_DS_TEMPLATES_BUCKET_NAME")
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

// GetImageTemplateViewURL handles GET /api/v1/dailystory/image-templates/:id/view-url
func (h *ImageTemplateHandler) GetImageTemplateViewURL(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid image template id"})
		return
	}

	// Get the image template to retrieve the file_key
	resp, err := h.service.GetImageTemplateByID(id)
	if err != nil {
		status := http.StatusInternalServerError
		if err.Error() == "image template not found" {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}

	// Get bucket name from environment
	bucketName := os.Getenv("R2_DS_TEMPLATES_BUCKET_NAME")
	if bucketName == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "R2 bucket configuration missing"})
		return
	}

	// Get public file URL (permanent URL for public bucket)
	publicURL, err := h.r2Client.GetPublicFileURL(bucketName, resp.FileKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate view URL"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"view_url": publicURL,
		"file_key": resp.FileKey,
	})
}

// GetDesignerStats handles GET /api/v1/dailystory/image-templates/designer-stats
func (h *ImageTemplateHandler) GetDesignerStats(c *gin.Context) {
	resp, err := h.service.GetDesignerStats()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": resp})
}

// GetPosterCountByCount handles GET /api/v1/dailystory/image-templates/poster-count-by-count
func (h *ImageTemplateHandler) GetPosterCountByCount(c *gin.Context) {
	// Default pagination values
	page := 1
	pageSize := 10

	// Parse page parameter
	if pageStr := c.Query("page"); pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}

	// Parse page_size parameter
	if pageSizeStr := c.Query("page_size"); pageSizeStr != "" {
		if ps, err := strconv.Atoi(pageSizeStr); err == nil && ps > 0 {
			pageSize = ps
		}
	}

	resp, err := h.service.GetPosterCountByTemplate(true, page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// GetPosterCountByDate handles GET /api/v1/dailystory/image-templates/poster-count-by-date
func (h *ImageTemplateHandler) GetPosterCountByDate(c *gin.Context) {
	// Default pagination values
	page := 1
	pageSize := 10

	// Parse page parameter
	if pageStr := c.Query("page"); pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}

	// Parse page_size parameter
	if pageSizeStr := c.Query("page_size"); pageSizeStr != "" {
		if ps, err := strconv.Atoi(pageSizeStr); err == nil && ps > 0 {
			pageSize = ps
		}
	}

	resp, err := h.service.GetPosterCountByTemplate(false, page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, resp)
}
