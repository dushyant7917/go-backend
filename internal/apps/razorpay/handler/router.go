package handler

import "github.com/gin-gonic/gin"

// RegisterSubscriptionRoutes registers all subscription-related routes
func RegisterSubscriptionRoutes(router *gin.RouterGroup, handler *SubscriptionHandler) {
	subscriptions := router.Group("/subscriptions")
	{
		// Create checkout URL for UPI Autopay subscription
		subscriptions.POST("/checkout", handler.CreateCheckoutURL)

		// Verify payment after successful checkout
		subscriptions.POST("/verify", handler.VerifyPayment)

		// Webhook endpoint for Razorpay events
		subscriptions.POST("/webhook", handler.HandleWebhook)

		// Get latest subscription by phone number and app name
		subscriptions.GET("/latest", handler.GetLatestSubscriptionByPhoneAndApp)

		// Get subscription by internal ID
		subscriptions.GET("/:id", handler.GetSubscription)

		// Get subscription by Razorpay subscription ID
		subscriptions.GET("/razorpay/:razorpay_id", handler.GetSubscriptionByRazorpayID)

		// Cancel subscription
		subscriptions.POST("/:id/cancel", handler.CancelSubscription)
	}
}
