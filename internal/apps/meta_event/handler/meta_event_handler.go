package handler

import (
	"net/http"

	"go-backend/internal/apps/meta_event/models"
	"go-backend/internal/apps/meta_event/service"
	commonResponse "go-backend/internal/common/response"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// MetaEventHandler handles HTTP requests for meta event operations
type MetaEventHandler struct {
	service service.MetaEventService
}

// NewMetaEventHandler creates a new instance of MetaEventHandler
func NewMetaEventHandler(service service.MetaEventService) *MetaEventHandler {
	return &MetaEventHandler{service: service}
}

// UpdateMetaEvent handles PUT /api/v1/meta-events/:id
func (h *MetaEventHandler) UpdateMetaEvent(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid meta event id"})
		return
	}

	var req models.UpdateMetaEventRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	event, err := h.service.UpdateMetaEvent(id, req)
	if err != nil {
		if err.Error() == "meta event not found" {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		commonResponse.Error(c, http.StatusInternalServerError, err, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": event.ToResponse()})
}
