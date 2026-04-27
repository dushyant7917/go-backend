package handler

import (
	"log"
	"net/http"

	metaEventModels "go-backend/internal/apps/meta_event/models"
	metaEventSvc "go-backend/internal/apps/meta_event/service"
	recurringPaymentModels "go-backend/internal/apps/razorpay/recurring_payment/models"
	recurringPaymentRepo "go-backend/internal/apps/razorpay/recurring_payment/repository"
	subscriptionModels "go-backend/internal/apps/razorpay/subscription/models"
	subscriptionRepo "go-backend/internal/apps/razorpay/subscription/repository"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// DailystoryHandler handles dailystory-related requests
type DailystoryHandler struct {
	subscriptionRepo     subscriptionRepo.SubscriptionRepository
	recurringPaymentRepo recurringPaymentRepo.RecurringPaymentRepository
	metaEventService     metaEventSvc.MetaEventService
}

// NewDailystoryHandler creates a new DailystoryHandler
func NewDailystoryHandler(
	subscriptionRepo subscriptionRepo.SubscriptionRepository,
	recurringPaymentRepo recurringPaymentRepo.RecurringPaymentRepository,
	metaEventService metaEventSvc.MetaEventService,
) *DailystoryHandler {
	return &DailystoryHandler{
		subscriptionRepo:     subscriptionRepo,
		recurringPaymentRepo: recurringPaymentRepo,
		metaEventService:     metaEventService,
	}
}

// CombinedSubscriptionStatusResponse represents the combined status response
type CombinedSubscriptionStatusResponse struct {
	ActiveSubscription bool                               `json:"active_subscription"` // Has active subscription OR recurring payment
	UsedFreeTrial      bool                               `json:"used_free_trial"`     // Has authenticated OR completed authorization
	PendingMetaEvent   *metaEventModels.MetaEventResponse `json:"pending_meta_event"`  // Oldest non-triggered meta event
}

// GetCombinedStatus handles GET /api/v1/dailystory/subscription/status
// Returns combined status from both old subscriptions and new recurring payments
func (h *DailystoryHandler) GetCombinedStatus(c *gin.Context) {
	phone := c.Query("phone")
	userIDStr := c.Query("user_id")
	appName := c.Query("app_name")

	if appName == "" {
		log.Printf("[GetCombinedStatus] Missing app_name parameter")
		c.JSON(http.StatusBadRequest, gin.H{"error": "app_name is required"})
		return
	}

	if phone == "" {
		log.Printf("[GetCombinedStatus] Missing phone parameter")
		c.JSON(http.StatusBadRequest, gin.H{"error": "phone is required"})
		return
	}

	if userIDStr == "" {
		log.Printf("[GetCombinedStatus] Missing user_id parameter")
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_id is required"})
		return
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		log.Printf("[GetCombinedStatus] Invalid user_id: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user_id"})
		return
	}

	response := &CombinedSubscriptionStatusResponse{}

	// Check old subscription status
	subStatus := h.checkSubscriptionStatus(phone, appName)
	response.ActiveSubscription = subStatus.Active
	response.UsedFreeTrial = subStatus.HasAuthenticated

	// Check new recurring payment status and OR the results
	rpStatus := h.checkRecurringPaymentStatus(userID, appName)
	response.ActiveSubscription = response.ActiveSubscription || rpStatus.ActiveSubscription
	response.UsedFreeTrial = response.UsedFreeTrial || rpStatus.UsedFreeTrial

	// Check for pending meta event
	pendingEvent, err := h.metaEventService.GetPendingEvent(userID, appName)
	if err != nil {
		log.Printf("[GetCombinedStatus] Failed to get pending meta event: %v", err)
	} else if pendingEvent != nil {
		resp := pendingEvent.ToResponse()
		response.PendingMetaEvent = &resp
	}

	c.JSON(http.StatusOK, gin.H{"data": response})
}

// checkSubscriptionStatus checks old subscription system status
func (h *DailystoryHandler) checkSubscriptionStatus(phone, appName string) *subscriptionModels.SubscriptionStatusResponse {
	result := &subscriptionModels.SubscriptionStatusResponse{}

	// Check for active subscription
	sub, err := h.subscriptionRepo.FindByPhoneAndAppName(phone, appName)
	if err == nil && sub != nil {
		result.Active = sub.Status == "active" || sub.Status == "authenticated"
	}

	// Check if ever authenticated
	hasAuth, err := h.subscriptionRepo.HasAuthenticatedSubscriptionByPhone(phone, appName)
	if err == nil {
		result.HasAuthenticated = hasAuth
	}

	return result
}

// checkRecurringPaymentStatus checks new recurring payment system status
func (h *DailystoryHandler) checkRecurringPaymentStatus(userID uuid.UUID, appName string) *recurringPaymentModels.RecurringPaymentStatusResponse {
	result := &recurringPaymentModels.RecurringPaymentStatusResponse{}

	// Check for active recurring payment
	_, err := h.recurringPaymentRepo.FindActiveRecurringPaymentByUserID(userID, appName)
	if err == nil {
		result.ActiveSubscription = true
	}

	// Check if ever completed authorization
	hasCompleted, err := h.recurringPaymentRepo.HasCompletedAuthorizationPayment(userID, appName)
	if err == nil {
		result.UsedFreeTrial = hasCompleted
	}

	return result
}
