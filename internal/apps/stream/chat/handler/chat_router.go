package handler

import "github.com/gin-gonic/gin"

// RegisterChatRoutes registers all Stream chat-related routes
func RegisterChatRoutes(router *gin.RouterGroup, handler *ChatHandler) {
	chat := router.Group("/stream/chat")
	{
		chat.POST("/token", handler.GenerateChatToken)
	}
}
