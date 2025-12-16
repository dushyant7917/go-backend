package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	clientModels "go-backend/internal/apps/razorpay/config/models"
	"go-backend/internal/apps/razorpay/config/repository"
	"go-backend/internal/apps/razorpay/subscription/models"
	razorpayRepository "go-backend/internal/apps/razorpay/subscription/repository"
	"go-backend/pkg/utils"

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
	GetLatestSubscriptionByPhoneAndApp(phone string, appName string) (*models.SubscriptionResponse, error)
	CancelSubscription(id uuid.UUID) error
	CheckAuthenticationStatus(phone string, appName string) (*models.CheckAuthenticationStatusResponse, error)
}

// subscriptionService implements SubscriptionService interface
type subscriptionService struct {
	repo        razorpayRepository.SubscriptionRepository
	configRepo  repository.RazorpayConfigRepository
	clientCache map[string]*razorpay.Client // Cache Razorpay clients by app_name:environment
	cacheMutex  sync.RWMutex                // Protect concurrent access to cache
}

// NewSubscriptionService creates a new instance of SubscriptionService
func NewSubscriptionService(
	repo razorpayRepository.SubscriptionRepository,
	configRepo repository.RazorpayConfigRepository,
) SubscriptionService {
	return &subscriptionService{
		repo:        repo,
		configRepo:  configRepo,
		clientCache: make(map[string]*razorpay.Client),
	}
}

// getRazorpayClient returns a cached Razorpay client or creates a new one
// This optimizes connection reuse and avoids creating clients repeatedly
func (s *subscriptionService) getRazorpayClient(config *clientModels.RazorpayConfig) *razorpay.Client {
	// Use app_name + environment as cache key for unique client identification
	cacheKey := config.AppName + ":" + config.Environment

	// Try to get from cache with read lock
	s.cacheMutex.RLock()
	cachedClient, exists := s.clientCache[cacheKey]
	s.cacheMutex.RUnlock()

	if exists {
		return cachedClient
	}

	// Create new client with write lock
	s.cacheMutex.Lock()
	defer s.cacheMutex.Unlock()

	// Double-check after acquiring write lock (another goroutine might have created it)
	if cachedClient, exists := s.clientCache[cacheKey]; exists {
		return cachedClient
	}

	// Create and cache new Razorpay client
	newClient := razorpay.NewClient(config.RazorpayKeyID, config.RazorpayKeySecret)
	s.clientCache[cacheKey] = newClient

	fmt.Printf("[getRazorpayClient] Created and cached new Razorpay client for app: %s\n", config.AppName)
	return newClient
}

