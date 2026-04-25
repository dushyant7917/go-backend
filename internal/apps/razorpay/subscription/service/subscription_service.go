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

	metaDatasetRepository "go-backend/internal/apps/metadataset/config/repository"
	clientModels "go-backend/internal/apps/razorpay/config/models"
	"go-backend/internal/apps/razorpay/config/repository"
	"go-backend/internal/apps/razorpay/subscription/models"
	razorpayRepository "go-backend/internal/apps/razorpay/subscription/repository"
	"go-backend/pkg/notification"
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
	GetSubscriptionStatus(phone string, appName string) (*models.SubscriptionStatusResponse, error)
	GetSubscriptionStats(appName string, days int, page int, pageSize int) (*models.SubscriptionStatsResponse, error)
}

// subscriptionService implements SubscriptionService interface
type subscriptionService struct {
	repo              razorpayRepository.SubscriptionRepository
	configRepo        repository.RazorpayConfigRepository
	metaDatasetRepo   metaDatasetRepository.MetaDatasetConfigRepository
	clientCache       map[string]*razorpay.Client     // Cache Razorpay clients by app_name:environment
	cacheMutex        sync.RWMutex                    // Protect concurrent access to cache
	metaDatasetClient *notification.MetaDatasetClient // Meta dataset client for conversion tracking
}

