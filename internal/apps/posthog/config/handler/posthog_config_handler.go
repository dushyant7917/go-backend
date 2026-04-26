package handler

import (
	"net/http"
	"strconv"

	"go-backend/internal/apps/posthog/config/models"
	"go-backend/internal/apps/posthog/config/service"
	commonResponse "go-backend/internal/common/response"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// PostHogConfigHandler handles HTTP requests for PostHog config operations
type PostHogConfigHandler struct {
	service service.PostHogConfigService
}

// NewPostHogConfigHandler creates a new PostHogConfigHandler
func NewPostHogConfigHandler(svc service.PostHogConfigService) *PostHogConfigHandler {
	return &PostHogConfigHandler{service: svc}
}

// CreatePostHogConfig handles POST /posthog-configs
func (h *PostHogConfigHandler) CreatePostHogConfig(c *gin.Context) {
	var req models.CreatePostHogConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	response, err := h.service.CreatePostHogConfig(req)
	if err != nil {
		if err.Error() == "app_name and environment combination already exists" {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		commonResponse.Error(c, http.StatusInternalServerError, err, err.Error())
		return
	}

	c.JSON(http.StatusCreated, response)
}

// GetPostHogConfigByID handles GET /posthog-configs/:id
func (h *PostHogConfigHandler) GetPostHogConfigByID(c *gin.Context) {
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid UUID"})
		return
	}

	response, err := h.service.GetPostHogConfigByID(id)
	if err != nil {
		if err.Error() == "posthog config not found" {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		commonResponse.Error(c, http.StatusInternalServerError, err, err.Error())
		return
	}

	c.JSON(http.StatusOK, response)
}

// GetPostHogConfigByAppNameAndEnv handles GET /posthog-configs/by-app?app_name=myapp&environment=test
func (h *PostHogConfigHandler) GetPostHogConfigByAppNameAndEnv(c *gin.Context) {
	appName := c.Query("app_name")
	if appName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "app_name is required"})
		return
	}

	environment := c.Query("environment")
	if environment == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "environment is required"})
		return
	}

	response, err := h.service.GetPostHogConfigByAppNameAndEnv(appName, environment)
	if err != nil {
		if err.Error() == "posthog config not found" {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		commonResponse.Error(c, http.StatusInternalServerError, err, err.Error())
		return
	}

	c.JSON(http.StatusOK, response)
}

// GetAllPostHogConfigs handles GET /posthog-configs
func (h *PostHogConfigHandler) GetAllPostHogConfigs(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))
	activeOnly := c.DefaultQuery("active_only", "false") == "true"

	response, err := h.service.GetAllPostHogConfigs(page, pageSize, activeOnly)
	if err != nil {
		commonResponse.Error(c, http.StatusInternalServerError, err, err.Error())
		return
	}

	c.JSON(http.StatusOK, response)
}

// UpdatePostHogConfig handles PUT /posthog-configs/:id
func (h *PostHogConfigHandler) UpdatePostHogConfig(c *gin.Context) {
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid UUID"})
		return
	}

	var req models.UpdatePostHogConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	response, err := h.service.UpdatePostHogConfig(id, req)
	if err != nil {
		if err.Error() == "posthog config not found" {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		commonResponse.Error(c, http.StatusInternalServerError, err, err.Error())
		return
	}

	c.JSON(http.StatusOK, response)
}

// DeletePostHogConfig handles DELETE /posthog-configs/:id
func (h *PostHogConfigHandler) DeletePostHogConfig(c *gin.Context) {
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid UUID"})
		return
	}

	if err := h.service.DeletePostHogConfig(id); err != nil {
		if err.Error() == "posthog config not found" {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		commonResponse.Error(c, http.StatusInternalServerError, err, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "posthog config deleted successfully"})
}
