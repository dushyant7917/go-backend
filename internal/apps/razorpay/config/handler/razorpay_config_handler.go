package handler

import (
	"net/http"
	"strconv"

	"go-backend/internal/apps/razorpay/config/models"
	"go-backend/internal/apps/razorpay/config/service"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RazorpayConfigHandler handles HTTP requests for razorpay config operations
type RazorpayConfigHandler struct {
	service service.RazorpayConfigService
}

// NewRazorpayConfigHandler creates a new RazorpayConfigHandler
func NewRazorpayConfigHandler(svc service.RazorpayConfigService) *RazorpayConfigHandler {
	return &RazorpayConfigHandler{service: svc}
}

// CreateRazorpayConfig handles POST /razorpay-configs
func (h *RazorpayConfigHandler) CreateRazorpayConfig(c *gin.Context) {
	var req models.CreateRazorpayConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	response, err := h.service.CreateRazorpayConfig(req)
	if err != nil {
		if err.Error() == "app_name and environment combination already exists" {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, response)
}

// GetRazorpayConfigByID handles GET /razorpay-configs/:id
func (h *RazorpayConfigHandler) GetRazorpayConfigByID(c *gin.Context) {
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid UUID"})
		return
	}

	response, err := h.service.GetRazorpayConfigByID(id)
	if err != nil {
		if err.Error() == "razorpay config not found" {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, response)
}

// GetRazorpayConfigByAppNameAndEnv handles GET /razorpay-configs/by-app?app_name=myapp&environment=test
func (h *RazorpayConfigHandler) GetRazorpayConfigByAppNameAndEnv(c *gin.Context) {
	appName := c.Query("app_name")
	if appName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "app_name is required"})
		return
	}

	environment := c.DefaultQuery("environment", "test")
	if environment != "test" && environment != "live" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "environment must be 'test' or 'live'"})
		return
	}

	response, err := h.service.GetRazorpayConfigByAppNameAndEnv(appName, environment)
	if err != nil {
		if err.Error() == "razorpay config not found" {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, response)
}

// GetAllRazorpayConfigs handles GET /razorpay-configs
func (h *RazorpayConfigHandler) GetAllRazorpayConfigs(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))
	activeOnly := c.DefaultQuery("active_only", "false") == "true"

	response, err := h.service.GetAllRazorpayConfigs(page, pageSize, activeOnly)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, response)
}

// UpdateRazorpayConfig handles PUT /razorpay-configs/:id
func (h *RazorpayConfigHandler) UpdateRazorpayConfig(c *gin.Context) {
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid UUID"})
		return
	}

	var req models.UpdateRazorpayConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	response, err := h.service.UpdateRazorpayConfig(id, req)
	if err != nil {
		if err.Error() == "razorpay config not found" {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, response)
}

// DeleteRazorpayConfig handles DELETE /razorpay-configs/:id
func (h *RazorpayConfigHandler) DeleteRazorpayConfig(c *gin.Context) {
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid UUID"})
		return
	}

	if err := h.service.DeleteRazorpayConfig(id); err != nil {
		if err.Error() == "razorpay config not found" {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "razorpay config deleted successfully"})
}
