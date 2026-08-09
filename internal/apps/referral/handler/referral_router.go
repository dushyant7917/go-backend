package handler

import "github.com/gin-gonic/gin"

// RegisterReferralRoutes registers all referral-related routes
func RegisterReferralRoutes(router *gin.RouterGroup, handler *ReferralHandler) {
	referrals := router.Group("/referrals")
	{
		referrals.GET("/bonus", handler.GetReferralBonus)
	}
}
