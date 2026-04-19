package handler

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"go-backend/internal/apps/razorpay/subscription/models"
	"go-backend/internal/apps/razorpay/subscription/service"
	commonResponse "go-backend/internal/common/response"
	"go-backend/pkg/utils"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// SubscriptionHandler handles HTTP requests for subscription operations
type SubscriptionHandler struct {
	service service.SubscriptionService
}

// NewSubscriptionHandler creates a new instance of SubscriptionHandler
func NewSubscriptionHandler(service service.SubscriptionService) *SubscriptionHandler {
	return &SubscriptionHandler{service: service}
}

// CreateCheckoutURL handles POST /api/v1/subscriptions/checkout
// Creates a subscription and returns the checkout URL
func (h *SubscriptionHandler) CreateCheckoutURL(c *gin.Context) {
	var req models.CreateSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	response, err := h.service.CreateCheckoutURL(req)
	if err != nil {
		// Extract more specific error message if possible
		errMsg := err.Error()
		if strings.Contains(errMsg, "does not exist") {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": errMsg,
				"hint":  "Please verify: 1) The plan_id exists in your Razorpay dashboard, 2) You're using the correct mode (test/live) keys matching the plan's mode, 3) The plan_id has no extra spaces or typos",
			})
			return
		}
		commonResponse.Error(c, http.StatusInternalServerError, err, errMsg)
		return
	}

	c.JSON(http.StatusCreated, gin.H{"data": response})
}

// VerifyPayment handles POST /api/v1/subscriptions/verify
// Verifies payment signature after successful payment
func (h *SubscriptionHandler) VerifyPayment(c *gin.Context) {
	var req models.VerifyPaymentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	response, err := h.service.VerifyPayment(req)
	if err != nil {
		if err.Error() == "invalid signature" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "payment verification failed"})
			return
		}
		if err.Error() == "subscription not found" {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		commonResponse.Error(c, http.StatusInternalServerError, err, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":    response,
		"message": "payment verified successfully",
	})
}

// HandleWebhook handles POST /api/v1/subscriptions/webhook
// Receives and processes Razorpay webhook events
func (h *SubscriptionHandler) HandleWebhook(c *gin.Context) {
	// Read raw body
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read request body"})
		return
	}

	// Get signature from header
	signature := c.GetHeader("X-Razorpay-Signature")
	if signature == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing signature header"})
		return
	}

	// Process webhook
	if err := h.service.HandleWebhook(body, signature); err != nil {
		if err.Error() == "invalid webhook signature" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
			return
		}
		commonResponse.Error(c, http.StatusInternalServerError, err, err.Error())
		return
	}

	// Respond with success
	c.JSON(http.StatusOK, gin.H{"message": "webhook processed successfully"})
}

