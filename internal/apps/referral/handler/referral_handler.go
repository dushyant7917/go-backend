package handler

import (
	"net/http"

	"go-backend/internal/apps/referral/service"
	commonResponse "go-backend/internal/common/response"

	"github.com/gin-gonic/gin"
)

// ReferralHandler handles HTTP requests for referral operations
type ReferralHandler struct {
	service service.ReferralService
}

// NewReferralHandler creates a new instance of ReferralHandler
func NewReferralHandler(service service.ReferralService) *ReferralHandler {
	return &ReferralHandler{service: service}
}

// GetReferralBonus handles GET /api/v1/referrals/bonus
func (h *ReferralHandler) GetReferralBonus(c *gin.Context) {
	appName := c.Query("app_name")
	referralCode := c.Query("referral_code")
	if appName == "" || referralCode == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "app_name and referral_code are required"})
		return
	}

	resp, err := h.service.GetPendingReferralBonus(appName, referralCode)
	if err != nil {
		commonResponse.Error(c, http.StatusInternalServerError, err, err.Error())
		return
	}

	commonResponse.Success(c, resp)
}
