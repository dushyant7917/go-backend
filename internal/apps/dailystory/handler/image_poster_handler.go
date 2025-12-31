package handler

import (
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
