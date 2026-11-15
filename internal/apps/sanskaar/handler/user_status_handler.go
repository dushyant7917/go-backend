package handler

import (
	"log"
	"net/http"

	metaEventModels "go-backend/internal/apps/meta_event/models"
	metaEventSvc "go-backend/internal/apps/meta_event/service"
	"go-backend/internal/common/subscriptionstatus"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// UserStatusHandler handles the combined user status request for the Sanskaar app: subscription,
// free-trial usage and the pending meta event. Unlike DailyStory there is no legacy subscription
// table to consult, and the status checker it is given covers only the unified autopay system
// (not the old Razorpay stack), so only autopay mandates and pending meta events are checked.
type UserStatusHandler struct {
	statusChecker    *subscriptionstatus.Checker
	metaEventService metaEventSvc.MetaEventService
}

// NewUserStatusHandler creates a new UserStatusHandler
func NewUserStatusHandler(
	statusChecker *subscriptionstatus.Checker,
	metaEventService metaEventSvc.MetaEventService,
) *UserStatusHandler {
	return &UserStatusHandler{
		statusChecker:    statusChecker,
		metaEventService: metaEventService,
	}
}

// UserStatusResponse represents the combined status response
type UserStatusResponse struct {
	ActiveSubscription bool                               `json:"active_subscription"` // Has an active autopay mandate (any gateway)
	UsedFreeTrial      bool                               `json:"used_free_trial"`     // Has completed an autopay authorization (any gateway)
	PendingMetaEvent   *metaEventModels.MetaEventResponse `json:"pending_meta_event"`  // Oldest non-triggered meta event
}

// GetUserStatus handles GET /api/v1/sanskaar/user-status
// Query params: app_name, user_id (both required).
func (h *UserStatusHandler) GetUserStatus(c *gin.Context) {
	userIDStr := c.Query("user_id")
	appName := c.Query("app_name")

	if appName == "" {
		log.Printf("[Sanskaar GetUserStatus] Missing app_name parameter")
		c.JSON(http.StatusBadRequest, gin.H{"error": "app_name is required"})
		return
	}

	if userIDStr == "" {
		log.Printf("[Sanskaar GetUserStatus] Missing user_id parameter")
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_id is required"})
		return
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		log.Printf("[Sanskaar GetUserStatus] Invalid user_id: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user_id"})
		return
	}

	status := h.statusChecker.Check(userID, appName)

	response := &UserStatusResponse{
		ActiveSubscription: status.ActiveSubscription,
		UsedFreeTrial:      status.UsedFreeTrial,
	}

	pendingEvent, err := h.metaEventService.GetPendingEvent(userID, appName)
	if err != nil {
		log.Printf("[Sanskaar GetUserStatus] Failed to get pending meta event: %v", err)
	} else if pendingEvent != nil {
		resp := pendingEvent.ToResponse()
		response.PendingMetaEvent = &resp
	}

	c.JSON(http.StatusOK, gin.H{"data": response})
}
