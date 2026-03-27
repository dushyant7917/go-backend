package handler

import (
	"net/http"

	"go-backend/internal/apps/dailystory/models"
	"go-backend/internal/apps/dailystory/service"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// NewsPosterHandler handles HTTP requests for news poster operations
type NewsPosterHandler struct {
	service service.NewsPosterService
}

// NewNewsPosterHandler creates a new instance of NewsPosterHandler
func NewNewsPosterHandler(service service.NewsPosterService) *NewsPosterHandler {
	return &NewsPosterHandler{service: service}
}

// CreateNewsPoster handles POST /api/v1/dailystory/news-posters
func (h *NewsPosterHandler) CreateNewsPoster(c *gin.Context) {
	var req models.CreateNewsPosterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate UUIDs
	if req.NewsID == uuid.Nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "news_id is required"})
		return
	}
	if req.UserID == uuid.Nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_id is required"})
		return
	}

	// Validate required string fields
	if req.UserName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_name is required"})
		return
	}
	if req.UserStateID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_state_id is required"})
		return
	}
	if req.LanguageCode == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "language_code is required"})
		return
	}

	newsPoster, err := h.service.CreateNewsPoster(&req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"data": newsPoster})
}
