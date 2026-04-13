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
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	response, err := h.service.CreateAuthorizationOrder(req)
	if err != nil {
		log.Printf("[CreateAuthorizationOrder] Error: %v", err)
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
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	response, err := h.service.CreateRegistrationLink(req)
	if err != nil {
		log.Printf("[CreateRegistrationLink] Error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	log.Printf("[CreateRegistrationLink] Generated URL: %s", response.ShortURL)
	c.JSON(http.StatusCreated, gin.H{"data": response})
}

// VerifyAuthorizationPayment handles POST /api/v1/recurring-payments/verify-authorization-payment
// Verifies authorization payment and activates the mandate
func (h *RecurringPaymentHandler) VerifyAuthorizationPayment(c *gin.Context) {
	var req models.VerifyAuthorizationPaymentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	response, err := h.service.VerifyAuthorizationPayment(req)
	if err != nil {
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid recurring payment id"})
		return
	}

	response, err := h.service.GetRecurringPaymentByID(id)
	if err != nil {
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_id is required"})
		return
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user_id"})
		return
	}

	appName := c.Query("app_name")
	if appName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "app_name is required"})
		return
	}

	response, err := h.service.GetRecurringPaymentStatus(userID, appName)
	if err != nil {
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
