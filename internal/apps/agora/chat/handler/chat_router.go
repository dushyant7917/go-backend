package handler

import "github.com/gin-gonic/gin"

// RegisterChatRoutes registers all Agora chat-related routes
func RegisterChatRoutes(router *gin.RouterGroup, handler *ChatHandler) {
	chat := router.Group("/agora/chat")
	{
		chat.POST("/token", handler.GenerateChatToken)
	}
}
