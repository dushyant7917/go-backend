package handler

import (
	"go-backend/internal/common/middleware"

	"github.com/gin-gonic/gin"
)

// otpRateLimitPerMin is the total requests/min allowed across all OTP routes (per server instance).
// It bounds SMS/email spend if the endpoints are abused.
const otpRateLimitPerMin = 100

// RegisterOTPRoutes registers all OTP routes
func RegisterOTPRoutes(router *gin.RouterGroup, phoneOTPHandler *PhoneOTPHandler, emailOTPHandler *EmailOTPHandler) {
	otp := router.Group("/otp")
	otp.Use(middleware.GlobalRateLimit("otp", otpRateLimitPerMin))
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
