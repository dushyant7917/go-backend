package handler

import "github.com/gin-gonic/gin"

// RegisterUserRoutes registers all user-related routes
func RegisterUserRoutes(router *gin.RouterGroup, handler *UserHandler) {
	users := router.Group("/users")
	{
		users.POST("", handler.CreateUser)
		users.GET("/all", handler.ListAllUsers)
		users.GET("/:id", handler.GetUser)
		users.PUT("/:id", handler.UpdateUser)
		users.GET("/by-phone", handler.GetUserByAppAndPhone)
		users.GET("/by-email", handler.GetUserByAppAndEmail)
	}
}
