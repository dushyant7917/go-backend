package handler

import (
	"net/http"
	"strconv"

	"go-backend/internal/apps/user/models"
	"go-backend/internal/apps/user/service"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// UserHandler handles HTTP requests for user operations
type UserHandler struct {
	service service.UserService
}

// NewUserHandler creates a new instance of UserHandler
func NewUserHandler(service service.UserService) *UserHandler {
	return &UserHandler{service: service}
}

// CreateUser handles POST /api/v1/users
func (h *UserHandler) CreateUser(c *gin.Context) {
	var req models.CreateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := h.service.CreateUser(req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"data": resp})
}

// GetUserByAppAndPhone handles GET /api/v1/users/by-phone
func (h *UserHandler) GetUserByAppAndPhone(c *gin.Context) {
	appName := c.Query("app_name")
	countryCode := c.Query("country_code")
	phone := c.Query("phone")
	if appName == "" || countryCode == "" || phone == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "app_name, country_code and phone are required"})
		return
	}

	resp, err := h.service.GetUserByAppAndContact(appName, countryCode, phone)
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

// GetUserByAppAndEmail handles GET /api/v1/users/by-email
func (h *UserHandler) GetUserByAppAndEmail(c *gin.Context) {
	appName := c.Query("app_name")
	email := c.Query("email")
	if appName == "" || email == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "app_name and email are required"})
		return
	}

	resp, err := h.service.GetUserByAppAndEmail(appName, email)
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

// GetUser handles GET /api/v1/users/:id
func (h *UserHandler) GetUser(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return
	}

	resp, err := h.service.GetUserByID(id)
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

// UpdateUser handles PUT /api/v1/users/:id
func (h *UserHandler) UpdateUser(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return
	}

	var req models.UpdateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := h.service.UpdateUser(id, req)
	if err != nil {
		status := http.StatusBadRequest
		if err.Error() == "user not found" {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": resp})
}

// ListAllUsers handles GET /api/v1/users/all
func (h *UserHandler) ListAllUsers(c *gin.Context) {
	// Default pagination values
	page := 1
	pageSize := 10

	// Get app_name filter (optional)
	appName := c.Query("app_name")

	// Parse page parameter
	if pageStr := c.Query("page"); pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}

	// Parse page_size parameter
	if pageSizeStr := c.Query("page_size"); pageSizeStr != "" {
		if ps, err := strconv.Atoi(pageSizeStr); err == nil && ps > 0 {
			pageSize = ps
		}
	}

	resp, err := h.service.ListAllUsersPaginated(appName, page, pageSize)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, resp)
}
