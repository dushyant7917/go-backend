package handler

import (
	"github.com/gin-gonic/gin"
)

// RegisterOTPRoutes registers all OTP routes
func RegisterOTPRoutes(router *gin.RouterGroup, phoneOTPHandler *PhoneOTPHandler, emailOTPHandler *EmailOTPHandler) {
	otp := router.Group("/otp")
	{
		// Phone OTP routes
		phone := otp.Group("/phone")
		{
			phone.POST("", phoneOTPHandler.CreateOrUpdateOTP)
			phone.POST("/verify", phoneOTPHandler.VerifyOTP)
		}

		// Email OTP routes
		email := otp.Group("/email")
		{
			email.POST("", emailOTPHandler.CreateOrUpdateOTP)
			email.POST("/verify", emailOTPHandler.VerifyOTP)
		}
	}
}