// CreateCheckoutURL creates a subscription and returns checkout URL
func (s *subscriptionService) CreateCheckoutURL(req models.CreateSubscriptionRequest) (*models.CheckoutURLResponse, error) {
	// Get razorpay config based on app_name or config_id
	var config *clientModels.RazorpayConfig
	var err error

	if req.ClientID != nil {
		config, err = s.configRepo.FindByID(*req.ClientID)
	} else {
		// Use server-side environment (derived from GO_ENV)
		env := utils.GetRazorpayEnvironment()
		config, err = s.configRepo.FindByAppNameAndEnv(req.AppName, env)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to find razorpay config: %w", err)
	}

	if !config.IsActive {
		return nil, errors.New("razorpay config is not active")
	}

	// Get or create cached Razorpay client for this config's credentials
	razorpayClient := s.getRazorpayClient(config)

	// Log the incoming plan_id for debugging
	fmt.Printf("[CreateCheckoutURL] Received plan_id: '%s' (length: %d)\n", req.PlanID, len(req.PlanID))

	// Trim whitespace from plan_id to avoid validation issues
	planID := strings.TrimSpace(req.PlanID)
	if planID == "" {
		return nil, errors.New("plan_id is required")
	}
	fmt.Printf("[CreateCheckoutURL] Trimmed plan_id: '%s'\n", planID)

	// Determine initial charge amount (in paise)
	// If provided by client, honor zero; default is ₹1 only when not provided
	initialChargeAmountPaise := 100
	if req.InitialChargeAmount != nil {
		if *req.InitialChargeAmount <= 0 {
			initialChargeAmountPaise = 0
		} else {
			initialChargeAmountPaise = *req.InitialChargeAmount * 100 // Convert rupees to paise
		}
	}
	fmt.Printf("[CreateCheckoutURL] Initial charge amount: ₹%d (paise: %d)\n", initialChargeAmountPaise/100, initialChargeAmountPaise)

	// Determine first subscription charge delay (in days)
	// Default to 1 day if not specified
	firstChargeDelayDays := 1
	if req.FirstChargeDelayDays != nil && *req.FirstChargeDelayDays >= 0 {
		firstChargeDelayDays = *req.FirstChargeDelayDays
	}
	fmt.Printf("[CreateCheckoutURL] First charge delay: %d days\n", firstChargeDelayDays)

	// If both initial_charge_amount and first_charge_delay_days are explicitly 0,
	// use plan amount as the initial charge and set delay from plan period
	if req.InitialChargeAmount != nil && req.FirstChargeDelayDays != nil &&
		*req.InitialChargeAmount == 0 && *req.FirstChargeDelayDays == 0 {
		// Fetch plan to derive amount and period
		planInfo, err := razorpayClient.Plan.Fetch(planID, nil, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch plan: %w", err)
		}
		planItem := planInfo["item"].(map[string]interface{})
		planAmountPaise := int(planItem["amount"].(float64))
		planPeriod := strings.ToLower(planInfo["period"].(string))
		// Map period to days
		periodDays := 30
		switch planPeriod {
		case "daily":
			periodDays = 1
		case "weekly":
			periodDays = 7
		case "monthly":
			periodDays = 30
		case "yearly":
			periodDays = 365
		}
		initialChargeAmountPaise = planAmountPaise
		firstChargeDelayDays = periodDays
		fmt.Printf("[CreateCheckoutURL] Overriding: initial charge = plan amount (₹%d), delay = %d days (from period '%s')\n",
			initialChargeAmountPaise/100, firstChargeDelayDays, planPeriod)
	}

	// Prepare subscription data - do NOT include customer_id initially
	// Customer will be linked automatically after authorization payment
	subscriptionData := map[string]interface{}{
		"plan_id":         planID,
		"quantity":        1,
		"customer_notify": false,
	}

	// Only add addon if initial charge amount is greater than 0
	// If initial_charge_amount is 0, skip the addon entirely
	if initialChargeAmountPaise > 0 {
		subscriptionData["addons"] = []map[string]interface{}{
			{
				"item": map[string]interface{}{
					"name":     "Initial Charge",
					"amount":   initialChargeAmountPaise,
					"currency": "INR",
				},
			},
		}
		fmt.Printf("[CreateCheckoutURL] Adding addon charge of ₹%d\n", initialChargeAmountPaise/100)
	} else {
		fmt.Printf("[CreateCheckoutURL] No addon charge - subscription will charge plan amount immediately\n")
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

	// Set start_at based on firstChargeDelayDays
	// If delay is 0, we want immediate first charge - but Razorpay requires start_at in future
	// When both initial_charge_amount=0 and first_charge_delay_days=0:
	// - No addon is added (see above)
	// - start_at is set to minimal future time
	// - User pays full plan amount on authorization (Razorpay's default UPI Autopay flow)
	if firstChargeDelayDays > 0 {
		startAt := time.Now().Add(time.Duration(firstChargeDelayDays) * 24 * time.Hour).Unix()
		subscriptionData["start_at"] = startAt
		fmt.Printf("[CreateCheckoutURL] First subscription charge scheduled for %d days from now\n", firstChargeDelayDays)
	} else {
		// For immediate charge: set start_at to minimum allowed (1 hour)
		// Razorpay will charge the plan amount on authorization
		// Note: With UPI Autopay, the first charge typically happens during authorization
		startAt := time.Now().Add(1 * time.Hour).Unix()
		subscriptionData["start_at"] = startAt
		fmt.Printf("[CreateCheckoutURL] Immediate first charge - start_at set to minimum (1 hour)\n")
	}

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
	fmt.Printf("[CreateCheckoutURL] Creating subscription with data: %+v\n", subscriptionData)
	razorpaySub, err := razorpayClient.Subscription.Create(subscriptionData, nil)
	if err != nil {
		// Enhanced error logging to help diagnose plan_id issues
		fmt.Printf("[CreateCheckoutURL ERROR] Failed to create Razorpay subscription\n")
		fmt.Printf("[CreateCheckoutURL ERROR] Plan ID used: '%s'\n", planID)
		fmt.Printf("[CreateCheckoutURL ERROR] Full subscription data: %+v\n", subscriptionData)
		fmt.Printf("[CreateCheckoutURL ERROR] Razorpay error: %v\n", err)
		return nil, fmt.Errorf("failed to create razorpay subscription with plan_id '%s': %w", planID, err)
	}
	fmt.Printf("[CreateCheckoutURL] Razorpay subscription created successfully: %+v\n", razorpaySub)

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
	razorpayPlanID := razorpaySub["plan_id"].(string)
	plan, err := razorpayClient.Plan.Fetch(razorpayPlanID, nil, nil)
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
		RazorpayConfigID:       config.ID,
		UserID:                 req.UserID,
		AppName:                req.AppName,
		Phone:                  req.Phone,
		Email:                  req.Email,
		RazorpaySubscriptionID: razorpaySubID,
		RazorpayCustomerID:     customerID,
		RazorpayPlanID:         razorpayPlanID,
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
	// Fetch subscription from database first to get razorpay_config_id
	subscription, err := s.repo.FindByRazorpaySubscriptionID(req.RazorpaySubscriptionID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("subscription not found")
		}
		return nil, err
	}

	// Get razorpay config
	config, err := s.configRepo.FindByID(subscription.RazorpayConfigID)
	if err != nil {
		return nil, fmt.Errorf("failed to find razorpay config: %w", err)
	}

	// Verify signature using config's key secret
	message := req.RazorpayPaymentID + "|" + req.RazorpaySubscriptionID
	if !s.verifySignature(message, req.RazorpaySignature, config.RazorpayKeySecret) {
		return nil, errors.New("invalid signature")
	}

	// Get or create cached Razorpay client for this config's credentials
	razorpayClient := s.getRazorpayClient(config)

	// Fetch subscription details from Razorpay to verify it exists
	_, err = razorpayClient.Subscription.Fetch(req.RazorpaySubscriptionID, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch razorpay subscription: %w", err)
	}

	// After successful signature verification, set status to authenticated
	subscription.Status = models.SubscriptionStatusAuthenticated

	// Set authenticated markers in metadata if not already present
	meta := map[string]interface{}{}
	_ = json.Unmarshal([]byte(subscription.Metadata), &meta)
	if auth, ok := meta["authenticated"].(bool); !ok || !auth {
		meta["authenticated"] = true
		if _, ok := meta["authenticated_at"]; !ok {
			meta["authenticated_at"] = time.Now().UTC().Format(time.RFC3339)
		}
		b, _ := json.Marshal(meta)
		subscription.Metadata = string(b)
	}
	if err := s.repo.Update(subscription); err != nil {
		return nil, err
	}

	response := subscription.ToResponse()
	return &response, nil
}

