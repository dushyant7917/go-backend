package handler

import (
	"io"
	"log"
	"net/http"

	"go-backend/internal/apps/razorpay/recurring_payment/models"
	"go-backend/internal/apps/razorpay/recurring_payment/service"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RecurringPaymentHandler handles HTTP requests for recurring payment operations
type RecurringPaymentHandler struct {
	service service.RecurringPaymentService
}

// NewRecurringPaymentHandler creates a new instance of RecurringPaymentHandler
func NewRecurringPaymentHandler(service service.RecurringPaymentService) *RecurringPaymentHandler {
	return &RecurringPaymentHandler{service: service}
}

// CreateAuthorizationOrder handles POST /api/v1/recurring-payments/authorization-order
// Creates customer, order, and initializes recurring payment records
func (h *RecurringPaymentHandler) CreateAuthorizationOrder(c *gin.Context) {
	var req models.CreateAuthorizationOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("[CreateAuthorizationOrder] Invalid request body: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	log.Printf("[CreateAuthorizationOrder] Request: user_id=%s, app_name=%s, amount=%d, start_at=%s",
		req.UserID, req.AppName, req.AuthorizationAmount, req.StartAt.Format("2006-01-02"))

	response, err := h.service.CreateAuthorizationOrder(req)
	if err != nil {
		log.Printf("[CreateAuthorizationOrder] Service error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"data": response})
}

// CreateRegistrationLink handles POST /api/v1/recurring-payments/registration-link
// Creates a Razorpay registration link for UPI recurring authorization
func (h *RecurringPaymentHandler) CreateRegistrationLink(c *gin.Context) {
	var req models.CreateRegistrationLinkRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("[CreateRegistrationLink] Invalid request body: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	log.Printf("[CreateRegistrationLink] Request: user_id=%s, app_name=%s, amount=%d, start_at=%s",
		req.UserID, req.AppName, req.AuthorizationAmount, req.StartAt.Format("2006-01-02"))

	response, err := h.service.CreateRegistrationLink(req)
	if err != nil {
		log.Printf("[CreateRegistrationLink] Service error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"data": response})
}

// VerifyAuthorizationPayment handles POST /api/v1/recurring-payments/verify-authorization-payment
// Verifies authorization payment and activates the mandate
func (h *RecurringPaymentHandler) VerifyAuthorizationPayment(c *gin.Context) {
	var req models.VerifyAuthorizationPaymentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("[VerifyAuthorizationPayment] Invalid request body: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	log.Printf("[VerifyAuthorizationPayment] Request: order_id=%s, payment_id=%s",
		req.RazorpayOrderID, req.RazorpayPaymentID)

	response, err := h.service.VerifyAuthorizationPayment(req)
	if err != nil {
		log.Printf("[VerifyAuthorizationPayment] Service error: %v", err)
		if err.Error() == "invalid signature" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "payment verification failed"})
			return
		}
		if err.Error() == "payment attempt not found" {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":    response,
		"message": "authorization payment verified successfully",
	})
}

// GetRecurringPayment handles GET /api/v1/recurring-payments/:id
// Retrieves recurring payment details by ID
func (h *RecurringPaymentHandler) GetRecurringPayment(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		log.Printf("[GetRecurringPayment] Invalid recurring payment id: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid recurring payment id"})
		return
	}

	log.Printf("[GetRecurringPayment] Request: id=%s", id)

	response, err := h.service.GetRecurringPaymentByID(id)
	if err != nil {
		log.Printf("[GetRecurringPayment] Service error: %v", err)
		if err.Error() == "recurring payment not found" {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": response})
}

// GetRecurringPaymentStatus handles GET /api/v1/recurring-payments/status
// Checks if user has active recurring payment and completed authorization
func (h *RecurringPaymentHandler) GetRecurringPaymentStatus(c *gin.Context) {
	userIDStr := c.Query("user_id")
	if userIDStr == "" {
		log.Printf("[GetRecurringPaymentStatus] Missing user_id parameter")
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_id is required"})
		return
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		log.Printf("[GetRecurringPaymentStatus] Invalid user_id: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user_id"})
		return
	}

	appName := c.Query("app_name")
	if appName == "" {
		log.Printf("[GetRecurringPaymentStatus] Missing app_name parameter")
		c.JSON(http.StatusBadRequest, gin.H{"error": "app_name is required"})
		return
	}

	log.Printf("[GetRecurringPaymentStatus] Request: user_id=%s, app_name=%s", userID, appName)

	response, err := h.service.GetRecurringPaymentStatus(userID, appName)
	if err != nil {
		log.Printf("[GetRecurringPaymentStatus] Service error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": response})
}

// HandleWebhook handles POST /api/v1/recurring-payments/webhook
// Receives and processes Razorpay webhook events
func (h *RecurringPaymentHandler) HandleWebhook(c *gin.Context) {
	// Read raw body
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		log.Printf("[HandleWebhook] Failed to read request body: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read request body"})
		return
	}

	// Get signature from header
	signature := c.GetHeader("X-Razorpay-Signature")
	if signature == "" {
		log.Printf("[HandleWebhook] Missing X-Razorpay-Signature header")
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing signature header"})
		return
	}

	log.Printf("[HandleWebhook] Received webhook, body_length=%d", len(body))

	// Process webhook
	if err := h.service.HandleWebhook(body, signature); err != nil {
		log.Printf("[HandleWebhook] Service error: %v", err)
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
