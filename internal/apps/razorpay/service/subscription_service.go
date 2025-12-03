package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"go-backend/internal/apps/razorpay/models"
	"go-backend/internal/apps/razorpay/repository"

	"github.com/google/uuid"
	razorpay "github.com/razorpay/razorpay-go"
	"gorm.io/gorm"
)

// SubscriptionService defines the interface for subscription business logic
type SubscriptionService interface {
	CreateCheckoutURL(req models.CreateSubscriptionRequest) (*models.CheckoutURLResponse, error)
	VerifyPayment(req models.VerifyPaymentRequest) (*models.SubscriptionResponse, error)
	HandleWebhook(payload []byte, signature string) error
	GetSubscriptionByID(id uuid.UUID) (*models.SubscriptionResponse, error)
	GetSubscriptionByRazorpayID(razorpaySubID string) (*models.SubscriptionResponse, error)
	CancelSubscription(id uuid.UUID) error
}

// subscriptionService implements SubscriptionService interface
type subscriptionService struct {
	repo              repository.SubscriptionRepository
	razorpayClient    *razorpay.Client
	razorpayKeySecret string
	webhookSecret     string
}

// NewSubscriptionService creates a new instance of SubscriptionService
func NewSubscriptionService(
	repo repository.SubscriptionRepository,
	razorpayKeyID string,
	razorpayKeySecret string,
	webhookSecret string,
) SubscriptionService {
	client := razorpay.NewClient(razorpayKeyID, razorpayKeySecret)
	return &subscriptionService{
		repo:              repo,
		razorpayClient:    client,
		razorpayKeySecret: razorpayKeySecret,
		webhookSecret:     webhookSecret,
	}
}

// CreateCheckoutURL creates a subscription and returns checkout URL
func (s *subscriptionService) CreateCheckoutURL(req models.CreateSubscriptionRequest) (*models.CheckoutURLResponse, error) {
	// Prepare subscription data - do NOT include customer_id initially
	// Customer will be linked automatically after authorization payment
	subscriptionData := map[string]interface{}{
		"plan_id":         req.PlanID,
		"quantity":        1,
		"customer_notify": false,
		"addons": []map[string]interface{}{
			{
				"item": map[string]interface{}{
					"name":     "Initial Charge",
					"amount":   100, // ₹1 in paise (day 0)
					"currency": "INR",
				},
			},
		},
	}

	// Set total_count - Razorpay requires either total_count or end_at
	// Default to 120 (10 years for monthly subscriptions) if not specified
	if req.TotalCount > 0 {
		subscriptionData["total_count"] = req.TotalCount
	} else {
		subscriptionData["total_count"] = 120
	}

	// Set expire_by to 7 days from now for the checkout link
	expireBy := time.Now().Add(7 * 24 * time.Hour).Unix()
	subscriptionData["expire_by"] = expireBy

	// Set start_at to 1 day from now for first subscription payment (day 1)
	// The addon (₹1) will be charged immediately on day 0
	startAt := time.Now().Add(24 * time.Hour).Unix()
	subscriptionData["start_at"] = startAt

	// Override with user-provided start_at if specified
	if req.StartAt != nil {
		subscriptionData["start_at"] = *req.StartAt
	}

	if req.Quantity > 0 {
		subscriptionData["quantity"] = req.Quantity
	}

	if req.Notes != nil {
		subscriptionData["notes"] = req.Notes
	}

	// Create subscription in Razorpay
	fmt.Printf("Creating subscription with data: %+v\n", subscriptionData)
	razorpaySub, err := s.razorpayClient.Subscription.Create(subscriptionData, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create razorpay subscription: %w", err)
	}
	fmt.Printf("Razorpay subscription response: %+v\n", razorpaySub)

	// Extract subscription details
	razorpaySubID := razorpaySub["id"].(string)
	shortURL := razorpaySub["short_url"].(string)
	status := razorpaySub["status"].(string)

	// Extract customer_id if available (will be populated after authorization)
	var customerID string
	if custID, ok := razorpaySub["customer_id"].(string); ok {
		customerID = custID
	}

	// Get plan details to extract amount
	planID := razorpaySub["plan_id"].(string)
	plan, err := s.razorpayClient.Plan.Fetch(planID, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch plan: %w", err)
	}

	amount := int64(plan["item"].(map[string]interface{})["amount"].(float64))
	currency := plan["item"].(map[string]interface{})["currency"].(string)
	frequency := plan["period"].(string)

	// Convert metadata to JSON
	metadataJSON := "{}"
	if req.Notes != nil {
		metadataBytes, _ := json.Marshal(req.Notes)
		metadataJSON = string(metadataBytes)
	}

	// Save subscription to database
	subscription := &models.Subscription{
		UserID:                 req.UserID,
		AppName:                req.AppName,
		Phone:                  req.Phone,
		Email:                  req.Email,
		RazorpaySubscriptionID: razorpaySubID,
		RazorpayCustomerID:     customerID,
		RazorpayPlanID:         planID,
		Status:                 models.SubscriptionStatus(status),
		Amount:                 amount,
		Currency:               currency,
		Frequency:              frequency,
		TotalCount:             req.TotalCount,
		ShortURL:               shortURL,
		Metadata:               metadataJSON,
	}

	if err := s.repo.Create(subscription); err != nil {
		return nil, fmt.Errorf("failed to save subscription: %w", err)
	}

	return &models.CheckoutURLResponse{
		SubscriptionID:         subscription.ID,
		RazorpaySubscriptionID: razorpaySubID,
		ShortURL:               shortURL,
		Status:                 status,
	}, nil
}