// NewSubscriptionService creates a new instance of SubscriptionService
func NewSubscriptionService(
	repo razorpayRepository.SubscriptionRepository,
	configRepo repository.RazorpayConfigRepository,
	metaDatasetRepo metaDatasetRepository.MetaDatasetConfigRepository,
) SubscriptionService {
	return &subscriptionService{
		repo:              repo,
		configRepo:        configRepo,
		metaDatasetRepo:   metaDatasetRepo,
		clientCache:       make(map[string]*razorpay.Client),
		metaDatasetClient: notification.NewMetaDatasetClient(),
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

	// Set authenticated_at in metadata if not already present
	meta := map[string]interface{}{}
	_ = json.Unmarshal([]byte(subscription.Metadata), &meta)
	if _, ok := meta["authenticated_at"]; !ok {
		meta["authenticated_at"] = time.Now().UTC().Format(time.RFC3339)
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
	fmt.Printf("[Webhook] ========== New Webhook Request ==========\n")
	fmt.Printf("[Webhook] Signature: %s\n", signature)
	fmt.Printf("[Webhook] Payload length: %d bytes\n", len(payload))

	// Parse webhook payload first to extract subscription info
	var event map[string]interface{}
	if err := json.Unmarshal(payload, &event); err != nil {
		fmt.Printf("[Webhook ERROR] Failed to parse JSON payload: %v\n", err)
		fmt.Printf("[Webhook ERROR] Raw payload: %s\n", string(payload))
		return fmt.Errorf("failed to parse webhook payload: %w", err)
	}

	eventType, ok := event["event"].(string)
	if !ok {
		fmt.Printf("[Webhook ERROR] Missing or invalid 'event' field in payload\n")
		return errors.New("invalid webhook payload: missing event type")
	}
	payloadData, ok := event["payload"].(map[string]interface{})
	if !ok {
		fmt.Printf("[Webhook ERROR] Missing or invalid 'payload' field\n")
		return errors.New("invalid webhook payload: missing payload data")
	}
	fmt.Printf("[Webhook] Event type: %s\n", eventType)

	// Extract razorpay_subscription_id to fetch client configuration
	var razorpaySubID string
	if subWrap, ok := payloadData["subscription"].(map[string]interface{}); ok {
		if entity, ok := subWrap["entity"].(map[string]interface{}); ok {
			if id, ok := entity["id"].(string); ok {
				razorpaySubID = id
				status, _ := entity["status"].(string)
				fmt.Printf("[Webhook] Subscription entity: id=%s status=%s\n", id, status)
			} else {
				fmt.Printf("[Webhook ERROR] Subscription ID field exists but is not a string\n")
			}
		} else {
			fmt.Printf("[Webhook ERROR] Subscription entity field missing or invalid\n")
		}
	} else {
		fmt.Printf("[Webhook ERROR] Subscription wrapper missing in payload\n")
	}

	if razorpaySubID == "" {
		fmt.Printf("[Webhook ERROR] Could not extract subscription ID from payload\n")
		fmt.Printf("[Webhook ERROR] Event type: %s\n", eventType)
		fmt.Printf("[Webhook ERROR] Payload data: %+v\n", payloadData)
		return errors.New("subscription ID not found in webhook payload")
	}

	// Fetch subscription to get razorpay_config_id
	fmt.Printf("[Webhook] Fetching subscription from database: %s\n", razorpaySubID)
	subscription, err := s.repo.FindByRazorpaySubscriptionID(razorpaySubID)
	if err != nil {
		fmt.Printf("[Webhook ERROR] Failed to find subscription in database: %v\n", err)
		fmt.Printf("[Webhook ERROR] Razorpay subscription ID: %s\n", razorpaySubID)
		return fmt.Errorf("failed to find subscription: %w", err)
	}
	fmt.Printf("[Webhook] Found subscription in database: ID=%s, Status=%s\n", subscription.ID, subscription.Status)

	// Get razorpay config
	fmt.Printf("[Webhook] Fetching Razorpay config: %s\n", subscription.RazorpayConfigID)
	config, err := s.configRepo.FindByID(subscription.RazorpayConfigID)
	if err != nil {
		fmt.Printf("[Webhook ERROR] Failed to find Razorpay config: %v\n", err)
		fmt.Printf("[Webhook ERROR] Config ID: %s\n", subscription.RazorpayConfigID)
		return fmt.Errorf("failed to find razorpay config: %w", err)
	}
	fmt.Printf("[Webhook] Using config: AppName=%s, Environment=%s\n", config.AppName, config.Environment)

	// Verify webhook signature using config's webhook secret
	fmt.Printf("[Webhook] Verifying webhook signature...\n")
	if !s.verifyWebhookSignature(payload, signature, config.RazorpayWebhookSecret) {
		fmt.Printf("[Webhook ERROR] Signature verification failed\n")
		fmt.Printf("[Webhook ERROR] Received signature: %s\n", signature)
		fmt.Printf("[Webhook ERROR] Payload (first 200 chars): %s\n", string(payload[:min(200, len(payload))]))
		return errors.New("invalid webhook signature")
	}
	fmt.Printf("[Webhook] Signature verified successfully\n")

	// Log payment info if present
	if payWrap, ok := payloadData["payment"].(map[string]interface{}); ok {
		if entity, ok := payWrap["entity"].(map[string]interface{}); ok {
			pid, _ := entity["id"].(string)
			pstatus, _ := entity["status"].(string)
			pmethod, _ := entity["method"].(string)
			fmt.Printf("[Webhook] Payment entity: id=%s status=%s method=%s\n", pid, pstatus, pmethod)
		}
	} else {
		fmt.Printf("[Webhook] No payment entity in webhook payload\n")
	}

	// Handle different event types
	fmt.Printf("[Webhook] Dispatching to event handler: %s\n", eventType)
	switch eventType {
	case "subscription.authenticated":
		err := s.handleSubscriptionAuthenticated(payloadData, eventType)
		if err != nil {
			fmt.Printf("[Webhook ERROR] handleSubscriptionAuthenticated failed: %v\n", err)
			return err
		}
		fmt.Printf("[Webhook] Successfully processed subscription.authenticated\n")
		return nil
	case "subscription.activated":
		err := s.handleSubscriptionActivated(payloadData, eventType)
		if err != nil {
			fmt.Printf("[Webhook ERROR] handleSubscriptionActivated failed: %v\n", err)
			return err
		}
		fmt.Printf("[Webhook] Successfully processed subscription.activated\n")
		return nil
	case "subscription.charged":
		err := s.handleSubscriptionCharged(payloadData, eventType)
		if err != nil {
			fmt.Printf("[Webhook ERROR] handleSubscriptionCharged failed: %v\n", err)
			return err
		}
		fmt.Printf("[Webhook] Successfully processed subscription.charged\n")
		return nil
	case "subscription.pending":
		err := s.handleSubscriptionPending(payloadData, eventType)
		if err != nil {
			fmt.Printf("[Webhook ERROR] handleSubscriptionPending failed: %v\n", err)
			return err
		}
		fmt.Printf("[Webhook] Successfully processed subscription.pending\n")
		return nil
	case "subscription.halted":
		err := s.handleSubscriptionHalted(payloadData, eventType)
		if err != nil {
			fmt.Printf("[Webhook ERROR] handleSubscriptionHalted failed: %v\n", err)
			return err
		}
		fmt.Printf("[Webhook] Successfully processed subscription.halted\n")
		return nil
	case "subscription.cancelled":
		err := s.handleSubscriptionCancelled(payloadData, eventType)
		if err != nil {
			fmt.Printf("[Webhook ERROR] handleSubscriptionCancelled failed: %v\n", err)
			return err
		}
		fmt.Printf("[Webhook] Successfully processed subscription.cancelled\n")
		return nil
	case "subscription.completed":
		err := s.handleSubscriptionCompleted(payloadData, eventType)
		if err != nil {
			fmt.Printf("[Webhook ERROR] handleSubscriptionCompleted failed: %v\n", err)
			return err
		}
		fmt.Printf("[Webhook] Successfully processed subscription.completed\n")
		return nil
	case "subscription.paused":
		err := s.handleSubscriptionPaused(payloadData, eventType)
		if err != nil {
			fmt.Printf("[Webhook ERROR] handleSubscriptionPaused failed: %v\n", err)
			return err
		}
		fmt.Printf("[Webhook] Successfully processed subscription.paused\n")
		return nil
	case "subscription.resumed":
		err := s.handleSubscriptionResumed(payloadData, eventType)
		if err != nil {
			fmt.Printf("[Webhook ERROR] handleSubscriptionResumed failed: %v\n", err)
			return err
		}
		fmt.Printf("[Webhook] Successfully processed subscription.resumed\n")
		return nil
	default:
		fmt.Printf("[Webhook] Unknown event type (ignoring): %s\n", eventType)
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

// appendEventToMetadata adds a timestamp key to subscription metadata for the given event
func (s *subscriptionService) appendEventToMetadata(subscription *models.Subscription, eventName string, payload map[string]interface{}) {
	meta := map[string]interface{}{}
	_ = json.Unmarshal([]byte(subscription.Metadata), &meta)

	// Map event names to timestamp keys
	eventKeyMap := map[string]string{
		"subscription.authenticated": "authenticated_at",
		"subscription.activated":     "activated_at",
		"subscription.charged":       "charged_at",
		"subscription.pending":       "pending_at",
		"subscription.halted":        "halted_at",
		"subscription.cancelled":     "cancelled_at",
		"subscription.completed":     "completed_at",
		"subscription.paused":        "paused_at",
		"subscription.resumed":       "resumed_at",
	}

	if key, ok := eventKeyMap[eventName]; ok {
		meta[key] = time.Now().UTC().Format(time.RFC3339)
	}

	b, _ := json.Marshal(meta)
	subscription.Metadata = string(b)
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// handleSubscriptionAuthenticated handles subscription.authenticated event
func (s *subscriptionService) handleSubscriptionAuthenticated(payload map[string]interface{}, eventName string) error {
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
	// Update status to authenticated from Razorpay webhook
	if rzpStatus, ok := subscriptionEntity["status"].(string); ok {
		subscription.Status = models.SubscriptionStatus(rzpStatus)
	}

	// Append event to metadata for audit trail
	s.appendEventToMetadata(subscription, eventName, payload)

	// Update subscription in database first
	if err := s.repo.Update(subscription); err != nil {
		return err
	}

	// Send Meta dataset SubscriptionAuthenticated event (non-blocking)
	go s.sendMetaDatasetAuthenticatedEvent(subscription, payload)

	return nil
}

// handleSubscriptionActivated handles subscription.activated event
func (s *subscriptionService) handleSubscriptionActivated(payload map[string]interface{}, eventName string) error {
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

	// Append event to metadata for audit trail
	s.appendEventToMetadata(subscription, eventName, payload)

	// Update subscription in database first
	if err := s.repo.Update(subscription); err != nil {
		return err
	}

	// Send Meta dataset SubscriptionActivated event (non-blocking)
	go s.sendMetaDatasetActivatedEvent(subscription, payload)

	return nil
}

// handleSubscriptionCharged handles subscription.charged event
func (s *subscriptionService) handleSubscriptionCharged(payload map[string]interface{}, eventName string) error {
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

	// Append event to metadata for audit trail
	s.appendEventToMetadata(subscription, eventName, payload)

	// Update subscription in database first
	if err := s.repo.Update(subscription); err != nil {
		return err
	}

	// Send Meta dataset Subscribe event (non-blocking)
	go s.sendMetaDatasetSubscribeEvent(subscription, payload)

	return nil
}

// handleSubscriptionPending handles subscription.pending event
func (s *subscriptionService) handleSubscriptionPending(payload map[string]interface{}, eventName string) error {
	subscriptionEntity := payload["subscription"].(map[string]interface{})["entity"].(map[string]interface{})
	razorpaySubID := subscriptionEntity["id"].(string)

	subscription, err := s.repo.FindByRazorpaySubscriptionID(razorpaySubID)
	if err != nil {
		return err
	}

	subscription.Status = models.SubscriptionStatusCreated

	// Append event to metadata for audit trail
	s.appendEventToMetadata(subscription, eventName, payload)

	return s.repo.Update(subscription)
}

// handleSubscriptionHalted handles subscription.halted event
func (s *subscriptionService) handleSubscriptionHalted(payload map[string]interface{}, eventName string) error {
	subscriptionEntity := payload["subscription"].(map[string]interface{})["entity"].(map[string]interface{})
	razorpaySubID := subscriptionEntity["id"].(string)

	subscription, err := s.repo.FindByRazorpaySubscriptionID(razorpaySubID)
	if err != nil {
		return err
	}

	subscription.Status = models.SubscriptionStatusHalted

	// Append event to metadata for audit trail
	s.appendEventToMetadata(subscription, eventName, payload)

	// Update subscription in database first
	if err := s.repo.Update(subscription); err != nil {
		return err
	}

	// Send Meta dataset SubscriptionHalted event (non-blocking)
	go s.sendMetaDatasetHaltedEvent(subscription, payload)

	return nil
}

// handleSubscriptionCancelled handles subscription.cancelled event
func (s *subscriptionService) handleSubscriptionCancelled(payload map[string]interface{}, eventName string) error {
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

	// Append event to metadata for audit trail
	s.appendEventToMetadata(subscription, eventName, payload)

	// Update subscription in database first
	if err := s.repo.Update(subscription); err != nil {
		return err
	}

	// Send Meta dataset SubscriptionCancelled event (non-blocking)
	go s.sendMetaDatasetCancelledEvent(subscription, payload)

	return nil
}

// handleSubscriptionCompleted handles subscription.completed event
func (s *subscriptionService) handleSubscriptionCompleted(payload map[string]interface{}, eventName string) error {
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

	// Append event to metadata for audit trail
	s.appendEventToMetadata(subscription, eventName, payload)

	return s.repo.Update(subscription)
}

// handleSubscriptionPaused handles subscription.paused event
func (s *subscriptionService) handleSubscriptionPaused(payload map[string]interface{}, eventName string) error {
	subscriptionEntity := payload["subscription"].(map[string]interface{})["entity"].(map[string]interface{})
	razorpaySubID := subscriptionEntity["id"].(string)

	subscription, err := s.repo.FindByRazorpaySubscriptionID(razorpaySubID)
	if err != nil {
		return err
	}

	subscription.Status = models.SubscriptionStatusPaused

	// Append event to metadata for audit trail
	s.appendEventToMetadata(subscription, eventName, payload)

	return s.repo.Update(subscription)
}

// handleSubscriptionResumed handles subscription.resumed event
func (s *subscriptionService) handleSubscriptionResumed(payload map[string]interface{}, eventName string) error {
	subscriptionEntity := payload["subscription"].(map[string]interface{})["entity"].(map[string]interface{})
	razorpaySubID := subscriptionEntity["id"].(string)

	subscription, err := s.repo.FindByRazorpaySubscriptionID(razorpaySubID)
	if err != nil {
		return err
	}

	subscription.Status = models.SubscriptionStatusActive

	// Append event to metadata for audit trail
	s.appendEventToMetadata(subscription, eventName, payload)

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

// GetSubscriptionStatus fetches both latest subscription and authentication status concurrently
func (s *subscriptionService) GetSubscriptionStatus(phone string, appName string) (*models.SubscriptionStatusResponse, error) {
	type latestSubResult struct {
		subscription *models.Subscription
		err          error
	}

	type authStatusResult struct {
		hasAuthenticated bool
		err              error
	}

	// Channels to receive results from goroutines
	latestSubChan := make(chan latestSubResult, 1)
	authStatusChan := make(chan authStatusResult, 1)

	// Fetch latest subscription in a goroutine
	go func() {
		subscription, err := s.repo.FindByPhoneAndAppName(phone, appName)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				latestSubChan <- latestSubResult{subscription: nil, err: nil}
				return
			}
			latestSubChan <- latestSubResult{subscription: nil, err: err}
			return
		}
		latestSubChan <- latestSubResult{subscription: subscription, err: nil}
	}()

	// Check authentication status in a goroutine
	go func() {
		hasAuthenticated, err := s.repo.HasAuthenticatedSubscriptionByPhone(phone, appName)
		if err != nil {
			authStatusChan <- authStatusResult{hasAuthenticated: false, err: err}
			return
		}
		authStatusChan <- authStatusResult{hasAuthenticated: hasAuthenticated, err: nil}
	}()

	// Wait for both goroutines to complete
	latestSubRes := <-latestSubChan
	authStatusRes := <-authStatusChan

	// Check for errors
	if latestSubRes.err != nil {
		return nil, fmt.Errorf("failed to fetch latest subscription: %w", latestSubRes.err)
	}

	if authStatusRes.err != nil {
		return nil, fmt.Errorf("failed to check authentication status: %w", authStatusRes.err)
	}

	// Determine if subscription is active based on status
	active := false
	if latestSubRes.subscription != nil {
		status := latestSubRes.subscription.Status
		active = status == models.SubscriptionStatusActive || status == models.SubscriptionStatusAuthenticated
	}

	// Build response
	return &models.SubscriptionStatusResponse{
		Active:           active,
		HasAuthenticated: authStatusRes.hasAuthenticated,
	}, nil
}

// sendMetaDatasetSubscribeEvent sends a Subscribe event to Meta via Conversions API (dataset_id)
// This function is called asynchronously and should not block the webhook handler
func (s *subscriptionService) sendMetaDatasetSubscribeEvent(subscription *models.Subscription, payload map[string]interface{}) {
	fmt.Printf("[Meta Dataset] Processing Subscribe event for subscription: %s\n", subscription.ID)

	// Get Meta dataset config based on app_name and environment
	env := utils.GetEnv("GO_ENV", "local")
	metaConfig, err := s.metaDatasetRepo.FindByAppNameAndEnv(subscription.AppName, env)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fmt.Printf("[Meta Dataset] No meta dataset config found for app: %s, environment: %s. Skipping event.\n", subscription.AppName, env)
		} else {
			fmt.Printf("[Meta Dataset ERROR] Failed to get meta dataset config: %v\n", err)
		}
		return
	}

	if !metaConfig.IsActive {
		fmt.Printf("[Meta Dataset] Meta dataset config is inactive for app: %s. Skipping event.\n", subscription.AppName)
		return
	}

	if metaConfig.DatasetID == "" {
		fmt.Printf("[Meta Dataset ERROR] dataset_id is empty for app: %s, environment: %s\n", subscription.AppName, env)
		return
	}

	// Extract payment information from webhook payload
	var paymentID string
	if payWrap, ok := payload["payment"].(map[string]interface{}); ok {
		if entity, ok := payWrap["entity"].(map[string]interface{}); ok {
			if pid, ok := entity["id"].(string); ok {
				paymentID = pid
			}
		}
	}

	// Convert amount from paise to rupees (Meta expects decimal currency value)
	value := float64(subscription.Amount) / 100.0

	// Prepare event data
	eventData := notification.MetaEventData{
		DatasetID:    metaConfig.DatasetID,
		AccessToken:  metaConfig.AccessToken,
		EventName:    "SubscriptionCharged",
		EventTime:    time.Now().Unix(),
		ActionSource: "other",
		UserData: notification.UserData{
			Phone:      notification.HashPhone(subscription.Phone),
			ExternalID: subscription.UserID.String(),
		},
		CustomData: notification.CustomData{
			Currency:    subscription.Currency,
			Value:       value,
			ContentName: fmt.Sprintf("%s Subscription", subscription.AppName),
			ContentType: "product",
			Contents: []notification.Content{
				{
					ID:       subscription.RazorpayPlanID,
					Quantity: 1,
					Price:    value,
				},
			},
		},
	}

	// Add event ID for deduplication if payment ID is available
	if paymentID != "" {
		eventData.EventID = notification.GenerateEventID(paymentID, eventData.EventTime)
	}

	// Send event to Meta Conversions API
	if err := s.metaDatasetClient.SendEvent(eventData); err != nil {
		fmt.Printf("[Meta Dataset ERROR] Failed to send Subscribe event: %v\n", err)
		return
	}

	fmt.Printf("[Meta Dataset] Successfully sent Subscribe event for subscription %s (%.2f %s) to dataset_id %s\n",
		subscription.ID, value, subscription.Currency, metaConfig.DatasetID)
}

// sendMetaDatasetAuthenticatedEvent sends SubscriptionAuthenticated event to Meta Conversions API
func (s *subscriptionService) sendMetaDatasetAuthenticatedEvent(subscription *models.Subscription, payload map[string]interface{}) {
	fmt.Printf("[Meta Dataset] Processing SubscriptionAuthenticated event for subscription: %s\n", subscription.ID)

	// Get Meta dataset config based on app_name and environment
	env := utils.GetEnv("GO_ENV", "local")
	metaConfig, err := s.metaDatasetRepo.FindByAppNameAndEnv(subscription.AppName, env)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fmt.Printf("[Meta Dataset] No meta dataset config found for app: %s, environment: %s. Skipping event.\n", subscription.AppName, env)
		} else {
			fmt.Printf("[Meta Dataset ERROR] Failed to get meta dataset config: %v\n", err)
		}
		return
	}

	if !metaConfig.IsActive {
		fmt.Printf("[Meta Dataset] Meta dataset config is inactive for app: %s. Skipping event.\n", subscription.AppName)
		return
	}

	if metaConfig.DatasetID == "" {
		fmt.Printf("[Meta Dataset ERROR] dataset_id is empty for app: %s, environment: %s\n", subscription.AppName, env)
		return
	}

	// Convert amount from paise to rupees (Meta expects decimal currency value)
	value := float64(subscription.Amount) / 100.0

	// Prepare event data
	eventData := notification.MetaEventData{
		DatasetID:    metaConfig.DatasetID,
		AccessToken:  metaConfig.AccessToken,
		EventName:    "SubscriptionAuthenticated",
		EventTime:    time.Now().Unix(),
		ActionSource: "other",
		UserData: notification.UserData{
			Phone:      notification.HashPhone(subscription.Phone),
			ExternalID: subscription.UserID.String(),
		},
		CustomData: notification.CustomData{
			Currency:    subscription.Currency,
			Value:       value,
			ContentName: fmt.Sprintf("%s Subscription", subscription.AppName),
			ContentType: "product",
			Contents: []notification.Content{
				{
					ID:       subscription.RazorpayPlanID,
					Quantity: 1,
					Price:    value,
				},
			},
		},
	}

	// Add event ID for deduplication using subscription ID
	eventData.EventID = notification.GenerateEventID(subscription.RazorpaySubscriptionID, eventData.EventTime)

	// Send event to Meta Conversions API
	if err := s.metaDatasetClient.SendEvent(eventData); err != nil {
		fmt.Printf("[Meta Dataset ERROR] Failed to send SubscriptionAuthenticated event: %v\n", err)
		return
	}

	fmt.Printf("[Meta Dataset] Successfully sent SubscriptionAuthenticated event for subscription %s (%.2f %s) to dataset_id %s\n",
		subscription.ID, value, subscription.Currency, metaConfig.DatasetID)
}

