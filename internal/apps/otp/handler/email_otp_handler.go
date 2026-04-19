package handler

import (
	"net/http"

	"go-backend/internal/apps/otp/models"
	"go-backend/internal/apps/otp/service"
	commonResponse "go-backend/internal/common/response"

	"github.com/gin-gonic/gin"
)

// EmailOTPHandler handles HTTP endpoints for Email OTP
type EmailOTPHandler struct {
	service service.EmailOTPService
}

// NewEmailOTPHandler creates a new instance of EmailOTPHandler
func NewEmailOTPHandler(service service.EmailOTPService) *EmailOTPHandler {
	return &EmailOTPHandler{service: service}
}

// CreateOrUpdateOTP handles POST /api/v1/otp/email
func (h *EmailOTPHandler) CreateOrUpdateOTP(c *gin.Context) {
	var req models.CreateEmailOTPRequest
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

// VerifyOTP handles POST /api/v1/otp/email/verify
func (h *EmailOTPHandler) VerifyOTP(c *gin.Context) {
	var req models.VerifyEmailOTPRequest
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