// HandleWebhook handles Razorpay webhook events
func (s *subscriptionService) HandleWebhook(payload []byte, signature string) error {
	// Parse webhook payload first to extract subscription info
	var event map[string]interface{}
	if err := json.Unmarshal(payload, &event); err != nil {
		return fmt.Errorf("failed to parse webhook payload: %w", err)
	}

	eventType := event["event"].(string)
	payloadData := event["payload"].(map[string]interface{})
	fmt.Printf("Webhook event received: %s\n", eventType)

	// Extract razorpay_subscription_id to fetch client configuration
	var razorpaySubID string
	if subWrap, ok := payloadData["subscription"].(map[string]interface{}); ok {
		if entity, ok := subWrap["entity"].(map[string]interface{}); ok {
			if id, ok := entity["id"].(string); ok {
				razorpaySubID = id
				status, _ := entity["status"].(string)
				fmt.Printf("Subscription entity: id=%s status=%s\n", id, status)
			}
		}
	}

	if razorpaySubID == "" {
		return errors.New("subscription ID not found in webhook payload")
	}

	// Fetch subscription to get razorpay_config_id
	subscription, err := s.repo.FindByRazorpaySubscriptionID(razorpaySubID)
	if err != nil {
		return fmt.Errorf("failed to find subscription: %w", err)
	}

	// Get razorpay config
	config, err := s.configRepo.FindByID(subscription.RazorpayConfigID)
	if err != nil {
		return fmt.Errorf("failed to find razorpay config: %w", err)
	}

	// Verify webhook signature using config's webhook secret
	if !s.verifyWebhookSignature(payload, signature, config.RazorpayWebhookSecret) {
		fmt.Printf("Webhook signature verification failed. signature=%s\n", signature)
		return errors.New("invalid webhook signature")
	}

	// Log payment info if present
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

// GetLatestSubscriptionByPhoneAndApp retrieves the latest subscription by phone number and app name
func (s *subscriptionService) GetLatestSubscriptionByPhoneAndApp(phone string, appName string) (*models.SubscriptionResponse, error) {
	subscription, err := s.repo.FindByPhoneAndAppName(phone, appName)
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

	// Get razorpay config
	config, err := s.configRepo.FindByID(subscription.RazorpayConfigID)
	if err != nil {
		return fmt.Errorf("failed to find razorpay config: %w", err)
	}

	// Get or create cached Razorpay client for this config's credentials
	razorpayClient := s.getRazorpayClient(config)

	// Cancel in Razorpay
	cancelData := map[string]interface{}{
		"cancel_at_cycle_end": 0,
	}
	_, err = razorpayClient.Subscription.Cancel(subscription.RazorpaySubscriptionID, cancelData, nil)
	if err != nil {
		return fmt.Errorf("failed to cancel razorpay subscription: %w", err)
	}

	// Update status in database
	subscription.Status = models.SubscriptionStatusCancelled
	return s.repo.Update(subscription)
}

// verifySignature verifies Razorpay signature
func (s *subscriptionService) verifySignature(message, signature, keySecret string) bool {
	mac := hmac.New(sha256.New, []byte(keySecret))
	mac.Write([]byte(message))
	expectedMAC := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(signature), []byte(expectedMAC))
}