// sendMetaDatasetActivatedEvent sends SubscriptionActivated event to Meta Conversions API
func (s *subscriptionService) sendMetaDatasetActivatedEvent(subscription *models.Subscription, payload map[string]interface{}) {
	fmt.Printf("[Meta Dataset] Processing SubscriptionActivated event for subscription: %s\n", subscription.ID)

	// Get Meta dataset config based on app_name and environment
	env := utils.GetEnv("GO_ENV", "local")
	metaConfig, err := s.metaDatasetRepo.FindByAppNameAndEnv(subscription.AppName, env)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fmt.Printf("[Meta Dataset] No meta dataset config found for app: %s, environment: %s. Skipping event.\n", subscription.AppName, env)
		} else {
			fmt.Printf("[Meta Dataset ERROR] Failed to get meta dataset config: %v\n", err)
		}
		return
	}

	if !metaConfig.IsActive {
		fmt.Printf("[Meta Dataset] Meta dataset config is inactive for app: %s. Skipping event.\n", subscription.AppName)
		return
	}

	if metaConfig.DatasetID == "" {
		fmt.Printf("[Meta Dataset ERROR] dataset_id is empty for app: %s, environment: %s\n", subscription.AppName, env)
		return
	}

	// Convert amount from paise to rupees (Meta expects decimal currency value)
	value := float64(subscription.Amount) / 100.0

	// Prepare event data
	eventData := notification.MetaEventData{
		DatasetID:    metaConfig.DatasetID,
		AccessToken:  metaConfig.AccessToken,
		EventName:    "SubscriptionActivated",
		EventTime:    time.Now().Unix(),
		ActionSource: "other",
		UserData: notification.UserData{
			Phone:      notification.HashPhone(subscription.Phone),
			ExternalID: subscription.UserID.String(),
		},
		CustomData: notification.CustomData{
			Currency:    subscription.Currency,
			Value:       value,
			ContentName: fmt.Sprintf("%s Subscription", subscription.AppName),
			ContentType: "product",
			Contents: []notification.Content{
				{
					ID:       subscription.RazorpayPlanID,
					Quantity: 1,
					Price:    value,
				},
			},
		},
	}

	// Add event ID for deduplication using subscription ID
	eventData.EventID = notification.GenerateEventID(subscription.RazorpaySubscriptionID, eventData.EventTime)

	// Send event to Meta Conversions API
	if err := s.metaDatasetClient.SendEvent(eventData); err != nil {
		fmt.Printf("[Meta Dataset ERROR] Failed to send SubscriptionActivated event: %v\n", err)
		return
	}

	fmt.Printf("[Meta Dataset] Successfully sent SubscriptionActivated event for subscription %s (%.2f %s) to dataset_id %s\n",
		subscription.ID, value, subscription.Currency, metaConfig.DatasetID)
}

