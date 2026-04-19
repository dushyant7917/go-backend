package response

import (
	"net/http"

	"github.com/getsentry/sentry-go"
	sentrygin "github.com/getsentry/sentry-go/gin"
	"github.com/gin-gonic/gin"
)

// Error sends an error response and captures 5xx errors in Sentry.
// Use this instead of c.JSON() for error responses to ensure errors are reported.
func Error(c *gin.Context, status int, err error, message string) {
	if status >= 500 && err != nil {
		if hub := sentrygin.GetHubFromContext(c); hub != nil {
			hub.CaptureException(err)
		}
	}
	c.JSON(status, gin.H{"error": message})
}

// ErrorWithDetails sends an error response with additional details and captures 5xx errors in Sentry.
func ErrorWithDetails(c *gin.Context, status int, err error, message string, details gin.H) {
	if status >= 500 && err != nil {
		if hub := sentrygin.GetHubFromContext(c); hub != nil {
			hub.CaptureException(err)
		}
	}
	response := gin.H{"error": message}
	for k, v := range details {
		response[k] = v
	}
	c.JSON(status, response)
}

// Success sends a success response with optional data.
func Success(c *gin.Context, data interface{}) {
	if data == nil {
		c.JSON(http.StatusOK, gin.H{"message": "success"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": data})
}

// SuccessWithMessage sends a success response with a custom message.
func SuccessWithMessage(c *gin.Context, message string, data interface{}) {
	response := gin.H{"message": message}
	if data != nil {
		response["data"] = data
	}
	c.JSON(http.StatusOK, response)
}

// CaptureError manually captures an error in Sentry without sending a response.
// Useful when you want to report an error but handle the response differently.
func CaptureError(c *gin.Context, err error) {
	if hub := sentrygin.GetHubFromContext(c); hub != nil {
		hub.CaptureException(err)
	} else {
		sentry.CaptureException(err)
	}
}