// VerifyPayment verifies the payment signature
func (s *subscriptionService) VerifyPayment(req models.VerifyPaymentRequest) (*models.SubscriptionResponse, error) {
	// Verify signature
	message := req.RazorpayPaymentID + "|" + req.RazorpaySubscriptionID
	if !s.verifySignature(message, req.RazorpaySignature) {
		return nil, errors.New("invalid signature")
	}

	// Fetch subscription from database
	subscription, err := s.repo.FindByRazorpaySubscriptionID(req.RazorpaySubscriptionID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("subscription not found")
		}
		return nil, err
	}

	// Fetch subscription details from Razorpay
	razorpaySub, err := s.razorpayClient.Subscription.Fetch(req.RazorpaySubscriptionID, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch razorpay subscription: %w", err)
	}

	// Update subscription status from Razorpay
	subscription.Status = models.SubscriptionStatus(razorpaySub["status"].(string))
	if err := s.repo.Update(subscription); err != nil {
		return nil, err
	}

	response := subscription.ToResponse()
	return &response, nil
}

// HandleWebhook handles Razorpay webhook events
func (s *subscriptionService) HandleWebhook(payload []byte, signature string) error {
	// Verify webhook signature
	if !s.verifyWebhookSignature(payload, signature) {
		fmt.Printf("Webhook signature verification failed. signature=%s\n", signature)
		return errors.New("invalid webhook signature")
	}

	// Parse webhook payload
	var event map[string]interface{}
	if err := json.Unmarshal(payload, &event); err != nil {
		return fmt.Errorf("failed to parse webhook payload: %w", err)
	}

	eventType := event["event"].(string)
	payloadData := event["payload"].(map[string]interface{})
	fmt.Printf("Webhook event received: %s\n", eventType)
	// Attempt to log subscription info if present
	if subWrap, ok := payloadData["subscription"].(map[string]interface{}); ok {
		if entity, ok := subWrap["entity"].(map[string]interface{}); ok {
			id, _ := entity["id"].(string)
			status, _ := entity["status"].(string)
			fmt.Printf("Subscription entity: id=%s status=%s\n", id, status)
		}
	}
	// Attempt to log payment info if present
	if payWrap, ok := payloadData["payment"].(map[string]interface{}); ok {
		if entity, ok := payWrap["entity"].(map[string]interface{}); ok {
			pid, _ := entity["id"].(string)
			pstatus, _ := entity["status"].(string)
			fmt.Printf("Payment entity: id=%s status=%s\n", pid, pstatus)
		}
	}
	// Handle different event types
	switch eventType {
	case "subscription.authenticated":
		return s.handleSubscriptionAuthenticated(payloadData)
	case "subscription.activated":
		return s.handleSubscriptionActivated(payloadData)
	case "subscription.charged":
		return s.handleSubscriptionCharged(payloadData)
	case "subscription.pending":
		return s.handleSubscriptionPending(payloadData)
	case "subscription.halted":
		return s.handleSubscriptionHalted(payloadData)
	case "subscription.cancelled":
		return s.handleSubscriptionCancelled(payloadData)
	case "subscription.completed":
		return s.handleSubscriptionCompleted(payloadData)
	case "subscription.paused":
		return s.handleSubscriptionPaused(payloadData)
	case "subscription.resumed":
		return s.handleSubscriptionResumed(payloadData)
	default:
		// Log unknown event type but don't error
		return nil
	}
}

