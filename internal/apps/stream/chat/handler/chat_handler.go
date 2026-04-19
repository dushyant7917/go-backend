package handler

import (
	"net/http"

	"go-backend/internal/apps/stream/chat/models"
	"go-backend/internal/apps/stream/chat/service"
	commonResponse "go-backend/internal/common/response"

	"github.com/gin-gonic/gin"
)

// ChatHandler handles HTTP requests for Stream chat operations
type ChatHandler struct {
	service service.ChatService
}

// NewChatHandler creates a new instance of ChatHandler
func NewChatHandler(service service.ChatService) *ChatHandler {
	return &ChatHandler{service: service}
}

// GenerateChatToken handles POST /api/v1/stream/chat/token
func (h *ChatHandler) GenerateChatToken(c *gin.Context) {
	var req models.GenerateChatTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := h.service.GenerateChatToken(req)
	if err != nil {
		commonResponse.Error(c, http.StatusInternalServerError, err, err.Error())
		return
	}

	c.JSON(http.StatusCreated, gin.H{"data": resp})
}
