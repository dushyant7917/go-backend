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

// StatusHandler handles HTTP requests for status operations
type StatusHandler struct {
	service         service.StatusService
	r2ClientFactory *r2ConfigService.R2ClientFactory
}

// NewStatusHandler creates a new instance of StatusHandler
func NewStatusHandler(service service.StatusService, r2ClientFactory *r2ConfigService.R2ClientFactory) *StatusHandler {
	return &StatusHandler{
		service:         service,
		r2ClientFactory: r2ClientFactory,
	}
}

// CreateStatus handles POST /api/v1/sanskaar/statuses
func (h *StatusHandler) CreateStatus(c *gin.Context) {
	var req models.CreateStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := validateStatusCategory(req.Category); err != nil {
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

// GetStatus handles GET /api/v1/sanskaar/statuses/:id
func (h *StatusHandler) GetStatus(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid status id"})
		return
	}

	resp, err := h.service.GetByID(id)
	if err != nil {
		status := http.StatusInternalServerError
		if err.Error() == "status not found" {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": resp})
}

// UpdateStatus handles PUT /api/v1/sanskaar/statuses/:id
func (h *StatusHandler) UpdateStatus(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid status id"})
		return
	}

	var req models.UpdateStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Category != nil {
		if err := validateStatusCategory(*req.Category); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	}

	resp, err := h.service.Update(id, req)
	if err != nil {
		status := http.StatusBadRequest
		if err.Error() == "status not found" {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": resp})
}

// GetStatusList handles GET /api/v1/sanskaar/statuses
func (h *StatusHandler) GetStatusList(c *gin.Context) {
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

// GetUploadURL handles POST /api/v1/sanskaar/statuses/upload-url
func (h *StatusHandler) GetUploadURL(c *gin.Context) {
	statusUploadURL(c, h.r2ClientFactory)
}
