package handler

import (
	"net/http"

	"go-backend/internal/apps/crush/models"
	"go-backend/internal/apps/crush/service"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// CrushHandler handles HTTP requests for crush operations
type CrushHandler struct {
	service service.CrushService
}

// NewCrushHandler creates a new instance of CrushHandler
func NewCrushHandler(service service.CrushService) *CrushHandler {
	return &CrushHandler{service: service}
}

// CreateCrush handles POST /api/v1/crushes
func (h *CrushHandler) CreateCrush(c *gin.Context) {
	var req models.CreateCrushRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := h.service.CreateCrush(req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"data": resp})
}

// UpdateCrush handles PUT /api/v1/crushes/:id
func (h *CrushHandler) UpdateCrush(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid crush id"})
		return
	}

	var req models.UpdateCrushRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := h.service.UpdateCrush(id, req)
	if err != nil {
		status := http.StatusBadRequest
		if err.Error() == "crush not found" {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": resp})
}

// ListCrushes handles GET /api/v1/crushes?user_id=<uuid>
func (h *CrushHandler) ListCrushes(c *gin.Context) {
	userIDStr := c.Query("user_id")
	if userIDStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_id is required"})
		return
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user_id"})
		return
	}

	resp, err := h.service.ListCrushesByUserID(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": resp})
}

// GetCrush handles GET /api/v1/crushes/:id
func (h *CrushHandler) GetCrush(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid crush id"})
		return
	}

	resp, err := h.service.GetCrushByID(id)
	if err != nil {
		status := http.StatusInternalServerError
		if err.Error() == "crush not found" {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": resp})
}

// ListCrushesOnUser handles GET /api/v1/crushes/on-user?user_id=<uuid>
func (h *CrushHandler) ListCrushesOnUser(c *gin.Context) {
	userIDStr := c.Query("user_id")
	if userIDStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_id is required"})
		return
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user_id"})
		return
	}

	resp, err := h.service.ListCrushesOnUser(userID)
	if err != nil {
		status := http.StatusInternalServerError
		if err.Error() == "user not found" {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": resp})
}
