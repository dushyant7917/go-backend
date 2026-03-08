package handler

import (
	"net/http"
	"strconv"

	"go-backend/internal/apps/r2/config/models"
	"go-backend/internal/apps/r2/config/service"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// R2ConfigHandler handles HTTP requests for R2 config operations
type R2ConfigHandler struct {
	service service.R2ConfigService
}

// NewR2ConfigHandler creates a new instance of R2ConfigHandler
func NewR2ConfigHandler(service service.R2ConfigService) *R2ConfigHandler {
	return &R2ConfigHandler{
		service: service,
	}
}

// CreateR2Config handles POST /api/v1/r2-configs
func (h *R2ConfigHandler) CreateR2Config(c *gin.Context) {
	var req models.CreateR2ConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	response, err := h.service.CreateConfig(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"data": response})
}

// GetR2Config handles GET /api/v1/r2-configs/:id
func (h *R2ConfigHandler) GetR2Config(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid r2 config id"})
		return
	}

	response, err := h.service.GetConfigByID(id)
	if err != nil {
		status := http.StatusInternalServerError
		if err.Error() == "r2 config not found" {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": response})
}

// GetR2Configs handles GET /api/v1/r2-configs
func (h *R2ConfigHandler) GetR2Configs(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))

	response, err := h.service.GetAllConfigs(page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, response)
}

// UpdateR2Config handles PUT /api/v1/r2-configs/:id
func (h *R2ConfigHandler) UpdateR2Config(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid r2 config id"})
		return
	}

	var req models.UpdateR2ConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	response, err := h.service.UpdateConfig(id, req)
	if err != nil {
		status := http.StatusInternalServerError
		if err.Error() == "r2 config not found" {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": response})
}

// DeleteR2Config handles DELETE /api/v1/r2-configs/:id
func (h *R2ConfigHandler) DeleteR2Config(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid r2 config id"})
		return
	}

	if err := h.service.DeleteConfig(id); err != nil {
		status := http.StatusInternalServerError
		if err.Error() == "r2 config not found" {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "r2 config deleted successfully"})
}
