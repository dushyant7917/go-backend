package handler

import "github.com/gin-gonic/gin"

// RegisterRecurringPaymentRoutes registers all recurring payment-related routes
func RegisterRecurringPaymentRoutes(router *gin.RouterGroup, handler *RecurringPaymentHandler) {
	recurringPayments := router.Group("/recurring-payments")
	{
		// Create authorization order (creates customer + order for mandate setup)
		recurringPayments.POST("/authorization-order", handler.CreateAuthorizationOrder)

		// Create registration link for UPI recurring authorization (hosted checkout URL)
		recurringPayments.POST("/registration-link", handler.CreateRegistrationLink)

		// Verify authorization payment and activate mandate
		recurringPayments.POST("/verify-authorization-payment", handler.VerifyAuthorizationPayment)

		// Create order for in-app one-time payment (no mandate, token_id stays nil)
		recurringPayments.POST("/one-time-payment-order", handler.CreateOneTimePaymentOrder)

		// Create hosted payment link for web one-time payment (no mandate, token_id stays nil)
		recurringPayments.POST("/one-time-payment-link", handler.CreateOneTimePaymentLink)

		// Verify one-time payment after in-app checkout SDK callback
		recurringPayments.POST("/verify-one-time-payment", handler.VerifyOneTimePayment)

		// Webhook endpoint for Razorpay events
		recurringPayments.POST("/webhook", handler.HandleWebhook)

		// Get recurring payment status for a user (active subscription + trial status)
		recurringPayments.GET("/status", handler.GetRecurringPaymentStatus)

		// Get recurring payment by ID
		recurringPayments.GET("/:id", handler.GetRecurringPayment)
	}
}
