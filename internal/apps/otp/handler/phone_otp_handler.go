package handler

import (
	"net/http"

	"go-backend/internal/apps/otp/models"
	"go-backend/internal/apps/otp/service"
	commonResponse "go-backend/internal/common/response"

	"github.com/gin-gonic/gin"
)

// PhoneOTPHandler handles HTTP endpoints for Phone OTP
type PhoneOTPHandler struct {
	service service.PhoneOTPService
}

// NewPhoneOTPHandler creates a new instance of PhoneOTPHandler
func NewPhoneOTPHandler(service service.PhoneOTPService) *PhoneOTPHandler {
	return &PhoneOTPHandler{service: service}
}

// CreateOrUpdateOTP handles POST /api/v1/otp/phone
func (h *PhoneOTPHandler) CreateOrUpdateOTP(c *gin.Context) {
	var req models.CreatePhoneOTPRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	otp, err := h.service.CreateOrUpdateOTP(req)
	if err != nil {
		commonResponse.Error(c, http.StatusInternalServerError, err, err.Error())
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": otp})
}

// VerifyOTP handles POST /api/v1/otp/phone/verify
func (h *PhoneOTPHandler) VerifyOTP(c *gin.Context) {
	var req models.VerifyPhoneOTPRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := h.service.VerifyOTP(req)
	if err != nil {
		status := http.StatusInternalServerError
		if err.Error() == "otp not found" {
			status = http.StatusNotFound
		}
		if status == http.StatusInternalServerError {
			commonResponse.Error(c, status, err, err.Error())
		} else {
			c.JSON(status, gin.H{"error": err.Error()})
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": resp})
}