// verifyWebhookSignature verifies webhook signature
func (s *subscriptionService) verifyWebhookSignature(payload []byte, signature, webhookSecret string) bool {
	mac := hmac.New(sha256.New, []byte(webhookSecret))
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

	// Ignore authentication event if subscription is already cancelled
	if subscription.Status == models.SubscriptionStatusCancelled {
		fmt.Printf("[handleSubscriptionAuthenticated] Ignoring authentication event for cancelled subscription: %s\n", razorpaySubID)
		return nil
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
	// Record authentication marker in metadata (idempotent)
	meta := map[string]interface{}{}
	_ = json.Unmarshal([]byte(subscription.Metadata), &meta)
	if auth, ok := meta["authenticated"].(bool); !ok || !auth {
		meta["authenticated"] = true
		if _, ok := meta["authenticated_at"]; !ok {
			meta["authenticated_at"] = time.Now().UTC().Format(time.RFC3339)
		}
		b, _ := json.Marshal(meta)
		subscription.Metadata = string(b)
	}

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

// CheckAuthenticationStatus checks if a phone number has ever had an authenticated subscription
func (s *subscriptionService) CheckAuthenticationStatus(phone string, appName string) (*models.CheckAuthenticationStatusResponse, error) {
	hasAuthenticated, err := s.repo.HasAuthenticatedSubscriptionByPhone(phone, appName)
	if err != nil {
		return nil, err
	}

	return &models.CheckAuthenticationStatusResponse{
		HasAuthenticated: hasAuthenticated,
		Phone:            phone,
	}, nil
}