// sendMetaDatasetCancelledEvent sends SubscriptionCancelled event to Meta Conversions API
func (s *subscriptionService) sendMetaDatasetCancelledEvent(subscription *models.Subscription, payload map[string]interface{}) {
	fmt.Printf("[Meta Dataset] Processing SubscriptionCancelled event for subscription: %s\n", subscription.ID)

	// Get Meta dataset config based on app_name and environment
	env := utils.GetEnv("GO_ENV", "local")
	metaConfig, err := s.metaDatasetRepo.FindByAppNameAndEnv(subscription.AppName, env)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fmt.Printf("[Meta Dataset] No meta dataset config found for app: %s, environment: %s. Skipping event.\n", subscription.AppName, env)
		} else {
			fmt.Printf("[Meta Dataset ERROR] Failed to get meta dataset config: %v\n", err)
		}
		return
	}

	if !metaConfig.IsActive {
		fmt.Printf("[Meta Dataset] Meta dataset config is inactive for app: %s. Skipping event.\n", subscription.AppName)
		return
	}

	if metaConfig.DatasetID == "" {
		fmt.Printf("[Meta Dataset ERROR] dataset_id is empty for app: %s, environment: %s\n", subscription.AppName, env)
		return
	}

	// Convert amount from paise to rupees (Meta expects decimal currency value)
	value := float64(subscription.Amount) / 100.0

	// Prepare event data
	eventData := notification.MetaEventData{
		DatasetID:    metaConfig.DatasetID,
		AccessToken:  metaConfig.AccessToken,
		EventName:    "SubscriptionCancelled",
		EventTime:    time.Now().Unix(),
		ActionSource: "other",
		UserData: notification.UserData{
			Phone:      notification.HashPhone(subscription.Phone),
			ExternalID: subscription.UserID.String(),
		},
		CustomData: notification.CustomData{
			Currency:    subscription.Currency,
			Value:       value,
			ContentName: fmt.Sprintf("%s Subscription", subscription.AppName),
			ContentType: "product",
			Contents: []notification.Content{
				{
					ID:       subscription.RazorpayPlanID,
					Quantity: 1,
					Price:    value,
				},
			},
		},
	}

	// Add event ID for deduplication using subscription ID
	eventData.EventID = notification.GenerateEventID(subscription.RazorpaySubscriptionID, eventData.EventTime)

	// Send event to Meta Conversions API
	if err := s.metaDatasetClient.SendEvent(eventData); err != nil {
		fmt.Printf("[Meta Dataset ERROR] Failed to send SubscriptionCancelled event: %v\n", err)
		return
	}

	fmt.Printf("[Meta Dataset] Successfully sent SubscriptionCancelled event for subscription %s (%.2f %s) to dataset_id %s\n",
		subscription.ID, value, subscription.Currency, metaConfig.DatasetID)
}

