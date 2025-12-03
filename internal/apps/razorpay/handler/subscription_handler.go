package handler

import (
	"io"
	"net/http"
	"strings"

	"go-backend/internal/apps/razorpay/models"
	"go-backend/internal/apps/razorpay/service"

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
		c.JSON(http.StatusInternalServerError, gin.H{"error": errMsg})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "subscription cancelled successfully"})
}
