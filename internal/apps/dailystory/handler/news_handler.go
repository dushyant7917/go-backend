package handler

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"go-backend/internal/apps/dailystory/models"
	"go-backend/internal/apps/dailystory/service"
	commonResponse "go-backend/internal/common/response"

	"github.com/gin-gonic/gin"
)

// NewsHandler handles HTTP requests for news operations
type NewsHandler struct {
	service service.NewsService
}

// NewNewsHandler creates a new instance of NewsHandler
func NewNewsHandler(service service.NewsService) *NewsHandler {
	return &NewsHandler{service: service}
}

// ListNews handles GET /api/v1/dailystory/news
func (h *NewsHandler) ListNews(c *gin.Context) {
	// Default pagination values
	page := 1
	pageSize := 20

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

	// Get optional filter parameters
	category := c.Query("category")
	subCategory := c.Query("sub_category")
	languageCode := c.Query("language_code")
	status := c.Query("status")

	// Parse created_at_from parameter (format: 2006-01-02)
	var createdAtFrom *time.Time
	if createdAtFromStr := c.Query("created_at_from"); createdAtFromStr != "" {
		parsedTime, err := time.Parse("2006-01-02", createdAtFromStr)
		if err == nil {
			createdAtFrom = &parsedTime
		}
	}

	resp, err := h.service.ListNewsPaginated(category, subCategory, languageCode, status, createdAtFrom, page, pageSize)
	if err != nil {
		commonResponse.Error(c, http.StatusInternalServerError, err, err.Error())
		return
	}

	c.JSON(http.StatusOK, resp)
}

// BulkUpdateNewsMediaFileKey handles PATCH /api/v1/dailystory/news/media-file-key
func (h *NewsHandler) BulkUpdateNewsMediaFileKey(c *gin.Context) {
	var req models.BulkUpdateNewsMediaFileKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := h.service.BulkUpdateNewsMediaFileKey(&req); err != nil {
		commonResponse.Error(c, http.StatusInternalServerError, err, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"updated": len(req.Items)})
}

// UpdateNews handles PUT /api/v1/dailystory/news/:id
func (h *NewsHandler) UpdateNews(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id is required"})
		return
	}

	var req models.UpdateNewsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	news, err := h.service.UpdateNews(id, &req)
	if err != nil {
		if errors.Is(err, service.ErrNewsNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		commonResponse.Error(c, http.StatusInternalServerError, err, err.Error())
		return
	}

	c.JSON(http.StatusOK, news)
}
