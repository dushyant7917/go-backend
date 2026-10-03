package handler

import (
	"net/http"

	r2ConfigService "go-backend/internal/apps/r2/config/service"
	"go-backend/internal/apps/sanskaar/models"
	"go-backend/internal/apps/sanskaar/service"
	commonResponse "go-backend/internal/common/response"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// DevotionalMusicHandler handles HTTP requests for devotional music operations
type DevotionalMusicHandler struct {
	service         service.DevotionalMusicService
	r2ClientFactory *r2ConfigService.R2ClientFactory
}

// NewDevotionalMusicHandler creates a new instance of DevotionalMusicHandler
func NewDevotionalMusicHandler(service service.DevotionalMusicService, r2ClientFactory *r2ConfigService.R2ClientFactory) *DevotionalMusicHandler {
	return &DevotionalMusicHandler{
		service:         service,
		r2ClientFactory: r2ClientFactory,
	}
}

// CreateDevotionalMusic handles POST /api/v1/sanskaar/devotional-music
func (h *DevotionalMusicHandler) CreateDevotionalMusic(c *gin.Context) {
	var req models.CreateDevotionalMusicRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := h.service.Create(req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"data": resp})
}

// GetDevotionalMusic handles GET /api/v1/sanskaar/devotional-music/:id
func (h *DevotionalMusicHandler) GetDevotionalMusic(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid devotional music id"})
		return
	}

	resp, err := h.service.GetByID(id)
	if err != nil {
		status := http.StatusInternalServerError
		if err.Error() == "devotional music not found" {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": resp})
}

// UpdateDevotionalMusic handles PUT /api/v1/sanskaar/devotional-music/:id
func (h *DevotionalMusicHandler) UpdateDevotionalMusic(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid devotional music id"})
		return
	}

	var req models.UpdateDevotionalMusicRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := h.service.Update(id, req)
	if err != nil {
		status := http.StatusBadRequest
		if err.Error() == "devotional music not found" {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": resp})
}

// GetDevotionalMusicList handles GET /api/v1/sanskaar/devotional-music
func (h *DevotionalMusicHandler) GetDevotionalMusicList(c *gin.Context) {
	page, pageSize := parsePageParams(c)
	category := c.Query("category")
	deity := c.Query("deity")

	userCreatedAt, firstDayUnlockCount, dailyUnlockBatchSize, ok := parseEligibilityParams(c)
	if !ok {
		return
	}

	resp, err := h.service.List(category, deity, userCreatedAt, firstDayUnlockCount, dailyUnlockBatchSize, page, pageSize)
	if err != nil {
		commonResponse.Error(c, http.StatusInternalServerError, err, err.Error())
		return
	}

	c.JSON(http.StatusOK, resp)
}

// GetUploadURL handles POST /api/v1/sanskaar/devotional-music/upload-url
func (h *DevotionalMusicHandler) GetUploadURL(c *gin.Context) {
	devotionalMusicUploadURL(c, h.r2ClientFactory)
}