// GetSubscriptionByID retrieves a subscription by its ID
func (s *subscriptionService) GetSubscriptionByID(id uuid.UUID) (*models.SubscriptionResponse, error) {
	subscription, err := s.repo.FindByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("subscription not found")
		}
		return nil, err
	}

	response := subscription.ToResponse()
	return &response, nil
}

// GetSubscriptionByRazorpayID retrieves a subscription by Razorpay subscription ID
func (s *subscriptionService) GetSubscriptionByRazorpayID(razorpaySubID string) (*models.SubscriptionResponse, error) {
	subscription, err := s.repo.FindByRazorpaySubscriptionID(razorpaySubID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("subscription not found")
		}
		return nil, err
	}

	response := subscription.ToResponse()
	return &response, nil
}

// CancelSubscription cancels a subscription
func (s *subscriptionService) CancelSubscription(id uuid.UUID) error {
	subscription, err := s.repo.FindByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("subscription not found")
		}
		return err
	}

	// Cancel in Razorpay
	cancelData := map[string]interface{}{
		"cancel_at_cycle_end": 0,
	}
	_, err = s.razorpayClient.Subscription.Cancel(subscription.RazorpaySubscriptionID, cancelData, nil)
	if err != nil {
		return fmt.Errorf("failed to cancel razorpay subscription: %w", err)
	}

	// Update status in database
	subscription.Status = models.SubscriptionStatusCancelled
	return s.repo.Update(subscription)
}

// verifySignature verifies Razorpay signature
func (s *subscriptionService) verifySignature(message, signature string) bool {
	mac := hmac.New(sha256.New, []byte(s.razorpayKeySecret))
	mac.Write([]byte(message))
	expectedMAC := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(signature), []byte(expectedMAC))
}

// verifyWebhookSignature verifies webhook signature
func (s *subscriptionService) verifyWebhookSignature(payload []byte, signature string) bool {
	mac := hmac.New(sha256.New, []byte(s.webhookSecret))
	mac.Write(payload)
	expectedMAC := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(signature), []byte(expectedMAC))
}

// handleSubscriptionAuthenticated handles subscription.authenticated event
func (s *subscriptionService) handleSubscriptionAuthenticated(payload map[string]interface{}) error {
	subscriptionEntity := payload["subscription"].(map[string]interface{})["entity"].(map[string]interface{})
	razorpaySubID := subscriptionEntity["id"].(string)

	subscription, err := s.repo.FindByRazorpaySubscriptionID(razorpaySubID)
	if err != nil {
		return err
	}

	// Persist customer_id if present, and any timing hints
	if custID, ok := subscriptionEntity["customer_id"].(string); ok {
		subscription.RazorpayCustomerID = custID
	}
	if startAt, ok := subscriptionEntity["start_at"].(float64); ok {
		t := time.Unix(int64(startAt), 0)
		subscription.StartAt = &t
	}
	if chargeAt, ok := subscriptionEntity["charge_at"].(float64); ok {
		t := time.Unix(int64(chargeAt), 0)
		subscription.NextChargeAt = &t
	}
	// Record authentication marker in metadata
	meta := map[string]interface{}{}
	_ = json.Unmarshal([]byte(subscription.Metadata), &meta)
	meta["authenticated"] = true
	meta["authenticated_at"] = time.Now().UTC().Format(time.RFC3339)
	b, _ := json.Marshal(meta)
	subscription.Metadata = string(b)

	// Update status to authenticated from Razorpay webhook
	if rzpStatus, ok := subscriptionEntity["status"].(string); ok {
		subscription.Status = models.SubscriptionStatus(rzpStatus)
	}

	return s.repo.Update(subscription)
}

// handleSubscriptionActivated handles subscription.activated event
func (s *subscriptionService) handleSubscriptionActivated(payload map[string]interface{}) error {
	subscriptionEntity := payload["subscription"].(map[string]interface{})["entity"].(map[string]interface{})
	razorpaySubID := subscriptionEntity["id"].(string)

	subscription, err := s.repo.FindByRazorpaySubscriptionID(razorpaySubID)
	if err != nil {
		return err
	}

	// Do not mark active here. Only set start_at to track schedule.
	if startAt, ok := subscriptionEntity["start_at"].(float64); ok {
		t := time.Unix(int64(startAt), 0)
		subscription.StartAt = &t
	}

	return s.repo.Update(subscription)
}