// sendMetaDatasetHaltedEvent sends SubscriptionHalted event to Meta Conversions API
func (s *subscriptionService) sendMetaDatasetHaltedEvent(subscription *models.Subscription, payload map[string]interface{}) {
	fmt.Printf("[Meta Dataset] Processing SubscriptionHalted event for subscription: %s\n", subscription.ID)

	// Get Meta dataset config based on app_name and environment
	env := utils.GetEnv("GO_ENV", "local")
	metaConfig, err := s.metaDatasetRepo.FindByAppNameAndEnv(subscription.AppName, env)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fmt.Printf("[Meta Dataset] No meta dataset config found for app: %s, environment: %s. Skipping event.\n", subscription.AppName, env)
		} else {
			fmt.Printf("[Meta Dataset ERROR] Failed to get meta dataset config: %v\n", err)
		}
		return
	}

	if !metaConfig.IsActive {
		fmt.Printf("[Meta Dataset] Meta dataset config is inactive for app: %s. Skipping event.\n", subscription.AppName)
		return
	}

	if metaConfig.DatasetID == "" {
		fmt.Printf("[Meta Dataset ERROR] dataset_id is empty for app: %s, environment: %s\n", subscription.AppName, env)
		return
	}

	// Convert amount from paise to rupees (Meta expects decimal currency value)
	value := float64(subscription.Amount) / 100.0

	// Prepare event data
	eventData := notification.MetaEventData{
		DatasetID:    metaConfig.DatasetID,
		AccessToken:  metaConfig.AccessToken,
		EventName:    "SubscriptionHalted",
		EventTime:    time.Now().Unix(),
		ActionSource: "other",
		UserData: notification.UserData{
			Phone:      notification.HashPhone(subscription.Phone),
			ExternalID: subscription.UserID.String(),
		},
		CustomData: notification.CustomData{
			Currency:    subscription.Currency,
			Value:       value,
			ContentName: fmt.Sprintf("%s Subscription", subscription.AppName),
			ContentType: "product",
			Contents: []notification.Content{
				{
					ID:       subscription.RazorpayPlanID,
					Quantity: 1,
					Price:    value,
				},
			},
		},
	}

	// Add event ID for deduplication using subscription ID
	eventData.EventID = notification.GenerateEventID(subscription.RazorpaySubscriptionID, eventData.EventTime)

	// Send event to Meta Conversions API
	if err := s.metaDatasetClient.SendEvent(eventData); err != nil {
		fmt.Printf("[Meta Dataset ERROR] Failed to send SubscriptionHalted event: %v\n", err)
		return
	}

	fmt.Printf("[Meta Dataset] Successfully sent SubscriptionHalted event for subscription %s (%.2f %s) to dataset_id %s\n",
		subscription.ID, value, subscription.Currency, metaConfig.DatasetID)
}

