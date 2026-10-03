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

// ToneHandler handles HTTP requests for tone operations
type ToneHandler struct {
	service         service.ToneService
	r2ClientFactory *r2ConfigService.R2ClientFactory
}

// NewToneHandler creates a new instance of ToneHandler
func NewToneHandler(service service.ToneService, r2ClientFactory *r2ConfigService.R2ClientFactory) *ToneHandler {
	return &ToneHandler{
		service:         service,
		r2ClientFactory: r2ClientFactory,
	}
}

// CreateTone handles POST /api/v1/sanskaar/tones
func (h *ToneHandler) CreateTone(c *gin.Context) {
	var req models.CreateToneRequest
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

// GetTone handles GET /api/v1/sanskaar/tones/:id
func (h *ToneHandler) GetTone(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid tone id"})
		return
	}

	resp, err := h.service.GetByID(id)
	if err != nil {
		status := http.StatusInternalServerError
		if err.Error() == "tone not found" {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": resp})
}

// UpdateTone handles PUT /api/v1/sanskaar/tones/:id
func (h *ToneHandler) UpdateTone(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid tone id"})
		return
	}

	var req models.UpdateToneRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := h.service.Update(id, req)
	if err != nil {
		status := http.StatusBadRequest
		if err.Error() == "tone not found" {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": resp})
}

// GetToneList handles GET /api/v1/sanskaar/tones
func (h *ToneHandler) GetToneList(c *gin.Context) {
	page, pageSize := parsePageParams(c)
	deity := c.Query("deity")

	userCreatedAt, firstDayUnlockCount, dailyUnlockBatchSize, ok := parseEligibilityParams(c)
	if !ok {
		return
	}

	resp, err := h.service.List(deity, userCreatedAt, firstDayUnlockCount, dailyUnlockBatchSize, page, pageSize)
	if err != nil {
		commonResponse.Error(c, http.StatusInternalServerError, err, err.Error())
		return
	}

	c.JSON(http.StatusOK, resp)
}

// GetUploadURL handles POST /api/v1/sanskaar/tones/upload-url
func (h *ToneHandler) GetUploadURL(c *gin.Context) {
	audioThumbnailUploadURL(c, h.r2ClientFactory, "tones")
}