// handleSubscriptionCharged handles subscription.charged event
func (s *subscriptionService) handleSubscriptionCharged(payload map[string]interface{}) error {
	subscriptionEntity := payload["subscription"].(map[string]interface{})["entity"].(map[string]interface{})
	razorpaySubID := subscriptionEntity["id"].(string)

	subscription, err := s.repo.FindByRazorpaySubscriptionID(razorpaySubID)
	if err != nil {
		return err
	}

	// Update next charge date if available
	if chargeAt, ok := subscriptionEntity["charge_at"].(float64); ok {
		t := time.Unix(int64(chargeAt), 0)
		subscription.NextChargeAt = &t
	}
	// Mark active on first successful charge
	if subscription.Status == models.SubscriptionStatusCreated || subscription.Status == models.SubscriptionStatusAuthenticated {
		subscription.Status = models.SubscriptionStatusActive
	}

	return s.repo.Update(subscription)
}

// handleSubscriptionPending handles subscription.pending event
func (s *subscriptionService) handleSubscriptionPending(payload map[string]interface{}) error {
	subscriptionEntity := payload["subscription"].(map[string]interface{})["entity"].(map[string]interface{})
	razorpaySubID := subscriptionEntity["id"].(string)

	return s.repo.UpdateStatus(uuid.MustParse(razorpaySubID), models.SubscriptionStatusCreated)
}

// handleSubscriptionHalted handles subscription.halted event
func (s *subscriptionService) handleSubscriptionHalted(payload map[string]interface{}) error {
	subscriptionEntity := payload["subscription"].(map[string]interface{})["entity"].(map[string]interface{})
	razorpaySubID := subscriptionEntity["id"].(string)

	subscription, err := s.repo.FindByRazorpaySubscriptionID(razorpaySubID)
	if err != nil {
		return err
	}

	subscription.Status = models.SubscriptionStatusExpired
	return s.repo.Update(subscription)
}

// handleSubscriptionCancelled handles subscription.cancelled event
func (s *subscriptionService) handleSubscriptionCancelled(payload map[string]interface{}) error {
	subscriptionEntity := payload["subscription"].(map[string]interface{})["entity"].(map[string]interface{})
	razorpaySubID := subscriptionEntity["id"].(string)

	subscription, err := s.repo.FindByRazorpaySubscriptionID(razorpaySubID)
	if err != nil {
		return err
	}

	subscription.Status = models.SubscriptionStatusCancelled
	if endAt, ok := subscriptionEntity["end_at"].(float64); ok {
		t := time.Unix(int64(endAt), 0)
		subscription.EndAt = &t
	}

	return s.repo.Update(subscription)
}

// handleSubscriptionCompleted handles subscription.completed event
func (s *subscriptionService) handleSubscriptionCompleted(payload map[string]interface{}) error {
	subscriptionEntity := payload["subscription"].(map[string]interface{})["entity"].(map[string]interface{})
	razorpaySubID := subscriptionEntity["id"].(string)

	subscription, err := s.repo.FindByRazorpaySubscriptionID(razorpaySubID)
	if err != nil {
		return err
	}

	subscription.Status = models.SubscriptionStatusCompleted
	if endAt, ok := subscriptionEntity["ended_at"].(float64); ok {
		t := time.Unix(int64(endAt), 0)
		subscription.EndAt = &t
	}

	return s.repo.Update(subscription)
}

// handleSubscriptionPaused handles subscription.paused event
func (s *subscriptionService) handleSubscriptionPaused(payload map[string]interface{}) error {
	subscriptionEntity := payload["subscription"].(map[string]interface{})["entity"].(map[string]interface{})
	razorpaySubID := subscriptionEntity["id"].(string)

	subscription, err := s.repo.FindByRazorpaySubscriptionID(razorpaySubID)
	if err != nil {
		return err
	}

	subscription.Status = models.SubscriptionStatusPaused
	return s.repo.Update(subscription)
}

// handleSubscriptionResumed handles subscription.resumed event
func (s *subscriptionService) handleSubscriptionResumed(payload map[string]interface{}) error {
	subscriptionEntity := payload["subscription"].(map[string]interface{})["entity"].(map[string]interface{})
	razorpaySubID := subscriptionEntity["id"].(string)

	subscription, err := s.repo.FindByRazorpaySubscriptionID(razorpaySubID)
	if err != nil {
		return err
	}

	subscription.Status = models.SubscriptionStatusActive
	return s.repo.Update(subscription)
}

// contains checks if a string contains a substring
func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