// GetSubscription handles GET /api/v1/subscriptions/:id
// Retrieves subscription details by ID
func (h *SubscriptionHandler) GetSubscription(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid subscription id"})
		return
	}

	subscription, err := h.service.GetSubscriptionByID(id)
	if err != nil {
		if err.Error() == "subscription not found" {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		commonResponse.Error(c, http.StatusInternalServerError, err, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": subscription})
}

// GetSubscriptionByRazorpayID handles GET /api/v1/subscriptions/razorpay/:razorpay_id
// Retrieves subscription details by Razorpay subscription ID
func (h *SubscriptionHandler) GetSubscriptionByRazorpayID(c *gin.Context) {
	razorpayID := c.Param("razorpay_id")
	if razorpayID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "razorpay subscription id required"})
		return
	}

	subscription, err := h.service.GetSubscriptionByRazorpayID(razorpayID)
	if err != nil {
		if err.Error() == "subscription not found" {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		commonResponse.Error(c, http.StatusInternalServerError, err, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": subscription})
}

// GetLatestSubscriptionByPhoneAndApp handles GET /api/v1/subscriptions/latest
// Retrieves the latest subscription for a user by phone number and app name
func (h *SubscriptionHandler) GetLatestSubscriptionByPhoneAndApp(c *gin.Context) {
	phone := c.Query("phone")
	appName := c.Query("app_name")

	if phone == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "phone number is required"})
		return
	}

	if appName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "app_name is required"})
		return
	}

	subscription, err := h.service.GetLatestSubscriptionByPhoneAndApp(phone, appName)
	if err != nil {
		if err.Error() == "subscription not found" {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		commonResponse.Error(c, http.StatusInternalServerError, err, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": subscription})
}

// CancelSubscription handles POST /api/v1/subscriptions/:id/cancel
// Cancels an active subscription
func (h *SubscriptionHandler) CancelSubscription(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid subscription id"})
		return
	}

	err = h.service.CancelSubscription(id)
	if err != nil {
		if err.Error() == "subscription not found" {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		commonResponse.Error(c, http.StatusInternalServerError, err, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "subscription cancelled successfully"})
}

// CheckAuthenticationStatus handles GET /api/v1/subscriptions/check-authentication
// Checks if a phone number has ever had an authenticated subscription
func (h *SubscriptionHandler) CheckAuthenticationStatus(c *gin.Context) {
	phone := c.Query("phone")
	if phone == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "phone number is required"})
		return
	}

	appName := c.Query("app_name")

	response, err := h.service.CheckAuthenticationStatus(phone, appName)
	if err != nil {
		commonResponse.Error(c, http.StatusInternalServerError, err, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": response})
}

// GetSubscriptionStatus handles GET /api/v1/subscriptions/status
// Returns both latest subscription and authentication status in a single call
func (h *SubscriptionHandler) GetSubscriptionStatus(c *gin.Context) {
	phone := c.Query("phone")
	if phone == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "phone number is required"})
		return
	}

	appName := c.Query("app_name")
	if appName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "app_name is required"})
		return
	}

	response, err := h.service.GetSubscriptionStatus(phone, appName)
	if err != nil {
		commonResponse.Error(c, http.StatusInternalServerError, err, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": response})
}

// GetSubscriptionStats handles GET /api/v1/subscriptions/stats
// Returns subscription statistics for last N days grouped by date with pagination
func (h *SubscriptionHandler) GetSubscriptionStats(c *gin.Context) {
	appName := c.Query("app_name")
	if appName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "app_name is required"})
		return
	}

	// Parse days parameter (required)
	daysStr := c.Query("days")
	if daysStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "days parameter is required"})
		return
	}
	days := 0
	if _, err := fmt.Sscanf(daysStr, "%d", &days); err != nil || days <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "days must be a positive integer"})
		return
	}

	// Parse pagination parameters (optional, with defaults)
	page := 1
	if pageStr := c.Query("page"); pageStr != "" {
		if _, err := fmt.Sscanf(pageStr, "%d", &page); err != nil || page < 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "page must be a positive integer"})
			return
		}
	}

	pageSize := 30
	if pageSizeStr := c.Query("page_size"); pageSizeStr != "" {
		if _, err := fmt.Sscanf(pageSizeStr, "%d", &pageSize); err != nil || pageSize < 1 || pageSize > 100 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "page_size must be between 1 and 100"})
			return
		}
	}

	// Get statistics from service
	response, err := h.service.GetSubscriptionStats(appName, days, page, pageSize)
	if err != nil {
		commonResponse.Error(c, http.StatusInternalServerError, err, err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": response})
}

// GetDefaultPlan handles GET /api/v1/subscriptions/default-plan
// Returns the default subscription plan ID and amount from environment variables
func (h *SubscriptionHandler) GetDefaultPlan(c *gin.Context) {
	planID := utils.GetEnv("DEFAULT_SUBSCRIPTION_PLAN_ID", "")
	amount := utils.GetEnv("DEFAULT_SUBSCRIPTION_AMOUNT", "")

	c.JSON(http.StatusOK, gin.H{
		"data": gin.H{
			"plan_id": planID,
			"amount":  amount,
		},
	})
}