// GetSubscriptionStats returns subscription statistics for the last N days with pagination
func (s *subscriptionService) GetSubscriptionStats(appName string, days int, page int, pageSize int) (*models.SubscriptionStatsResponse, error) {
	// Validate input
	if days <= 0 {
		return nil, errors.New("days must be greater than 0")
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 30 // Default page size
	}

	// Fetch all subscriptions for the last N days
	subscriptions, err := s.repo.GetStatsByAppName(appName, days)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch subscriptions: %w", err)
	}

	// Create a map to group subscriptions by date
	dateStatsMap := make(map[string]*models.DailySubscriptionStats)

	// Track unique phone numbers per date for created count
	datePhoneSet := make(map[string]map[string]bool)

	// Initialize stats for each day in the range (in descending order)
	now := time.Now().Truncate(24 * time.Hour)
	for i := 0; i < days; i++ {
		date := now.AddDate(0, 0, -i)
		dateStr := date.Format("2006-01-02")
		dateStatsMap[dateStr] = &models.DailySubscriptionStats{
			Date:           dateStr,
			CreatedCount:   0,
			AuthCount:      0,
			CancelledCount: 0,
			ActiveCount:    0,
			Revenue:        0.0,
		}
		datePhoneSet[dateStr] = make(map[string]bool)
	}

	// Process each subscription and calculate statistics
	for _, sub := range subscriptions {
		// Count unique phone numbers by created_at date
		createdDateStr := sub.CreatedAt.Truncate(24 * time.Hour).Format("2006-01-02")
		if stats, exists := dateStatsMap[createdDateStr]; exists {
			phoneKey := strings.TrimSpace(sub.Phone)
			if _, counted := datePhoneSet[createdDateStr][phoneKey]; !counted {
				stats.CreatedCount++
				datePhoneSet[createdDateStr][phoneKey] = true
			}

			// Count by current status (per subscription)
			switch sub.Status {
			case models.SubscriptionStatusAuthenticated:
				stats.AuthCount++
			case models.SubscriptionStatusActive:
				stats.ActiveCount++
			case models.SubscriptionStatusCancelled:
				stats.CancelledCount++
			}
		}

		// Calculate revenue based on updated_at date when status became active
		if sub.Status == models.SubscriptionStatusActive && sub.Amount > 0 {
			updatedDateStr := sub.UpdatedAt.Truncate(24 * time.Hour).Format("2006-01-02")
			if updatedStats, exists := dateStatsMap[updatedDateStr]; exists {
				// Add amount (1 × amount) to revenue for the day it became active
				revenue := float64(sub.Amount) / 100.0
				updatedStats.Revenue += revenue
			}
		}
	}

	// Convert map to sorted slice (descending by date)
	var allStats []models.DailySubscriptionStats
	totalRevenue := 0.0
	totalCreated := 0
	totalAuthenticated := 0
	totalCancelled := 0
	totalActive := 0

	for i := 0; i < days; i++ {
		date := now.AddDate(0, 0, -i)
		dateStr := date.Format("2006-01-02")
		if stats, exists := dateStatsMap[dateStr]; exists {
			allStats = append(allStats, *stats)
			totalRevenue += stats.Revenue
			totalCreated += stats.CreatedCount
			totalAuthenticated += stats.AuthCount
			totalCancelled += stats.CancelledCount
			totalActive += stats.ActiveCount
		}
	}

	// Calculate pagination
	totalDays := len(allStats)
	totalPages := (totalDays + pageSize - 1) / pageSize
	if totalPages == 0 {
		totalPages = 1
	}

	// Paginate the results
	start := (page - 1) * pageSize
	end := start + pageSize
	if start > len(allStats) {
		start = len(allStats)
	}
	if end > len(allStats) {
		end = len(allStats)
	}

	paginatedStats := allStats[start:end]

	return &models.SubscriptionStatsResponse{
		Stats:              paginatedStats,
		TotalRevenue:       totalRevenue,
		TotalCreated:       totalCreated,
		TotalAuthenticated: totalAuthenticated,
		TotalCancelled:     totalCancelled,
		TotalActive:        totalActive,
		TotalPages:         totalPages,
		TotalDays:          totalDays,
		CurrentPage:        page,
		PageSize:           pageSize,
	}, nil
}
