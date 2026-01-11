package handler

import (
	"fmt"
	"net/http"

	"go-backend/internal/apps/dailystory/models"
	"go-backend/internal/apps/dailystory/service"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ImagePosterHandler handles HTTP requests for image poster operations
type ImagePosterHandler struct {
	service service.ImagePosterService
}

// NewImagePosterHandler creates a new instance of ImagePosterHandler
func NewImagePosterHandler(service service.ImagePosterService) *ImagePosterHandler {
	return &ImagePosterHandler{
		service: service,
	}
}

// GeneratePoster handles POST /api/v1/dailystory/posters/generate
func (h *ImagePosterHandler) GeneratePoster(c *gin.Context) {
	var req models.GeneratePosterRequest
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

	resp, err := h.service.GeneratePoster(req.TemplateID, req.UserID)
	if err != nil {
		status := http.StatusInternalServerError
		errMsg := err.Error()

		// Handle specific error cases
		if errMsg == "user not found" || errMsg == "template not found" {
			status = http.StatusNotFound
		} else if errMsg == "user does not have a profile picture" ||
			errMsg == "template does not have complete configuration" {
			status = http.StatusBadRequest
		}

		c.JSON(status, gin.H{"error": errMsg})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": resp})
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
