package handler

import (
	"net/http"
	"strconv"

	"go-backend/internal/apps/wingwoman/service"
	commonResponse "go-backend/internal/common/response"

	"github.com/gin-gonic/gin"
)

// HelperHandler handles HTTP requests for helper operations
type HelperHandler struct {
	service service.HelperService
}

// NewHelperHandler creates a new instance of HelperHandler
func NewHelperHandler(service service.HelperService) *HelperHandler {
	return &HelperHandler{service: service}
}

// ListHelpers handles GET /api/v1/wingwoman/helpers
func (h *HelperHandler) ListHelpers(c *gin.Context) {
	// Default pagination values
	page := 1
	pageSize := 10

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

	resp, err := h.service.ListHelpersPaginated(page, pageSize)
	if err != nil {
		commonResponse.Error(c, http.StatusInternalServerError, err, err.Error())
		return
	}

	c.JSON(http.StatusOK, resp)
}
