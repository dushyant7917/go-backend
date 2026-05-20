package service

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	metaEventSvc "go-backend/internal/apps/meta_event/service"
	metaDatasetModels "go-backend/internal/apps/metadataset/config/models"
	metaDatasetRepository "go-backend/internal/apps/metadataset/config/repository"
	posthogModels "go-backend/internal/apps/posthog/config/models"
	"go-backend/internal/apps/posthog/config/repository"
	clientModels "go-backend/internal/apps/razorpay/config/models"
	razorpayConfigRepository "go-backend/internal/apps/razorpay/config/repository"
	"go-backend/internal/apps/razorpay/recurring_payment/models"
	repo "go-backend/internal/apps/razorpay/recurring_payment/repository"
	userModels "go-backend/internal/apps/user/models"
	userRepo "go-backend/internal/apps/user/repository"
	"go-backend/internal/common/constants"

	"go-backend/pkg/analytics"
	"go-backend/pkg/notification"
	"go-backend/pkg/utils"

	"github.com/getsentry/sentry-go"

	"github.com/google/uuid"
	razorpay "github.com/razorpay/razorpay-go"
	"gorm.io/gorm"
)

// RecurringPaymentService defines the interface for recurring payment business logic
type RecurringPaymentService interface {
	// API endpoints
	CreateAuthorizationOrder(req models.CreateAuthorizationOrderRequest) (*models.AuthorizationOrderResponse, error)
	CreateRegistrationLink(req models.CreateRegistrationLinkRequest) (*models.RegistrationLinkResponse, error)
	VerifyAuthorizationPayment(req models.VerifyAuthorizationPaymentRequest) (*models.RecurringPaymentResponse, error)
	GetRecurringPaymentByID(id uuid.UUID) (*models.RecurringPaymentResponse, error)
	GetRecurringPaymentStatus(userID uuid.UUID, appName string) (*models.RecurringPaymentStatusResponse, error)
	HandleWebhook(payload []byte, signature string) error

	// Cron job methods
	ProcessNewBillingCycles() error  // Cron A: Create new billing cycles and send notifications
	RetryFailedBillingCycles() error // Cron B: Retry failed billing cycles
	ChargePendingPayments() error    // Cron C: Charge pending payment attempts via Razorpay SDK
	ReconcilePayments() error        // Cron D: Sync state from Razorpay for missed webhooks
}

// recurringPaymentService implements RecurringPaymentService interface
type recurringPaymentService struct {
	repo              repo.RecurringPaymentRepository
	configRepo        razorpayConfigRepository.RazorpayConfigRepository
	userRepo          userRepo.UserRepository
	metaDatasetRepo   metaDatasetRepository.MetaDatasetConfigRepository
	posthogConfigRepo repository.PostHogConfigRepository
	metaEventService  metaEventSvc.MetaEventService
	clientCache       map[string]*razorpay.Client
	configCache       map[string]*clientModels.RazorpayConfig
	cacheMutex        sync.RWMutex
	metaDatasetClient *notification.MetaDatasetClient
	posthogClient     *analytics.PostHogClient
}

// NewRecurringPaymentService creates a new instance of RecurringPaymentService
func NewRecurringPaymentService(
	repo repo.RecurringPaymentRepository,
	configRepo razorpayConfigRepository.RazorpayConfigRepository,
	userRepo userRepo.UserRepository,
	metaDatasetRepo metaDatasetRepository.MetaDatasetConfigRepository,
	posthogConfigRepo repository.PostHogConfigRepository,
	metaEventService metaEventSvc.MetaEventService,
) RecurringPaymentService {
	return &recurringPaymentService{
		repo:              repo,
		configRepo:        configRepo,
		userRepo:          userRepo,
		metaDatasetRepo:   metaDatasetRepo,
		posthogConfigRepo: posthogConfigRepo,
		metaEventService:  metaEventService,
		clientCache:       make(map[string]*razorpay.Client),
		configCache:       make(map[string]*clientModels.RazorpayConfig),
		metaDatasetClient: notification.NewMetaDatasetClient(),
		posthogClient:     analytics.NewPostHogClient(),
	}
}

// ==================== Cache Helpers ====================

// getRazorpayClient returns a cached Razorpay client or creates a new one
func (s *recurringPaymentService) getRazorpayClient(config *clientModels.RazorpayConfig) *razorpay.Client {
	cacheKey := config.AppName + ":" + config.Environment

	s.cacheMutex.RLock()
	cachedClient, exists := s.clientCache[cacheKey]
	s.cacheMutex.RUnlock()

	if exists {
		return cachedClient
	}

	s.cacheMutex.Lock()
	defer s.cacheMutex.Unlock()

	if cachedClient, exists := s.clientCache[cacheKey]; exists {
		return cachedClient
	}

	newClient := razorpay.NewClient(config.RazorpayKeyID, config.RazorpayKeySecret)
	s.clientCache[cacheKey] = newClient

	fmt.Printf("[getRazorpayClient] Created and cached new Razorpay client for app: %s\n", config.AppName)
	return newClient
}

// getConfig returns a cached Razorpay config or fetches from database
func (s *recurringPaymentService) getConfig(appName string) (*clientModels.RazorpayConfig, error) {
	env := utils.GetEnv("GO_ENV", "local")
	cacheKey := appName + ":" + env

	s.cacheMutex.RLock()
	cachedConfig, exists := s.configCache[cacheKey]
	s.cacheMutex.RUnlock()

	if exists {
		return cachedConfig, nil
	}

	s.cacheMutex.Lock()
	defer s.cacheMutex.Unlock()

	if cachedConfig, exists := s.configCache[cacheKey]; exists {
		return cachedConfig, nil
	}

	config, err := s.configRepo.FindByAppNameAndEnv(appName, env)
	if err != nil {
		return nil, err
	}

	s.configCache[cacheKey] = config
	fmt.Printf("[getConfig] Fetched and cached config for app: %s\n", appName)
	return config, nil
}

// ==================== Generic Helpers ====================

const maxConcurrency = 15

// processInParallel runs a processor function in parallel over items with bounded concurrency
func processInParallel[T any](items []T, processor func(T) error, logPrefix string) {
	sem := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup

	for _, item := range items {
		wg.Add(1)
		sem <- struct{}{} // acquire semaphore

		go func(item T) {
			defer wg.Done()
			defer func() { <-sem }() // release semaphore
			defer func() {
				if r := recover(); r != nil {
					fmt.Printf("[%s] PANIC: %v\n", logPrefix, r)
				}
			}()

			if err := processor(item); err != nil {
				fmt.Printf("[%s] ERROR: %v\n", logPrefix, err)
			}
		}(item)
	}
	wg.Wait()
}

// scheduleRetry calculates and sets the next retry time for a billing cycle
// Retry pattern: T+3, T+6, T+9... days from start
func scheduleRetry(billingCycle *models.BillingCycle) {
	attemptNum := billingCycle.ChargeAttempts
	retryDays := attemptNum * 3
	nextRetry := billingCycle.StartAt.AddDate(0, 0, retryDays)
	billingCycle.NextAttemptAt = &nextRetry
}

// handlePaymentCaptured updates records for a captured payment, schedules next charge, and emits Meta event
func (s *recurringPaymentService) handlePaymentCaptured(
	paymentAttempt *models.PaymentAttempt,
	billingCycle *models.BillingCycle,
	recurringPayment *models.RecurringPayment,
) {
	// Idempotency: skip if already captured
	if paymentAttempt.Status == models.PaymentAttemptStatusCaptured {
		return
	}

	now := time.Now().UTC()

	paymentAttempt.Status = models.PaymentAttemptStatusCaptured
	billingCycle.Status = models.BillingCycleStatusPaid

	// Use scheduled time if available, otherwise now
	if billingCycle.NextAttemptAt != nil {
		recurringPayment.LastChargedAt = billingCycle.NextAttemptAt
	} else {
		recurringPayment.LastChargedAt = &now
	}
	billingCycle.NextAttemptAt = nil // No more attempts needed - cycle is paid

	// Schedule next charge
	nextCharge := s.calculateNextChargeDate(*recurringPayment)
	recurringPayment.NextChargeAt = &nextCharge

	// Emit Meta event for Purchase
	go s.registerPurchaseMetaEvent(recurringPayment, paymentAttempt, billingCycle)

	// Emit PostHog event for payment captured
	user, err := s.userRepo.FindByID(recurringPayment.UserID)
	if err == nil {
		go s.sendPostHogRecurringPaymentCapturedEvent(recurringPayment, paymentAttempt, user)
	}
}

// handlePaymentFailed updates records for a failed payment
func (s *recurringPaymentService) handlePaymentFailed(
	paymentAttempt *models.PaymentAttempt,
	billingCycle *models.BillingCycle,
	recurringPayment *models.RecurringPayment,
	paymentEntity map[string]interface{},
) {
	// Idempotency: skip if already in a terminal state
	if paymentAttempt.Status == models.PaymentAttemptStatusFailed {
		return
	}

	paymentAttempt.Status = models.PaymentAttemptStatusFailed

	// Extract error details
	if errorCode, ok := paymentEntity["error_code"].(string); ok {
		paymentAttempt.ErrorCode = errorCode
	}
	if errorDesc, ok := paymentEntity["error_description"].(string); ok {
		paymentAttempt.ErrorDescription = errorDesc
	}

	// Emit PostHog event for payment failed
	user, err := s.userRepo.FindByID(recurringPayment.UserID)
	if err == nil {
		go s.sendPostHogRecurringPaymentFailedEvent(recurringPayment, paymentAttempt, user)
	}

	// Skip retry scheduling for authorization payment (cycle 0)
	if billingCycle.CycleNumber == 0 {
		return
	}

	// Mark expired if max attempts reached (9)
	if billingCycle.ChargeAttempts >= 9 {
		billingCycle.Status = models.BillingCycleStatusFailed
		recurringPayment.Status = models.RecurringPaymentStatusExpired
		return
	}

	// Schedule next retry
	scheduleRetry(billingCycle)
}

// razorpayErrorInfo contains extracted error information from Razorpay error response
type razorpayErrorInfo struct {
	Code        string
	Description string
}

// extractRazorpayError extracts code and description from Razorpay error response
// Razorpay errors have structure: {"error": {"code": "BAD_REQUEST_ERROR", "description": "...", ...}}
func extractRazorpayError(err error) razorpayErrorInfo {
	if err == nil {
		return razorpayErrorInfo{}
	}

	errMsg := err.Error()
	info := razorpayErrorInfo{}

	// Try to parse as JSON
	var errorResponse map[string]interface{}
	if jsonErr := json.Unmarshal([]byte(errMsg), &errorResponse); jsonErr != nil {
		// Not a JSON error, use raw error message as description
		info.Description = errMsg
		return info
	}

	// Extract nested error object
	errorObj, ok := errorResponse["error"].(map[string]interface{})
	if !ok {
		// No nested error object, use raw error message as description
		info.Description = errMsg
		return info
	}

	// Extract code
	if code, ok := errorObj["code"].(string); ok {
		info.Code = code
	}

	// Extract description
	if desc, ok := errorObj["description"].(string); ok {
		info.Description = desc
	} else if info.Code == "" {
		// No description and no code, use raw error message
		info.Description = errMsg
	}

	return info
}

// extractRazorpayErrorCode extracts only the error code from Razorpay error response
func extractRazorpayErrorCode(err error) *string {
	info := extractRazorpayError(err)
	if info.Code == "" {
		return nil
	}
	return &info.Code
}

// extractRazorpayErrorCodeFromInfo extracts error code pointer from errorInfo
func extractRazorpayErrorCodeFromInfo(errorInfo razorpayErrorInfo) *string {
	if errorInfo.Code == "" {
		return nil
	}
	return &errorInfo.Code
}

// isTokenError checks if an error code indicates an invalid token
func isTokenError(errorCode string) bool {
	return strings.ToLower(errorCode) == "invalid_token"
}

// isMandateError checks if an error code indicates a mandate failure
func isMandateError(errorCode string) bool {
	lowerCode := strings.ToLower(errorCode)
	return lowerCode == "mandate_cancelled" ||
		lowerCode == "mandate_expired" ||
		lowerCode == "mandate_not_active"
}

// calculateAttemptAmount calculates the amount to charge based on attempt number
// First 3 attempts: maxAmount (full amount)
// 4th attempt onwards: maxAmount - 5 rupees (500 paise) per attempt above 3
func calculateAttemptAmount(maxAmount int64, attemptNumber int) int64 {
	if attemptNumber <= 3 {
		return maxAmount
	}
	// Decrease by 5 rupees (500 paise) for each attempt above 3
	discount := int64((attemptNumber - 3) * 500)
	if discount >= maxAmount {
		return 100 // Minimum 1 rupee (100 paise)
	}
	return maxAmount - discount
}

// saveRecordsInTransaction saves all records in a single transaction
func (s *recurringPaymentService) saveRecordsInTransaction(
	paymentAttempt *models.PaymentAttempt,
	billingCycle *models.BillingCycle,
	recurringPayment *models.RecurringPayment,
) error {
	tx := s.repo.BeginTransaction()

	if err := s.repo.UpdatePaymentAttempt(tx, paymentAttempt); err != nil {
		s.repo.RollbackTransaction(tx)
		return fmt.Errorf("failed to update payment attempt: %w", err)
	}
	if err := s.repo.UpdateBillingCycle(tx, billingCycle); err != nil {
		s.repo.RollbackTransaction(tx)
		return fmt.Errorf("failed to update billing cycle: %w", err)
	}
	if err := s.repo.UpdateRecurringPayment(tx, recurringPayment); err != nil {
		s.repo.RollbackTransaction(tx)
		return fmt.Errorf("failed to update recurring payment: %w", err)
	}

	if err := s.repo.CommitTransaction(tx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// ==================== API Endpoints ====================

// CreateAuthorizationOrder creates a customer, order, and initializes recurring payment records
func (s *recurringPaymentService) CreateAuthorizationOrder(req models.CreateAuthorizationOrderRequest) (*models.AuthorizationOrderResponse, error) {
	// Get user details for email and phone
	user, err := s.userRepo.FindByID(req.UserID)
	if err != nil {
		return nil, fmt.Errorf("failed to find user: %w", err)
	}

	config, err := s.getConfig(req.AppName)
	if err != nil {
		return nil, fmt.Errorf("failed to find razorpay config: %w", err)
	}

	if !config.IsActive {
		return nil, errors.New("razorpay config is not active")
	}

	// Validate StartAt is at least 45 hours in the future
	// ProcessNewBillingCycles cron looks for next_charge_at in 25-50 hour window
	// If StartAt is too close, the cron will miss creating the first billing cycle
	minStartAt := time.Now().UTC().Add(45 * time.Hour)
	if req.StartAt.Before(minStartAt) {
		return nil, fmt.Errorf("start_at must be at least 45 hours in the future")
	}

	// Validate authorization_amount <= max_amount (Razorpay requirement for UPI recurring)
	if req.AuthorizationAmount > req.MaxAmount {
		return nil, fmt.Errorf("authorization_amount (%d) cannot be greater than max_amount (%d)", req.AuthorizationAmount, req.MaxAmount)
	}

	razorpayClient := s.getRazorpayClient(config)

	// Create customer in Razorpay
	customerID, err := s.createRazorpayCustomer(razorpayClient, user)
	if err != nil {
		return nil, err
	}

	// Create authorization order in Razorpay
	orderID, err := s.createAuthorizationRazorpayOrder(razorpayClient, customerID, req)
	if err != nil {
		return nil, err
	}

	// Create database records
	recurringPayment, billingCycle, paymentAttempt, err := s.createAuthorizationDBRecords(req, customerID, orderID)
	if err != nil {
		return nil, err
	}

	fmt.Printf("[CreateAuthorizationOrder] Created RecurringPayment: %s, BillingCycle: %s, PaymentAttempt: %s\n",
		recurringPayment.ID, billingCycle.ID, paymentAttempt.ID)

	return &models.AuthorizationOrderResponse{
		RecurringPaymentID: recurringPayment.ID,
		RazorpayOrderID:    orderID,
		RazorpayCustomerID: customerID,
		Amount:             req.AuthorizationAmount,
		Currency:           "INR",
		KeyID:              config.RazorpayKeyID,
	}, nil
}

// CreateRegistrationLink creates a Razorpay registration link for UPI recurring authorization
// Uses Razorpay's Invoice.CreateRegistrationLink API which handles customer + order internally
// https://razorpay.com/docs/api/payments/recurring-payments/upi/create-authorization-transaction/#121-create-a-registration-link
func (s *recurringPaymentService) CreateRegistrationLink(req models.CreateRegistrationLinkRequest) (*models.RegistrationLinkResponse, error) {
	// Get user details for email and phone
	user, err := s.userRepo.FindByID(req.UserID)
	if err != nil {
		return nil, fmt.Errorf("failed to find user: %w", err)
	}

	// Validate required user fields
	if user.Email == nil || *user.Email == "" {
		return nil, errors.New("user email is required")
	}
	if user.CountryCode == nil || *user.CountryCode == "" {
		return nil, errors.New("user country_code is required")
	}
	if user.Phone == nil || *user.Phone == "" {
		return nil, errors.New("user phone is required")
	}

	config, err := s.getConfig(req.AppName)
	if err != nil {
		return nil, fmt.Errorf("failed to find razorpay config: %w", err)
	}

	if !config.IsActive {
		return nil, errors.New("razorpay config is not active")
	}

	// Validate StartAt is at least 45 hours in the future
	minStartAt := time.Now().UTC().Add(45 * time.Hour)
	if req.StartAt.Before(minStartAt) {
		return nil, fmt.Errorf("start_at must be at least 45 hours in the future")
	}

	// Validate authorization_amount <= max_amount (Razorpay requirement for UPI recurring)
	if req.AuthorizationAmount > req.MaxAmount {
		return nil, fmt.Errorf("authorization_amount (%d) cannot be greater than max_amount (%d)", req.AuthorizationAmount, req.MaxAmount)
	}

	razorpayClient := s.getRazorpayClient(config)

	contact := "+" + *user.CountryCode + *user.Phone
	expireBy := time.Now().UTC().Add(24 * time.Hour).Unix()

	// Create registration link via Razorpay Invoice API
	// This API creates customer, order, and returns a hosted link in one call
	linkData := map[string]interface{}{
		"customer": map[string]interface{}{
			"email":   *user.Email,
			"contact": contact,
		},
		"type":         "link",
		"amount":       req.AuthorizationAmount,
		"currency":     "INR",
		"description":  "UPI Recurring Authorization",
		"expire_by":    expireBy,
		"sms_notify":   0,
		"email_notify": 0,
		"subscription_registration": map[string]interface{}{
			"method":     "upi",
			"max_amount": req.MaxAmount,
			"expire_at":  req.StartAt.AddDate(1, 0, 0).Unix(), // 1 year from start date
			"frequency":  "as_presented",
		},
		"notes": map[string]interface{}{
			"app_name": req.AppName,
		},
	}

	link, err := razorpayClient.Invoice.CreateRegistrationLink(linkData, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create registration link: %w", err)
	}

	shortURL := link["short_url"].(string)
	orderID := link["order_id"].(string)
	customerID := link["customer_id"].(string)
	fmt.Printf("[CreateRegistrationLink] Created registration link: %s, order: %s, customer: %s\n", shortURL, orderID, customerID)

	// Create database records using the customer_id and order_id from Razorpay
	authReq := models.CreateAuthorizationOrderRequest{
		UserID:              req.UserID,
		AppName:             req.AppName,
		AuthorizationAmount: req.AuthorizationAmount,
		MaxAmount:           req.MaxAmount,
		StartAt:             req.StartAt,
		Frequency:           req.Frequency,
	}
	_, _, _, err = s.createAuthorizationDBRecords(authReq, customerID, orderID)
	if err != nil {
		return nil, err
	}

	return &models.RegistrationLinkResponse{
		ShortURL: shortURL,
	}, nil
}

// createRazorpayCustomer creates a customer in Razorpay, or fetches existing if already exists
func (s *recurringPaymentService) createRazorpayCustomer(razorpayClient *razorpay.Client, user *userModels.User) (string, error) {
	// Validate required user fields
	if user.Email == nil || *user.Email == "" {
		return "", errors.New("user email is required")
	}
	if user.CountryCode == nil || *user.CountryCode == "" {
		return "", errors.New("user country_code is required")
	}
	if user.Phone == nil || *user.Phone == "" {
		return "", errors.New("user phone is required")
	}

	contact := "+" + *user.CountryCode + *user.Phone
	customerData := map[string]interface{}{
		"email":         *user.Email,
		"contact":       contact,
		"fail_existing": 0,
	}

	customer, err := razorpayClient.Customer.Create(customerData, nil)
	if err != nil {
		// If customer already exists, fetch by phone
		if strings.Contains(strings.ToLower(err.Error()), "already exists") {
			contact := "+" + *user.CountryCode + *user.Phone
			existingID, fetchErr := s.fetchCustomerByPhone(razorpayClient, contact)
			if fetchErr != nil {
				return "", fmt.Errorf("failed to fetch existing customer: %w", fetchErr)
			}
			fmt.Printf("[CreateAuthorizationOrder] Using existing customer: %s\n", existingID)
			return existingID, nil
		}
		return "", fmt.Errorf("failed to create razorpay customer: %w", err)
	}

	customerID := customer["id"].(string)
	fmt.Printf("[CreateAuthorizationOrder] Created customer: %s\n", customerID)
	return customerID, nil
}

// fetchCustomerByPhone fetches an existing Razorpay customer by phone contact
func (s *recurringPaymentService) fetchCustomerByPhone(razorpayClient *razorpay.Client, contact string) (string, error) {
	response, err := razorpayClient.Customer.All(map[string]interface{}{"contact": contact}, nil)
	if err != nil {
		return "", fmt.Errorf("failed to query customers: %w", err)
	}
	items, ok := response["items"].([]interface{})
	if !ok || len(items) == 0 {
		return "", errors.New("no customer found with phone")
	}
	customer, ok := items[0].(map[string]interface{})
	if !ok {
		return "", errors.New("invalid customer response")
	}
	customerID, ok := customer["id"].(string)
	if !ok {
		return "", errors.New("invalid customer id")
	}
	return customerID, nil
}

// createAuthorizationRazorpayOrder creates an authorization order in Razorpay
func (s *recurringPaymentService) createAuthorizationRazorpayOrder(
	razorpayClient *razorpay.Client,
	customerID string,
	req models.CreateAuthorizationOrderRequest,
) (string, error) {
	orderData := map[string]interface{}{
		"amount":          req.AuthorizationAmount,
		"currency":        "INR",
		"customer_id":     customerID,
		"method":          "upi",
		"payment_capture": true,
		"token": map[string]interface{}{
			"max_amount": req.MaxAmount,
			"expire_at":  req.StartAt.AddDate(1, 0, 0).Unix(), // 1 year from start date
			"frequency":  "as_presented",
		},
		"notes": map[string]interface{}{
			"purpose":  "authorization",
			"app_name": req.AppName,
		},
	}

	order, err := razorpayClient.Order.Create(orderData, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create razorpay order: %w", err)
	}

	orderID := order["id"].(string)
	fmt.Printf("[CreateAuthorizationOrder] Created order: %s\n", orderID)
	return orderID, nil
}

// createAuthorizationDBRecords creates the database records for authorization
func (s *recurringPaymentService) createAuthorizationDBRecords(
	req models.CreateAuthorizationOrderRequest,
	customerID, orderID string,
) (*models.RecurringPayment, *models.BillingCycle, *models.PaymentAttempt, error) {
	tx := s.repo.BeginTransaction()

	// Create RecurringPayment record
	endAt := req.StartAt.AddDate(1, 0, 0) // One year from start
	recurringPayment := &models.RecurringPayment{
		UserID:             req.UserID,
		AppName:            req.AppName,
		RazorpayCustomerID: &customerID,
		Status:             models.RecurringPaymentStatusCreated,
		MaxAmount:          req.MaxAmount,
		Frequency:          req.Frequency,
		StartAt:            &req.StartAt,
		EndAt:              &endAt,
		Metadata:           make(utils.Metadata),
	}

	if err := s.repo.CreateRecurringPayment(tx, recurringPayment); err != nil {
		s.repo.RollbackTransaction(tx)
		return nil, nil, nil, fmt.Errorf("failed to create recurring payment: %w", err)
	}

	// Create BillingCycle (cycle 0 for authorization)
	now := time.Now().UTC()
	bcEndAt := req.StartAt.Add(-24 * time.Hour)
	billingCycle := &models.BillingCycle{
		RecurringPaymentID: recurringPayment.ID,
		CycleNumber:        0,
		StartAt:            now,
		EndAt:              &bcEndAt,
		Amount:             req.AuthorizationAmount,
		Status:             models.BillingCycleStatusPending,
		ChargeAttempts:     1,
		Metadata:           make(utils.Metadata),
	}

	if err := s.repo.CreateBillingCycle(tx, billingCycle); err != nil {
		s.repo.RollbackTransaction(tx)
		return nil, nil, nil, fmt.Errorf("failed to create billing cycle: %w", err)
	}

	// Create PaymentAttempt
	paymentAttempt := &models.PaymentAttempt{
		BillingCycleID:  billingCycle.ID,
		AttemptNumber:   1,
		RazorpayOrderID: &orderID,
		Status:          models.PaymentAttemptStatusCreated,
		Amount:          req.AuthorizationAmount,
		Metadata:        make(utils.Metadata),
	}

	if err := s.repo.CreatePaymentAttempt(tx, paymentAttempt); err != nil {
		s.repo.RollbackTransaction(tx)
		return nil, nil, nil, fmt.Errorf("failed to create payment attempt: %w", err)
	}

	if err := s.repo.CommitTransaction(tx); err != nil {
		return nil, nil, nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return recurringPayment, billingCycle, paymentAttempt, nil
}

// VerifyAuthorizationPayment verifies the authorization payment and activates the mandate
func (s *recurringPaymentService) VerifyAuthorizationPayment(req models.VerifyAuthorizationPaymentRequest) (*models.RecurringPaymentResponse, error) {
	paymentAttempt, err := s.repo.FindPaymentAttemptByOrderID(req.RazorpayOrderID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("payment attempt not found")
		}
		return nil, err
	}

	billingCycle, recurringPayment, config, err := s.getPaymentRelatedRecords(paymentAttempt.BillingCycleID)
	if err != nil {
		return nil, err
	}

	// Verify signature
	if !s.verifySignature(req.RazorpayOrderID+"|"+req.RazorpayPaymentID, req.RazorpaySignature, config.RazorpayKeySecret) {
		return nil, errors.New("invalid signature")
	}

	// Fetch payment details from Razorpay
	razorpayClient := s.getRazorpayClient(config)
	payment, err := razorpayClient.Payment.Fetch(req.RazorpayPaymentID, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch payment: %w", err)
	}

	// Activate mandate using the same helper as webhook
	if err := s.activateMandateFromPayment(payment, paymentAttempt, billingCycle, recurringPayment); err != nil {
		return nil, err
	}

	fmt.Printf("[VerifyAuthorizationPayment] Activated mandate: recurring_payment_id=%s\n",
		recurringPayment.ID)

	response := recurringPayment.ToResponse()
	return &response, nil
}

// getPaymentRelatedRecords fetches billing cycle, recurring payment, and config
func (s *recurringPaymentService) getPaymentRelatedRecords(billingCycleID uuid.UUID) (*models.BillingCycle, *models.RecurringPayment, *clientModels.RazorpayConfig, error) {
	billingCycle, err := s.repo.FindBillingCycleByID(billingCycleID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to find billing cycle: %w", err)
	}

	recurringPayment, err := s.repo.FindRecurringPaymentByID(billingCycle.RecurringPaymentID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to find recurring payment: %w", err)
	}

	config, err := s.getConfig(recurringPayment.AppName)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to find razorpay config: %w", err)
	}

	return billingCycle, recurringPayment, config, nil
}

// fetchTokenIDFromPayment fetches payment details and extracts token_id
func (s *recurringPaymentService) fetchTokenIDFromPayment(razorpayClient *razorpay.Client, paymentID string) (string, error) {
	payment, err := razorpayClient.Payment.Fetch(paymentID, nil, nil)
	if err != nil {
		return "", fmt.Errorf("failed to fetch payment: %w", err)
	}

	tokenID, ok := payment["token_id"].(string)
	if !ok || tokenID == "" {
		return "", errors.New("token_id not found in payment")
	}

	return tokenID, nil
}

// fetchTokenByCustomerID fetches tokens by customer ID and returns the latest confirmed recurring token
// https://razorpay.com/docs/api/payments/recurring-payments/upi/tokens/#22-fetch-tokens-by-customer-id
func (s *recurringPaymentService) fetchTokenByCustomerID(customerID string, config *clientModels.RazorpayConfig) (string, error) {
	razorpayClient := s.getRazorpayClient(config)

	// Fetch tokens for the customer using All method
	tokensData, err := razorpayClient.Token.All(customerID, nil, nil)
	if err != nil {
		return "", fmt.Errorf("failed to fetch tokens for customer: %w", err)
	}

	// Extract items array
	items, ok := tokensData["items"].([]interface{})
	if !ok || len(items) == 0 {
		return "", errors.New("no tokens found for customer")
	}

	var latestToken string
	var latestCreatedAt int64

	for _, item := range items {
		token, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		// Check if token is recurring
		recurring, _ := token["recurring"].(bool)
		if !recurring {
			continue
		}

		// Check recurring_details.status is confirmed
		recurringDetails, ok := token["recurring_details"].(map[string]interface{})
		if !ok {
			continue
		}
		status, _ := recurringDetails["status"].(string)
		if status != "confirmed" {
			continue
		}

		// Get token ID
		tokenID, ok := token["id"].(string)
		if !ok || tokenID == "" {
			continue
		}

		// Get created_at timestamp to find the latest token
		createdAt, _ := token["created_at"].(float64)
		if createdAt > float64(latestCreatedAt) {
			latestCreatedAt = int64(createdAt)
			latestToken = tokenID
		}
	}

	if latestToken == "" {
		return "", errors.New("no confirmed recurring token found for customer")
	}

	return latestToken, nil
}

// updateAuthorizationPaymentRecords updates all records for verified authorization payment
func (s *recurringPaymentService) updateAuthorizationPaymentRecords(
	paymentAttempt *models.PaymentAttempt,
	billingCycle *models.BillingCycle,
	recurringPayment *models.RecurringPayment,
	paymentID, tokenID string,
) error {
	// Idempotency: skip if already processed
	if paymentAttempt.Status == models.PaymentAttemptStatusCaptured {
		return nil
	}

	tx := s.repo.BeginTransaction()

	now := time.Now().UTC()

	paymentAttempt.RazorpayPaymentID = &paymentID
	paymentAttempt.Status = models.PaymentAttemptStatusCaptured
	if err := s.repo.UpdatePaymentAttempt(tx, paymentAttempt); err != nil {
		s.repo.RollbackTransaction(tx)
		return fmt.Errorf("failed to update payment attempt: %w", err)
	}

	billingCycle.Status = models.BillingCycleStatusPaid
	billingCycle.LastAttemptAt = &now
	if err := s.repo.UpdateBillingCycle(tx, billingCycle); err != nil {
		s.repo.RollbackTransaction(tx)
		return fmt.Errorf("failed to update billing cycle: %w", err)
	}

	recurringPayment.TokenID = &tokenID
	recurringPayment.Status = models.RecurringPaymentStatusActive
	recurringPayment.LastChargedAt = &now
	recurringPayment.NextChargeAt = recurringPayment.StartAt
	recurringPayment.Metadata["authorized_at"] = now.Format(time.RFC3339)
	if err := s.repo.UpdateRecurringPayment(tx, recurringPayment); err != nil {
		s.repo.RollbackTransaction(tx)
		return fmt.Errorf("failed to update recurring payment: %w", err)
	}

	return s.repo.CommitTransaction(tx)
}

// GetRecurringPaymentByID retrieves a recurring payment by ID
func (s *recurringPaymentService) GetRecurringPaymentByID(id uuid.UUID) (*models.RecurringPaymentResponse, error) {
	rp, err := s.repo.FindRecurringPaymentByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("recurring payment not found")
		}
		return nil, err
	}

	response := rp.ToResponse()
	return &response, nil
}

// GetRecurringPaymentStatus checks if user has active recurring payment and completed authorization
func (s *recurringPaymentService) GetRecurringPaymentStatus(userID uuid.UUID, appName string) (*models.RecurringPaymentStatusResponse, error) {
	// Check for active recurring payment
	hasActive, err := s.hasActiveRecurringPayment(userID, appName)
	if err != nil {
		return nil, err
	}

	// Check if user has ever completed authorization payment (availed free trial)
	hasCompletedAuth, err := s.repo.HasCompletedAuthorizationPayment(userID, appName)
	if err != nil {
		return nil, err
	}

	return &models.RecurringPaymentStatusResponse{
		ActiveSubscription: hasActive,
		UsedFreeTrial:      hasCompletedAuth,
	}, nil
}

// hasActiveRecurringPayment checks if user has an active recurring payment mandate
func (s *recurringPaymentService) hasActiveRecurringPayment(userID uuid.UUID, appName string) (bool, error) {
	_, err := s.repo.FindActiveRecurringPaymentByUserID(userID, appName)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// ==================== Webhook Handling ====================

// HandleWebhook processes Razorpay webhook events
func (s *recurringPaymentService) HandleWebhook(payload []byte, signature string) error {
	fmt.Printf("[Webhook] ========== New Recurring Payment Webhook ==========\n")

	var event map[string]interface{}
	if err := json.Unmarshal(payload, &event); err != nil {
		return fmt.Errorf("failed to parse webhook payload: %w", err)
	}

	eventType, ok := event["event"].(string)
	if !ok {
		return errors.New("invalid webhook payload: missing event type")
	}

	payloadData, ok := event["payload"].(map[string]interface{})
	if !ok {
		return errors.New("invalid webhook payload: missing payload data")
	}

	fmt.Printf("[Webhook] Event type: %s\n", eventType)

	switch eventType {
	case "payment.captured", "payment.failed":
		return s.handlePaymentWebhook(eventType, payloadData, payload, signature)
	case "token.confirmed", "token.cancelled":
		return s.handleTokenWebhook(eventType, payloadData, payload, signature)
	default:
		fmt.Printf("[Webhook] Unknown event type (ignoring): %s\n", eventType)
		return nil
	}
}

// handlePaymentWebhook handles payment-related webhook events
func (s *recurringPaymentService) handlePaymentWebhook(eventType string, payloadData map[string]interface{}, rawPayload []byte, signature string) error {
	paymentEntity, err := extractPaymentEntity(payloadData)
	if err != nil {
		fmt.Printf("[Webhook ERROR] Failed to extract payment entity: %v\n", err)
		return err
	}

	// Log payment entity details for debugging
	if orderID, ok := paymentEntity["order_id"].(string); ok {
		fmt.Printf("[Webhook] Payment entity order_id: %s\n", orderID)
	}
	if paymentID, ok := paymentEntity["id"].(string); ok {
		fmt.Printf("[Webhook] Payment entity id: %s\n", paymentID)
	}

	paymentAttempt, err := s.findPaymentAttemptFromEntity(paymentEntity)
	if err != nil {
		fmt.Printf("[Webhook ERROR] Failed to find payment attempt: %v\n", err)
		return fmt.Errorf("failed to find payment attempt: %w", err)
	}

	// If paymentAttempt is nil, the order/payment doesn't belong to recurring_payment module
	// This can happen if the webhook is for a subscription or other payment type
	// Return nil to acknowledge the webhook without processing
	if paymentAttempt == nil {
		fmt.Printf("[Webhook] Payment not found in recurring_payment module, ignoring webhook\n")
		return nil
	}

	fmt.Printf("[Webhook] Found payment attempt: id=%s, billing_cycle_id=%s\n", paymentAttempt.ID, paymentAttempt.BillingCycleID)

	billingCycle, recurringPayment, config, err := s.getPaymentRelatedRecords(paymentAttempt.BillingCycleID)
	if err != nil {
		fmt.Printf("[Webhook ERROR] Failed to get payment related records: %v\n", err)
		return err
	}
	fmt.Printf("[Webhook] Found billing_cycle=%s, recurring_payment=%s, config_app=%s\n", billingCycle.ID, recurringPayment.ID, config.AppName)

	fmt.Printf("[Webhook] Verifying webhook signature...\n")
	if !s.verifyWebhookSignature(rawPayload, signature, config.RazorpayWebhookSecret) {
		fmt.Printf("[Webhook ERROR] Signature verification failed\n")
		return errors.New("invalid webhook signature")
	}
	fmt.Printf("[Webhook] Signature verified successfully\n")

	return s.processPaymentEvent(eventType, paymentEntity, paymentAttempt, billingCycle, recurringPayment)
}

// extractPaymentEntity extracts the payment entity from webhook payload
func extractPaymentEntity(payloadData map[string]interface{}) (map[string]interface{}, error) {
	paymentWrap, ok := payloadData["payment"].(map[string]interface{})
	if !ok {
		return nil, errors.New("invalid payment webhook payload")
	}

	paymentEntity, ok := paymentWrap["entity"].(map[string]interface{})
	if !ok {
		return nil, errors.New("invalid payment entity in webhook")
	}

	return paymentEntity, nil
}

// extractTokenEntity extracts the token entity from webhook payload
func extractTokenEntity(payloadData map[string]interface{}) (map[string]interface{}, error) {
	tokenWrap, ok := payloadData["token"].(map[string]interface{})
	if !ok {
		return nil, errors.New("invalid token webhook payload")
	}

	tokenEntity, ok := tokenWrap["entity"].(map[string]interface{})
	if !ok {
		return nil, errors.New("invalid token entity in webhook")
	}

	return tokenEntity, nil
}

// handleTokenWebhook handles token-related webhook events
func (s *recurringPaymentService) handleTokenWebhook(eventType string, payloadData map[string]interface{}, rawPayload []byte, signature string) error {
	tokenEntity, err := extractTokenEntity(payloadData)
	if err != nil {
		fmt.Printf("[Webhook ERROR] Failed to extract token entity: %v\n", err)
		return err
	}

	tokenID, ok := tokenEntity["id"].(string)
	if !ok || tokenID == "" {
		return errors.New("invalid token entity: missing id")
	}
	fmt.Printf("[Webhook] Token entity id: %s\n", tokenID)

	switch eventType {
	case "token.confirmed":
		return s.handleTokenConfirmed(tokenEntity, rawPayload, signature)
	case "token.cancelled":
		return s.handleTokenCancelled(tokenID, rawPayload, signature)
	default:
		fmt.Printf("[Webhook] Unknown token event type (ignoring): %s\n", eventType)
		return nil
	}
}

// handleTokenConfirmed processes a token.confirmed webhook event
func (s *recurringPaymentService) handleTokenConfirmed(tokenEntity map[string]interface{}, rawPayload []byte, signature string) error {
	tokenID, ok := tokenEntity["id"].(string)
	if !ok || tokenID == "" {
		return errors.New("invalid token entity: missing id")
	}

	// Defensive: only process recurring tokens
	recurring, _ := tokenEntity["recurring"].(bool)
	if !recurring {
		fmt.Printf("[Webhook] Token %s is not recurring, ignoring token.confirmed\n", tokenID)
		return nil
	}

	customerID, _ := tokenEntity["customer_id"].(string)

	// Try finding recurring payment by token_id first
	rp, err := s.repo.FindRecurringPaymentByTokenID(tokenID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("failed to find recurring payment by token: %w", err)
	}

	// If not found by token_id, try finding by customer_id
	if rp == nil {
		if customerID == "" {
			fmt.Printf("[Webhook] Token %s has no customer_id and no recurring payment found by token_id, ignoring token.confirmed\n", tokenID)
			return nil
		}
		rp, err = s.repo.FindRecurringPaymentByCustomerID(customerID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				fmt.Printf("[Webhook] No recurring payment found for customer %s with unset token, ignoring\n", customerID)
				return nil
			}
			return fmt.Errorf("failed to find recurring payment by customer: %w", err)
		}
		fmt.Printf("[Webhook] Found recurring_payment=%s for customer=%s, token not yet set\n", rp.ID, customerID)
	}

	// Get config for signature verification (must happen before any state changes)
	config, err := s.getConfig(rp.AppName)
	if err != nil {
		return fmt.Errorf("failed to get config: %w", err)
	}

	// Verify webhook signature
	if !s.verifyWebhookSignature(rawPayload, signature, config.RazorpayWebhookSecret) {
		fmt.Printf("[Webhook ERROR] Signature verification failed\n")
		return errors.New("invalid webhook signature")
	}
	fmt.Printf("[Webhook] Signature verified successfully\n")

	// Idempotency: skip if token already linked to this recurring payment
	if rp.TokenID != nil && *rp.TokenID == tokenID {
		fmt.Printf("[Webhook] Token %s already linked to recurring_payment=%s, skipping token.confirmed\n", tokenID, rp.ID)
		return nil
	}

	// Set the token_id
	rp.TokenID = &tokenID
	tx := s.repo.BeginTransaction()
	if err := s.repo.UpdateRecurringPayment(tx, rp); err != nil {
		s.repo.RollbackTransaction(tx)
		return fmt.Errorf("failed to update recurring payment with token: %w", err)
	}
	if err := s.repo.CommitTransaction(tx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	fmt.Printf("[Webhook] Processed token.confirmed: recurring_payment=%s, token_id=%s\n", rp.ID, tokenID)
	return nil
}

// handleTokenCancelled processes a token.cancelled webhook event
func (s *recurringPaymentService) handleTokenCancelled(tokenID string, rawPayload []byte, signature string) error {
	// Find recurring payment by token_id
	rp, err := s.repo.FindRecurringPaymentByTokenID(tokenID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fmt.Printf("[Webhook] Token %s not found in recurring_payments, ignoring\n", tokenID)
			return nil
		}
		return fmt.Errorf("failed to find recurring payment by token: %w", err)
	}

	fmt.Printf("[Webhook] Found recurring_payment=%s, status=%s\n", rp.ID, rp.Status)

	// Idempotency: skip if already in a terminal state
	if rp.Status == models.RecurringPaymentStatusCancelled || rp.Status == models.RecurringPaymentStatusExpired {
		fmt.Printf("[Webhook] Recurring payment %s already %s, skipping token.cancelled\n", rp.ID, rp.Status)
		return nil
	}

	// Handle active, paused, or created recurring payments
	if rp.Status != models.RecurringPaymentStatusActive &&
		rp.Status != models.RecurringPaymentStatusPaused &&
		rp.Status != models.RecurringPaymentStatusCreated {
		fmt.Printf("[Webhook] Recurring payment %s status is %s, not handling token.cancelled\n", rp.ID, rp.Status)
		return nil
	}

	// Get config for signature verification
	config, err := s.getConfig(rp.AppName)
	if err != nil {
		return fmt.Errorf("failed to get config: %w", err)
	}

	// Verify webhook signature
	if !s.verifyWebhookSignature(rawPayload, signature, config.RazorpayWebhookSecret) {
		fmt.Printf("[Webhook ERROR] Signature verification failed\n")
		return errors.New("invalid webhook signature")
	}
	fmt.Printf("[Webhook] Signature verified successfully\n")

	// Find latest billing cycle for this recurring payment
	bc, err := s.repo.FindLatestBillingCycleByRecurringPayment(rp.ID)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("failed to find latest billing cycle: %w", err)
	}

	// Get user for PostHog event
	user, _ := s.userRepo.FindByID(rp.UserID)

	// Determine amount for PostHog event
	var amount int64
	if bc != nil {
		amount = bc.Amount
	} else {
		amount = rp.MaxAmount
	}

	// Emit PostHog event before status change (following existing pattern)
	if user != nil {
		go s.sendPostHogRecurringPaymentCancelledEvent(rp, amount, user)
	}

	// Update records in a single transaction
	tx := s.repo.BeginTransaction()

	rp.Status = models.RecurringPaymentStatusCancelled
	rp.NextChargeAt = nil
	if err := s.repo.UpdateRecurringPayment(tx, rp); err != nil {
		s.repo.RollbackTransaction(tx)
		return fmt.Errorf("failed to update recurring payment: %w", err)
	}

	// Cancel pending billing cycle if it exists
	if bc != nil && bc.Status == models.BillingCycleStatusPending {
		bc.Status = models.BillingCycleStatusCancelled
		bc.NextAttemptAt = nil
		if err := s.repo.UpdateBillingCycle(tx, bc); err != nil {
			s.repo.RollbackTransaction(tx)
			return fmt.Errorf("failed to update billing cycle: %w", err)
		}
	}

	if err := s.repo.CommitTransaction(tx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	fmt.Printf("[Webhook] Processed token.cancelled: recurring_payment=%s, status=cancelled\n", rp.ID)
	return nil
}

// findPaymentAttemptFromEntity finds payment attempt from webhook payment entity
func (s *recurringPaymentService) findPaymentAttemptFromEntity(paymentEntity map[string]interface{}) (*models.PaymentAttempt, error) {
	if orderID, ok := paymentEntity["order_id"].(string); ok && orderID != "" {
		pa, err := s.repo.FindPaymentAttemptByOrderID(orderID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				fmt.Printf("[Webhook] Order %s not found in payment_attempts (not a recurring_payment order, ignoring)\n", orderID)
				return nil, nil // Return nil without error to acknowledge webhook
			}
			return nil, err
		}
		return pa, nil
	}
	if paymentID, ok := paymentEntity["id"].(string); ok {
		pa, err := s.repo.FindPaymentAttemptByPaymentID(paymentID)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				fmt.Printf("[Webhook] Payment %s not found in payment_attempts (not a recurring_payment payment, ignoring)\n", paymentID)
				return nil, nil // Return nil without error to acknowledge webhook
			}
			return nil, err
		}
		return pa, nil
	}
	return nil, errors.New("no order_id or payment_id in payment entity")
}

// processPaymentEvent updates records based on payment event
func (s *recurringPaymentService) processPaymentEvent(
	eventType string,
	paymentEntity map[string]interface{},
	paymentAttempt *models.PaymentAttempt,
	billingCycle *models.BillingCycle,
	recurringPayment *models.RecurringPayment,
) error {
	fmt.Printf("[processPaymentEvent] Processing %s for billing_cycle=%s, cycle_number=%d\n",
		eventType, billingCycle.ID, billingCycle.CycleNumber)

	if paymentID, ok := paymentEntity["id"].(string); ok {
		paymentAttempt.RazorpayPaymentID = &paymentID
	}

	switch eventType {
	case "payment.captured":
		// Handle authorization payment (cycle 0) - activate mandate
		if billingCycle.CycleNumber == 0 {
			fmt.Printf("[processPaymentEvent] Activating mandate for cycle 0 authorization\n")
			if err := s.activateMandateFromPayment(paymentEntity, paymentAttempt, billingCycle, recurringPayment); err != nil {
				fmt.Printf("[processPaymentEvent ERROR] Failed to activate mandate: %v\n", err)
				return err
			}
		} else {
			fmt.Printf("[processPaymentEvent] Handling payment captured for cycle %d\n", billingCycle.CycleNumber)
			s.handlePaymentCaptured(paymentAttempt, billingCycle, recurringPayment)
		}
	case "payment.failed":
		fmt.Printf("[processPaymentEvent] Handling payment failed for cycle %d\n", billingCycle.CycleNumber)
		s.handlePaymentFailed(paymentAttempt, billingCycle, recurringPayment, paymentEntity)
	}

	fmt.Printf("[processPaymentEvent] Saving records to database...\n")
	if err := s.saveRecordsInTransaction(paymentAttempt, billingCycle, recurringPayment); err != nil {
		fmt.Printf("[processPaymentEvent ERROR] Failed to save records: %v\n", err)
		return err
	}

	fmt.Printf("[Webhook] Processed %s: payment_attempt=%s, status=%s\n",
		eventType, paymentAttempt.ID, paymentAttempt.Status)

	return nil
}

// activateMandateFromPayment activates the mandate from payment data
// Used by both webhook and VerifyAuthorizationPayment
func (s *recurringPaymentService) activateMandateFromPayment(
	paymentEntity map[string]interface{},
	paymentAttempt *models.PaymentAttempt,
	billingCycle *models.BillingCycle,
	recurringPayment *models.RecurringPayment,
) error {
	// Idempotency: skip if already processed
	if paymentAttempt.Status == models.PaymentAttemptStatusCaptured {
		return nil
	}

	// Extract token_id from payment entity
	tokenID, err := extractTokenIDFromPaymentEntity(paymentEntity)
	if err != nil {
		config, configErr := s.getConfig(recurringPayment.AppName)
		if configErr != nil {
			return fmt.Errorf("failed to get config to fetch token: %w", configErr)
		}

		// Try to get customer_id from payment entity or recurring payment
		customerID := ""
		if cid, ok := paymentEntity["customer_id"].(string); ok && cid != "" {
			customerID = cid
		} else if recurringPayment.RazorpayCustomerID != nil {
			customerID = *recurringPayment.RazorpayCustomerID
		}

		if customerID == "" {
			return fmt.Errorf("failed to extract token_id and customer_id is missing: %w", err)
		}

		// Fetch token from Razorpay using customer ID
		var tokenErr error
		tokenID, tokenErr = s.fetchTokenByCustomerID(customerID, config)
		if tokenErr != nil {
			fmt.Printf("[activateMandateFromPayment ERROR] Failed to fetch token by customer ID: %v\n", tokenErr)
			return fmt.Errorf("failed to fetch token by customer ID: %w", tokenErr)
		}
	}

	// Get user for Meta event
	user, err := s.userRepo.FindByID(recurringPayment.UserID)
	if err != nil {
		fmt.Printf("[activateMandateFromPayment ERROR] Failed to find user: %v\n", err)
		return fmt.Errorf("failed to find user: %w", err)
	}

	// Get payment ID
	paymentID := ""
	if pid, ok := paymentEntity["id"].(string); ok {
		paymentID = pid
	}

	// Update all records using the same function as VerifyAuthorizationPayment
	if err := s.updateAuthorizationPaymentRecords(paymentAttempt, billingCycle, recurringPayment, paymentID, tokenID); err != nil {
		fmt.Printf("[activateMandateFromPayment ERROR] Failed to update authorization payment records: %v\n", err)
		return err
	}

	// Emit Meta pixel event for StartTrial
	go s.registerStartTrialMetaEvent(recurringPayment, paymentAttempt, user)

	// Emit PostHog event for recurring payment started
	go s.sendPostHogRecurringPaymentStartedEvent(recurringPayment, paymentAttempt, user)

	// If cycle 0 amount equals MaxAmount, this is a real charge — also emit Purchase event
	if paymentAttempt.Amount == recurringPayment.MaxAmount {
		fmt.Printf("[activateMandateFromPayment] Cycle 0 amount equals MaxAmount (%d), emitting captured events\n", paymentAttempt.Amount)

		// Emit Meta pixel event for Purchase
		go s.registerPurchaseMetaEvent(recurringPayment, paymentAttempt, billingCycle)

		// Emit PostHog event for recurring payment captured
		go s.sendPostHogRecurringPaymentCapturedEvent(recurringPayment, paymentAttempt, user)
	}

	return nil
}

// extractTokenIDFromPaymentEntity extracts token_id from payment entity
func extractTokenIDFromPaymentEntity(paymentEntity map[string]interface{}) (string, error) {
	tokenID, ok := paymentEntity["token_id"].(string)
	if !ok || tokenID == "" {
		// Also check for "token" key (Razorpay sometimes uses different key names)
		if tokenData, ok := paymentEntity["token"].(map[string]interface{}); ok {
			if tokenID, ok := tokenData["id"].(string); ok && tokenID != "" {
				return tokenID, nil
			}
		}
		return "", errors.New("token_id not found in payment entity")
	}
	return tokenID, nil
}

// ==================== Cron Jobs ====================

// RetryFailedBillingCycles processes billing cycles that need retry attempts
func (s *recurringPaymentService) RetryFailedBillingCycles() error {
	fmt.Printf("[RetryFailedBillingCycles] Starting\n")

	now := time.Now().UTC()
	windowStart := now.Add(25 * time.Hour)
	windowEnd := now.Add(50 * time.Hour)

	recurringPayments, err := s.repo.FindRecurringPaymentsForRetry(windowStart, windowEnd)
	if err != nil {
		return fmt.Errorf("failed to find recurring payments for retry: %w", err)
	}

	fmt.Printf("[RetryFailedBillingCycles] Found %d recurring payments needing retry\n", len(recurringPayments))

	processInParallel(recurringPayments, s.processPendingBillingCycleRetry, "RetryFailedBillingCycles")
	return nil
}

// ProcessNewBillingCycles sends pre-debit notifications for NEW billing cycles
func (s *recurringPaymentService) ProcessNewBillingCycles() error {
	fmt.Printf("[SendPreDebitNotifications] Starting\n")

	now := time.Now().UTC()
	windowStart := now.Add(25 * time.Hour)
	windowEnd := now.Add(50 * time.Hour)

	recurringPayments, err := s.repo.FindRecurringPaymentsForNewBillingCycle(windowStart, windowEnd)
	if err != nil {
		return fmt.Errorf("failed to find recurring payments for notification: %w", err)
	}

	fmt.Printf("[SendPreDebitNotifications] Found %d recurring payments needing notification\n", len(recurringPayments))

	processInParallel(recurringPayments, s.processNewBillingCycleForPayment, "ProcessNewBillingCycles")
	return nil
}

// processPendingBillingCycleRetry processes retry for a recurring payment's pending billing cycle
func (s *recurringPaymentService) processPendingBillingCycleRetry(rp models.RecurringPayment) error {
	bc, err := s.repo.FindLatestBillingCycleByRecurringPayment(rp.ID)
	if err != nil {
		return fmt.Errorf("failed to find latest billing cycle: %w", err)
	}

	if bc.Status != models.BillingCycleStatusPending {
		return fmt.Errorf("latest billing cycle is not pending")
	}

	if bc.ChargeAttempts >= 9 {
		return s.markBillingCycleAsFailed(bc, &rp)
	}

	hasPending, err := s.repo.HasPendingPaymentAttemptForBillingCycle(bc.ID)
	if err != nil {
		return fmt.Errorf("failed to check pending attempts: %w", err)
	}
	if hasPending {
		fmt.Printf("[processPendingBillingCycleRetry] Skipping billing_cycle %s: pending attempt already exists\n", bc.ID)
		return nil
	}

	config, err := s.getConfig(rp.AppName)
	if err != nil {
		return fmt.Errorf("failed to find config: %w", err)
	}

	razorpayClient := s.getRazorpayClient(config)

	newChargeAttempts := bc.ChargeAttempts + 1
	user, err := s.userRepo.FindByID(rp.UserID)
	if err != nil {
		return fmt.Errorf("failed to find user: %w", err)
	}
	orderID, err := s.createRetryOrder(razorpayClient, &rp, bc, newChargeAttempts, user)
	if err != nil {
		errorInfo := extractRazorpayError(err)
		return s.handleOrderCreationError(err, &rp, errorInfo.Code)
	}

	if err := s.createRetryDBRecords(bc, &rp, orderID, newChargeAttempts); err != nil {
		return err
	}

	fmt.Printf("[processPendingBillingCycleRetry] Created retry order %s for billing_cycle=%s, cycle=%d, attempt=%d\n",
		orderID, bc.ID, bc.CycleNumber, bc.ChargeAttempts)

	return nil
}

// markBillingCycleAsFailed marks a billing cycle and recurring payment as failed
func (s *recurringPaymentService) markBillingCycleAsFailed(bc *models.BillingCycle, rp *models.RecurringPayment) error {
	tx := s.repo.BeginTransaction()
	bc.Status = models.BillingCycleStatusFailed
	rp.Status = models.RecurringPaymentStatusExpired
	if err := s.repo.UpdateBillingCycle(tx, bc); err != nil {
		s.repo.RollbackTransaction(tx)
		return fmt.Errorf("failed to update billing cycle: %w", err)
	}
	if err := s.repo.UpdateRecurringPayment(tx, rp); err != nil {
		s.repo.RollbackTransaction(tx)
		return fmt.Errorf("failed to update recurring payment: %w", err)
	}
	if err := s.repo.CommitTransaction(tx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}
	return fmt.Errorf("max charge attempts (8) reached for billing cycle")
}

// createRetryOrder creates a Razorpay order for retry
func (s *recurringPaymentService) createRetryOrder(
	razorpayClient *razorpay.Client,
	rp *models.RecurringPayment,
	bc *models.BillingCycle,
	attemptNumber int,
	user *userModels.User,
) (string, error) {
	// Nil check to prevent panic
	if bc.NextAttemptAt == nil {
		return "", fmt.Errorf("billing cycle next_attempt_at is nil")
	}

	amount := calculateAttemptAmount(rp.MaxAmount, attemptNumber)
	orderData := map[string]interface{}{
		"amount":          amount,
		"currency":        "INR",
		"payment_capture": true,
		"notification": map[string]interface{}{
			"token_id":      rp.TokenID,
			"payment_after": bc.NextAttemptAt.Unix(),
		},
		"notes": map[string]interface{}{
			"recurring_payment_id": rp.ID.String(),
			"cycle_number":         bc.CycleNumber,
			"attempt_number":       attemptNumber,
		},
	}

	order, err := razorpayClient.Order.Create(orderData, nil)
	if err != nil {
		// Emit PostHog event for order creation failure
		errorInfo := extractRazorpayError(err)
		go s.sendPostHogOrderCreationFailedEvent(rp, amount, extractRazorpayErrorCodeFromInfo(errorInfo), user)
		return "", err
	}

	orderID, ok := order["id"].(string)
	if !ok || orderID == "" {
		return "", fmt.Errorf("invalid order response: missing or invalid id field")
	}
	return orderID, nil
}

// handleOrderCreationError handles errors during order creation
func (s *recurringPaymentService) handleOrderCreationError(
	err error,
	rp *models.RecurringPayment,
	errorCode string,
) error {
	if isTokenError(errorCode) || isMandateError(errorCode) {
		rp.Status = models.RecurringPaymentStatusExpired
		if updateErr := s.repo.UpdateRecurringPayment(nil, rp); updateErr != nil {
			return fmt.Errorf("token invalid, failed to update recurring payment: %w", updateErr)
		}
		return fmt.Errorf("token invalid, marked as expired: %w", err)
	}
	return fmt.Errorf("failed to create order: %w", err)
}

// createRetryDBRecords creates database records for retry
func (s *recurringPaymentService) createRetryDBRecords(
	bc *models.BillingCycle,
	rp *models.RecurringPayment,
	orderID string,
	attemptNumber int,
) error {
	tx := s.repo.BeginTransaction()

	bc.ChargeAttempts = attemptNumber
	if err := s.repo.UpdateBillingCycle(tx, bc); err != nil {
		s.repo.RollbackTransaction(tx)
		return fmt.Errorf("failed to update billing cycle: %w", err)
	}

	amount := calculateAttemptAmount(rp.MaxAmount, attemptNumber)
	paymentAttempt := &models.PaymentAttempt{
		BillingCycleID:  bc.ID,
		AttemptNumber:   attemptNumber,
		RazorpayOrderID: &orderID,
		Status:          models.PaymentAttemptStatusCreated,
		Amount:          amount,
		Metadata:        make(utils.Metadata),
	}
	if err := s.repo.CreatePaymentAttempt(tx, paymentAttempt); err != nil {
		s.repo.RollbackTransaction(tx)
		return fmt.Errorf("failed to create payment attempt: %w", err)
	}

	if err := s.repo.CommitTransaction(tx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// processNewBillingCycleForPayment creates a new billing cycle
func (s *recurringPaymentService) processNewBillingCycleForPayment(rp models.RecurringPayment) error {
	config, err := s.getConfig(rp.AppName)
	if err != nil {
		return fmt.Errorf("failed to find config: %w", err)
	}

	razorpayClient := s.getRazorpayClient(config)

	billingCycles, err := s.repo.FindBillingCyclesByRecurringPayment(rp.ID)
	if err != nil {
		return fmt.Errorf("failed to find billing cycles: %w", err)
	}

	nextCycleNumber := len(billingCycles)
	amount := calculateAttemptAmount(rp.MaxAmount, 1) // First attempt of new cycle
	nextChargeAt := s.calculateNextChargeDate(rp)
	endAt := nextChargeAt.Add(-24 * time.Hour)

	// Check if billing cycle would extend beyond mandate expiry BEFORE creating order
	if rp.EndAt != nil && endAt.After(*rp.EndAt) {
		// Mark as expired - mandate ends before this billing cycle would complete
		rp.Status = models.RecurringPaymentStatusExpired
		if err := s.repo.UpdateRecurringPayment(nil, &rp); err != nil {
			return fmt.Errorf("failed to update recurring payment: %w", err)
		}
		return fmt.Errorf("billing cycle end_at (%s) would exceed mandate end_at (%s)", endAt.Format(time.RFC3339), rp.EndAt.Format(time.RFC3339))
	}

	user, err := s.userRepo.FindByID(rp.UserID)
	if err != nil {
		return fmt.Errorf("failed to find user: %w", err)
	}
	orderID, err := s.createNewCycleOrder(razorpayClient, &rp, nextCycleNumber, amount, user)
	if err != nil {
		errorInfo := extractRazorpayError(err)
		return s.handleOrderCreationError(err, &rp, errorInfo.Code)
	}

	if err := s.createNewCycleDBRecords(&rp, orderID, nextCycleNumber, amount, endAt); err != nil {
		return err
	}

	fmt.Printf("[processNewBillingCycleForPayment] Created order %s for recurring_payment=%s, cycle=%d, attempt=%d\n",
		orderID, rp.ID, nextCycleNumber, 1)

	return nil
}

// createNewCycleOrder creates a Razorpay order for a new billing cycle
func (s *recurringPaymentService) createNewCycleOrder(
	razorpayClient *razorpay.Client,
	rp *models.RecurringPayment,
	cycleNumber int,
	amount int64,
	user *userModels.User,
) (string, error) {
	// Nil check to prevent panic
	if rp.NextChargeAt == nil {
		return "", fmt.Errorf("recurring payment next_charge_at is nil")
	}

	orderData := map[string]interface{}{
		"amount":          amount,
		"currency":        "INR",
		"payment_capture": true,
		"notification": map[string]interface{}{
			"token_id":      rp.TokenID,
			"payment_after": rp.NextChargeAt.Unix(),
		},
		"notes": map[string]interface{}{
			"recurring_payment_id": rp.ID.String(),
			"cycle_number":         cycleNumber,
			"attempt_number":       1,
		},
	}

	order, err := razorpayClient.Order.Create(orderData, nil)
	if err != nil {
		// Emit PostHog event for order creation failure
		errorInfo := extractRazorpayError(err)
		go s.sendPostHogOrderCreationFailedEvent(rp, amount, extractRazorpayErrorCodeFromInfo(errorInfo), user)
		return "", err
	}

	orderID, ok := order["id"].(string)
	if !ok || orderID == "" {
		return "", fmt.Errorf("invalid order response: missing or invalid id field")
	}
	return orderID, nil
}

// createNewCycleDBRecords creates database records for a new billing cycle
func (s *recurringPaymentService) createNewCycleDBRecords(
	rp *models.RecurringPayment,
	orderID string,
	cycleNumber int,
	amount int64,
	endAt time.Time,
) error {
	tx := s.repo.BeginTransaction()

	// Nil check for NextChargeAt to prevent panic
	if rp.NextChargeAt == nil {
		s.repo.RollbackTransaction(tx)
		return fmt.Errorf("recurring payment next_charge_at is nil")
	}

	billingCycle := &models.BillingCycle{
		RecurringPaymentID: rp.ID,
		CycleNumber:        cycleNumber,
		StartAt:            *rp.NextChargeAt,
		EndAt:              &endAt,
		Amount:             amount,
		Status:             models.BillingCycleStatusPending,
		ChargeAttempts:     1,
		NextAttemptAt:      rp.NextChargeAt,
		Metadata:           make(utils.Metadata),
	}
	if err := s.repo.CreateBillingCycle(tx, billingCycle); err != nil {
		s.repo.RollbackTransaction(tx)
		return fmt.Errorf("failed to create billing cycle: %w", err)
	}

	paymentAttempt := &models.PaymentAttempt{
		BillingCycleID:  billingCycle.ID,
		AttemptNumber:   1,
		RazorpayOrderID: &orderID,
		Status:          models.PaymentAttemptStatusCreated,
		Amount:          amount,
		Metadata:        make(utils.Metadata),
	}
	if err := s.repo.CreatePaymentAttempt(tx, paymentAttempt); err != nil {
		s.repo.RollbackTransaction(tx)
		return fmt.Errorf("failed to create payment attempt: %w", err)
	}

	if err := s.repo.CommitTransaction(tx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// ChargePendingPayments charges pending payment attempts via Razorpay SDK
func (s *recurringPaymentService) ChargePendingPayments() error {
	fmt.Printf("[ChargePendingPayments] Starting\n")

	now := time.Now().UTC()
	paymentAttempts, err := s.repo.FindPendingPaymentAttempts(now)
	if err != nil {
		return fmt.Errorf("failed to find pending payment attempts: %w", err)
	}

	fmt.Printf("[ChargePendingPayments] Found %d pending payment attempts\n", len(paymentAttempts))

	processInParallel(paymentAttempts, s.createRazorpayRecurringPayment, "ChargePendingPayments")
	return nil
}

// createRazorpayRecurringPayment creates a recurring payment via Razorpay SDK
func (s *recurringPaymentService) createRazorpayRecurringPayment(pa models.PaymentAttempt) error {
	billingCycle, recurringPayment, user, config, err := s.getChargeRelatedRecords(&pa)
	if err != nil {
		return err
	}

	razorpayClient := s.getRazorpayClient(config)

	order, err := razorpayClient.Order.Fetch(*pa.RazorpayOrderID, nil, nil)
	if err != nil {
		return fmt.Errorf("failed to fetch order: %w", err)
	}

	if orderStatus, ok := order["status"].(string); ok && orderStatus == "paid" {
		fmt.Printf("[createRazorpayRecurringPayment] Order %s already paid, updating records\n", derefString(pa.RazorpayOrderID))
		// Extract payment_id from order for data completeness
		if paymentID := extractPaymentIDFromOrder(order); paymentID != nil {
			pa.RazorpayPaymentID = paymentID
		}
		// Update all records to reflect paid status
		s.handlePaymentCaptured(&pa, billingCycle, recurringPayment)
		if err := s.saveRecordsInTransaction(&pa, billingCycle, recurringPayment); err != nil {
			return err
		}
		return nil
	}

	// Validate required user fields
	if user.Email == nil || *user.Email == "" {
		return errors.New("user email is required")
	}
	if user.CountryCode == nil || *user.CountryCode == "" {
		return errors.New("user country_code is required")
	}
	if user.Phone == nil || *user.Phone == "" {
		return errors.New("user phone is required")
	}

	contact := "+" + *user.CountryCode + *user.Phone
	recurringData := map[string]interface{}{
		"email":       *user.Email,
		"contact":     contact,
		"amount":      pa.Amount,
		"currency":    "INR",
		"order_id":    pa.RazorpayOrderID,
		"customer_id": recurringPayment.RazorpayCustomerID,
		"token":       recurringPayment.TokenID,
		"recurring":   true,
		"notes": map[string]interface{}{
			"recurring_payment_id": recurringPayment.ID.String(),
			"cycle_number":         billingCycle.CycleNumber,
			"attempt_number":       pa.AttemptNumber,
		},
	}

	payment, err := razorpayClient.Payment.CreateRecurringPayment(recurringData, nil)
	// payment, err := s.createRecurringPaymentDirectHTTP(config, recurringData)
	if err != nil {
		errorInfo := extractRazorpayError(err)
		// Emit PostHog event for recurring payment creation failure
		go s.sendPostHogRecurringPaymentCreationFailedEvent(recurringPayment, pa.Amount, user, extractRazorpayErrorCodeFromInfo(errorInfo))
		return s.handleRecurringPaymentError(err, &pa, billingCycle, recurringPayment, errorInfo.Code, errorInfo.Description, user)
	}

	if paymentID, ok := payment["razorpay_payment_id"].(string); ok {
		pa.RazorpayPaymentID = &paymentID
	}
	pa.Status = models.PaymentAttemptStatusPending

	now := time.Now().UTC()
	billingCycle.LastAttemptAt = &now

	// Update both records in a single transaction
	tx := s.repo.BeginTransaction()
	if err := s.repo.UpdatePaymentAttempt(tx, &pa); err != nil {
		s.repo.RollbackTransaction(tx)
		return fmt.Errorf("failed to update payment attempt: %w", err)
	}
	if err := s.repo.UpdateBillingCycle(tx, billingCycle); err != nil {
		s.repo.RollbackTransaction(tx)
		return fmt.Errorf("failed to update billing cycle: %w", err)
	}
	if err := s.repo.CommitTransaction(tx); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	fmt.Printf("[createRazorpayRecurringPayment] Created recurring payment: payment_id=%s, order_id=%s\n",
		derefString(pa.RazorpayPaymentID),
		derefString(pa.RazorpayOrderID),
	)

	return nil
}

// createRecurringPaymentDirectHTTP makes a raw HTTP POST to Razorpay's create/recurring endpoint
// to debug whether the 404 is from the SDK or the URL itself
func (s *recurringPaymentService) createRecurringPaymentDirectHTTP(
	config *clientModels.RazorpayConfig,
	data map[string]interface{},
) (map[string]interface{}, error) {
	jsonBody, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	fmt.Printf("[DEBUG createRecurringPaymentDirectHTTP] Payload: %s\n", string(jsonBody))

	url := "https://api.razorpay.com/v1/payments/create/recurring"
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	req.SetBasicAuth(config.RazorpayKeyID, config.RazorpayKeySecret)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	fmt.Printf("[DEBUG createRecurringPaymentDirectHTTP] Status: %d, Body: %s\n", resp.StatusCode, string(body))

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response JSON: %w", err)
	}

	return result, nil
}

// getChargeRelatedRecords fetches all records needed for charging
func (s *recurringPaymentService) getChargeRelatedRecords(pa *models.PaymentAttempt) (*models.BillingCycle, *models.RecurringPayment, *userModels.User, *clientModels.RazorpayConfig, error) {
	billingCycle, err := s.repo.FindBillingCycleByID(pa.BillingCycleID)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to find billing cycle: %w", err)
	}

	recurringPayment, err := s.repo.FindRecurringPaymentByID(billingCycle.RecurringPaymentID)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to find recurring payment: %w", err)
	}

	user, err := s.userRepo.FindByID(recurringPayment.UserID)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to find user: %w", err)
	}

	config, err := s.getConfig(recurringPayment.AppName)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to find config: %w", err)
	}

	return billingCycle, recurringPayment, user, config, nil
}

// handleRecurringPaymentError handles errors during recurring payment creation
func (s *recurringPaymentService) handleRecurringPaymentError(
	err error,
	pa *models.PaymentAttempt,
	billingCycle *models.BillingCycle,
	recurringPayment *models.RecurringPayment,
	errorCode string,
	errorDescription string,
	user *userModels.User,
) error {
	now := time.Now().UTC()

	// 1. Update payment attempt fields
	pa.Status = models.PaymentAttemptStatusFailed
	pa.ErrorCode = errorCode
	pa.ErrorDescription = errorDescription

	// 2. Update billing cycle fields
	billingCycle.LastAttemptAt = &now

	// 3. Determine error category and update records accordingly
	isMandateCancelled := strings.ToLower(errorCode) == "mandate_cancelled"
	isTokenOrMandateError := isTokenError(errorCode) || isMandateError(errorCode)
	needsRPUpdate := false

	if isMandateCancelled {
		// Emit PostHog event for recurring payment cancelled
		go s.sendPostHogRecurringPaymentCancelledEvent(recurringPayment, pa.Amount, user)
		recurringPayment.Status = models.RecurringPaymentStatusCancelled
		needsRPUpdate = true
	} else if isTokenOrMandateError {
		recurringPayment.Status = models.RecurringPaymentStatusExpired
		needsRPUpdate = true
	} else if billingCycle.CycleNumber > 0 {
		// Generic error on a billing cycle: schedule retry or mark expired
		if billingCycle.ChargeAttempts >= 9 {
			billingCycle.Status = models.BillingCycleStatusFailed
			recurringPayment.Status = models.RecurringPaymentStatusExpired
			needsRPUpdate = true
		} else {
			scheduleRetry(billingCycle)
		}
	}

	// 4. Persist all changes in a single transaction
	tx := s.repo.BeginTransaction()

	if updateErr := s.repo.UpdatePaymentAttempt(tx, pa); updateErr != nil {
		s.repo.RollbackTransaction(tx)
		return fmt.Errorf("failed to update payment attempt: %w", updateErr)
	}

	if updateErr := s.repo.UpdateBillingCycle(tx, billingCycle); updateErr != nil {
		s.repo.RollbackTransaction(tx)
		return fmt.Errorf("failed to update billing cycle: %w", updateErr)
	}

	if needsRPUpdate {
		if updateErr := s.repo.UpdateRecurringPayment(tx, recurringPayment); updateErr != nil {
			s.repo.RollbackTransaction(tx)
			return fmt.Errorf("failed to update recurring payment: %w", updateErr)
		}
	}

	if commitErr := s.repo.CommitTransaction(tx); commitErr != nil {
		return fmt.Errorf("failed to commit transaction: %w", commitErr)
	}

	// 5. Return appropriate error message
	if isMandateCancelled {
		return fmt.Errorf("mandate cancelled: %w", err)
	}
	if isTokenOrMandateError {
		return fmt.Errorf("token invalid, marked as expired: %w", err)
	}
	return fmt.Errorf("failed to create recurring payment: %w", err)
}

// ReconcilePayments reconciles stale pending payments with Razorpay
func (s *recurringPaymentService) ReconcilePayments() error {
	fmt.Printf("[ReconcilePayments] Starting\n")

	now := time.Now().UTC()
	paymentAttempts, err := s.repo.FindPendingPaymentAttempts(now)
	if err != nil {
		return fmt.Errorf("failed to find stale payment attempts: %w", err)
	}

	fmt.Printf("[ReconcilePayments] Found %d stale payment attempts\n", len(paymentAttempts))

	processInParallel(paymentAttempts, s.reconcilePayment, "ReconcilePayments")
	return nil
}

// reconcilePayment reconciles a single payment attempt with Razorpay
func (s *recurringPaymentService) reconcilePayment(pa models.PaymentAttempt) error {
	billingCycle, recurringPayment, config, err := s.getPaymentRelatedRecords(pa.BillingCycleID)
	if err != nil {
		return err
	}

	razorpayClient := s.getRazorpayClient(config)

	if pa.RazorpayPaymentID != nil && *pa.RazorpayPaymentID != "" {
		if err := s.reconcileFromPaymentID(razorpayClient, &pa, billingCycle, recurringPayment); err != nil {
			return err
		}
	} else if pa.RazorpayOrderID != nil && *pa.RazorpayOrderID != "" {
		if err := s.reconcileFromOrderID(razorpayClient, &pa, billingCycle, recurringPayment); err != nil {
			return err
		}
	}

	if err := s.saveRecordsInTransaction(&pa, billingCycle, recurringPayment); err != nil {
		return err
	}

	fmt.Printf("[reconcilePayment] Reconciled payment_attempt=%s, status=%s\n", pa.ID, pa.Status)

	return nil
}

// reconcileFromPaymentID reconciles using payment ID
func (s *recurringPaymentService) reconcileFromPaymentID(
	razorpayClient *razorpay.Client,
	pa *models.PaymentAttempt,
	billingCycle *models.BillingCycle,
	recurringPayment *models.RecurringPayment,
) error {
	payment, err := razorpayClient.Payment.Fetch(*pa.RazorpayPaymentID, nil, nil)
	if err != nil {
		return fmt.Errorf("failed to fetch payment: %w", err)
	}

	status, ok := payment["status"].(string)
	if !ok {
		return nil
	}

	switch status {
	case "captured":
		s.handlePaymentCaptured(pa, billingCycle, recurringPayment)
	case "failed":
		s.handlePaymentFailed(pa, billingCycle, recurringPayment, payment)
	}

	return nil
}

// extractPaymentIDFromOrder extracts the payment_id from an order response
// Order response contains payments under: payments.items[0].id
func extractPaymentIDFromOrder(order map[string]interface{}) *string {
	payments, ok := order["payments"].(map[string]interface{})
	if !ok {
		return nil
	}
	items, ok := payments["items"].([]interface{})
	if !ok || len(items) == 0 {
		return nil
	}
	item, ok := items[0].(map[string]interface{})
	if !ok {
		return nil
	}
	paymentID, ok := item["id"].(string)
	if !ok {
		return nil
	}
	return &paymentID
}

// reconcileFromOrderID reconciles using order ID
func (s *recurringPaymentService) reconcileFromOrderID(
	razorpayClient *razorpay.Client,
	pa *models.PaymentAttempt,
	billingCycle *models.BillingCycle,
	recurringPayment *models.RecurringPayment,
) error {
	order, err := razorpayClient.Order.Fetch(*pa.RazorpayOrderID, nil, nil)
	if err != nil {
		return fmt.Errorf("failed to fetch order: %w", err)
	}

	if status, ok := order["status"].(string); ok && status == "paid" {
		// Extract payment_id from order for data completeness
		if paymentID := extractPaymentIDFromOrder(order); paymentID != nil {
			pa.RazorpayPaymentID = paymentID
		}
		s.handlePaymentCaptured(pa, billingCycle, recurringPayment)
	}

	return nil
}

// ==================== Signature Verification ====================

// verifySignature verifies Razorpay signature
func (s *recurringPaymentService) verifySignature(message, signature, keySecret string) bool {
	mac := hmac.New(sha256.New, []byte(keySecret))
	mac.Write([]byte(message))
	expectedMAC := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(signature), []byte(expectedMAC))
}

// verifyWebhookSignature verifies webhook signature
func (s *recurringPaymentService) verifyWebhookSignature(payload []byte, signature, webhookSecret string) bool {
	mac := hmac.New(sha256.New, []byte(webhookSecret))
	mac.Write(payload)
	expectedMAC := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(signature), []byte(expectedMAC))
}

// ==================== Date Calculation ====================

// calculateNextChargeDate calculates the next charge date based on frequency
func (s *recurringPaymentService) calculateNextChargeDate(rp models.RecurringPayment) time.Time {
	baseTime := time.Now().UTC()
	if rp.NextChargeAt != nil {
		baseTime = *rp.NextChargeAt
	}

	switch rp.Frequency {
	case "daily":
		return baseTime.AddDate(0, 0, 1)
	case "weekly":
		return baseTime.AddDate(0, 0, 7)
	case "fortnightly":
		return baseTime.AddDate(0, 0, 15)
	case "bimonthly":
		return baseTime.AddDate(0, 0, 15)
	case "monthly":
		return baseTime.AddDate(0, 1, 0)
	case "quarterly":
		return baseTime.AddDate(0, 3, 0)
	case "half_yearly":
		return baseTime.AddDate(0, 6, 0)
	case "yearly":
		return baseTime.AddDate(1, 0, 0)
	default:
		return baseTime.AddDate(0, 1, 0)
	}
}

// ==================== Meta Dataset Events ====================

// metaEventParams contains parameters for sending a Meta event
type metaEventParams struct {
	eventName   string
	contentName string
	eventID     string // ID used for deduplication
}

// getMetaConfig fetches and validates the Meta dataset config for an app
func (s *recurringPaymentService) getMetaConfig(appName string) (*metaDatasetModels.MetaDatasetConfig, bool) {
	env := utils.GetEnv("GO_ENV", "local")
	metaConfig, err := s.metaDatasetRepo.FindByAppNameAndEnv(appName, env)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fmt.Printf("[Meta Dataset] No meta dataset config found for app: %s, environment: %s. Skipping event.\n", appName, env)
			sentry.WithScope(func(scope *sentry.Scope) {
				scope.SetTag("app_name", appName)
				sentry.CaptureException(fmt.Errorf("[Meta Dataset] no config found for app %s, env %s", appName, env))
			})
		} else {
			fmt.Printf("[Meta Dataset ERROR] Failed to get meta dataset config: %v\n", err)
			sentry.WithScope(func(scope *sentry.Scope) {
				scope.SetTag("app_name", appName)
				sentry.CaptureException(fmt.Errorf("[Meta Dataset] failed to get config for app %s: %w", appName, err))
			})
		}
		return nil, false
	}

	if !metaConfig.IsActive {
		fmt.Printf("[Meta Dataset] Meta dataset config is inactive for app: %s. Skipping event.\n", appName)
		sentry.WithScope(func(scope *sentry.Scope) {
			scope.SetTag("app_name", appName)
			sentry.CaptureException(fmt.Errorf("[Meta Dataset] config is inactive for app: %s, env: %s", appName, env))
		})
		return nil, false
	}

	if metaConfig.DatasetID == "" {
		fmt.Printf("[Meta Dataset ERROR] dataset_id is empty for app: %s, environment: %s\n", appName, env)
		sentry.WithScope(func(scope *sentry.Scope) {
			scope.SetTag("app_name", appName)
			sentry.CaptureException(fmt.Errorf("[Meta Dataset] dataset_id is empty for app: %s, env: %s", appName, env))
		})
		return nil, false
	}

	return metaConfig, true
}

// derefString safely dereferences a *string, returning empty string if nil.
func derefString(s *string) string {
	if s != nil {
		return *s
	}
	return ""
}

// getUserInfo retrieves the user's phone and app_data from user metadata.
// Returns phone string and app_data from user metadata (or nil if not available).
func (s *recurringPaymentService) getUserInfo(userID uuid.UUID) (string, *notification.AppData) {
	user, err := s.userRepo.FindByID(userID)
	if err != nil {
		fmt.Printf("[Meta Dataset ERROR] Failed to find user for phone hash: %v\n", err)
		sentry.WithScope(func(scope *sentry.Scope) {
			scope.SetTag("app_name", "recurring_payment")
			scope.SetTag("user_id", userID.String())
			sentry.CaptureException(fmt.Errorf("[Meta Dataset] failed to find user %s for phone hash: %w", userID, err))
		})
		return "", nil
	}

	var phone string
	if user.CountryCode != nil && user.Phone != nil {
		phone = *user.CountryCode + *user.Phone
	}

	appData := notification.AppDataFromUserMetadata(user.Metadata)
	return phone, &appData
}

// sendMetaEvent sends a Meta Conversions API event
func (s *recurringPaymentService) sendMetaEvent(
	recurringPayment *models.RecurringPayment,
	amount int64,
	params metaEventParams,
	metaConfig *metaDatasetModels.MetaDatasetConfig,
	phone string,
	appData *notification.AppData,
) {
	value := float64(amount) / 100.0

	eventData := notification.NewAppEventData(notification.MetaEventParams{
		DatasetID:    metaConfig.DatasetID,
		AccessToken:  metaConfig.AccessToken,
		EventName:    params.eventName,
		ActionSource: "app",
		Phone:        phone,
		UserID:       recurringPayment.UserID.String(),
		Currency:     "INR",
		Value:        value,
		ContentName:  params.contentName,
		ContentID:    params.eventID,
		DedupSource:  params.eventID,
		EventID:      params.eventID,
		AppData:      appData,
	})

	if err := s.metaDatasetClient.SendEvent(eventData); err != nil {
		fmt.Printf("[Meta Dataset ERROR] Failed to send %s event: %v\n", params.eventName, err)
		sentry.WithScope(func(scope *sentry.Scope) {
			scope.SetTag("app_name", recurringPayment.AppName)
			scope.SetTag("recurring_payment_id", recurringPayment.ID.String())
			scope.SetTag("event_name", params.eventName)
			sentry.CaptureException(fmt.Errorf("[Meta Dataset] failed to send %s event for recurring_payment %s: %w", params.eventName, recurringPayment.ID, err))
		})
		return
	}

	fmt.Printf("[Meta Dataset] Successfully sent %s event for recurring_payment %s (%.2f INR) to dataset_id %s\n",
		params.eventName, recurringPayment.ID, value, metaConfig.DatasetID)
}

// sendMetaDatasetStartTrialEvent sends StartTrial event to Meta CAPI
func (s *recurringPaymentService) sendMetaDatasetStartTrialEvent(
	recurringPayment *models.RecurringPayment,
	paymentAttempt *models.PaymentAttempt,
	user *userModels.User,
	eventID uuid.UUID,
) {
	fmt.Printf("[Meta Dataset] Processing StartTrial event for recurring_payment: %s\n", recurringPayment.ID)

	metaConfig, ok := s.getMetaConfig(recurringPayment.AppName)
	if !ok {
		return
	}

	var phone string
	if user.CountryCode != nil && user.Phone != nil {
		phone = *user.CountryCode + *user.Phone
	}
	appData := notification.AppDataFromUserMetadata(user.Metadata)

	s.sendMetaEvent(recurringPayment, paymentAttempt.Amount, metaEventParams{
		eventName:   "StartTrial",
		contentName: fmt.Sprintf("%s Recurring Payment", recurringPayment.AppName),
		eventID:     eventID.String(),
	}, metaConfig, phone, &appData)
}

// sendMetaDatasetPurchaseEvent sends Purchase event to Meta CAPI
func (s *recurringPaymentService) sendMetaDatasetPurchaseEvent(
	recurringPayment *models.RecurringPayment,
	paymentAttempt *models.PaymentAttempt,
	billingCycle *models.BillingCycle,
	eventID uuid.UUID,
) {
	fmt.Printf("[Meta Dataset] Processing Purchase event for recurring_payment: %s\n", recurringPayment.ID)

	metaConfig, ok := s.getMetaConfig(recurringPayment.AppName)
	if !ok {
		return
	}

	phone, appData := s.getUserInfo(recurringPayment.UserID)

	s.sendMetaEvent(recurringPayment, paymentAttempt.Amount, metaEventParams{
		eventName:   "Purchase",
		contentName: fmt.Sprintf("%s Recurring Payment - Cycle %d", recurringPayment.AppName, billingCycle.CycleNumber),
		eventID:     eventID.String(),
	}, metaConfig, phone, appData)
}

// registerStartTrialMetaEvent creates a StartTrial meta event and sends it to Meta CAPI
func (s *recurringPaymentService) registerStartTrialMetaEvent(
	recurringPayment *models.RecurringPayment,
	paymentAttempt *models.PaymentAttempt,
	user *userModels.User,
) {
	value := float64(paymentAttempt.Amount) / 100.0
	event, err := s.metaEventService.CreateMetaEvent(nil, recurringPayment.UserID, recurringPayment.AppName, "StartTrial", map[string]interface{}{
		"value":    value,
		"currency": "INR",
	})
	if err != nil {
		fmt.Printf("[Meta Event ERROR] Failed to create StartTrial meta event: %v\n", err)
		return
	}
	s.sendMetaDatasetStartTrialEvent(recurringPayment, paymentAttempt, user, event.ID)
}

// registerPurchaseMetaEvent creates a Purchase meta event and sends it to Meta CAPI
func (s *recurringPaymentService) registerPurchaseMetaEvent(
	recurringPayment *models.RecurringPayment,
	paymentAttempt *models.PaymentAttempt,
	billingCycle *models.BillingCycle,
) {
	value := float64(paymentAttempt.Amount) / 100.0
	event, err := s.metaEventService.CreateMetaEvent(nil, recurringPayment.UserID, recurringPayment.AppName, "Purchase", map[string]interface{}{
		"value":    value,
		"currency": "INR",
	})
	if err != nil {
		fmt.Printf("[Meta Event ERROR] Failed to create Purchase meta event: %v\n", err)
		return
	}
	s.sendMetaDatasetPurchaseEvent(recurringPayment, paymentAttempt, billingCycle, event.ID)
}

// ==================== PostHog Analytics Events ====================

const (
	// PostHog event names
	PostHogEventTrialStarted                   = "TrialStarted"
	PostHogEventSubscriptionCharged            = "SubscriptionCharged"
	PostHogEventRecurringPaymentFailed         = "RecurringPaymentFailed"
	PostHogEventRecurringPaymentCreationFailed = "RecurringPaymentCreationFailed"
	PostHogEventSubscriptionCancelled          = "SubscriptionCancelled"
	PostHogEventOrderCreationFailed            = "OrderCreationFailed"
)

// getPostHogConfig fetches and validates the PostHog config for an app
func (s *recurringPaymentService) getPostHogConfig(appName string) (*posthogModels.PostHogConfig, bool) {
	env := utils.GetEnv("GO_ENV", "local")
	config, err := s.posthogConfigRepo.FindByAppNameAndEnv(appName, env)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fmt.Printf("[PostHog] No config found for app: %s, environment: %s. Skipping event.\n", appName, env)
			sentry.WithScope(func(scope *sentry.Scope) {
				scope.SetTag("app_name", appName)
				sentry.CaptureException(fmt.Errorf("[PostHog] no config found for app %s, env %s", appName, env))
			})
		} else {
			fmt.Printf("[PostHog ERROR] Failed to get config: %v\n", err)
			sentry.WithScope(func(scope *sentry.Scope) {
				scope.SetTag("app_name", appName)
				sentry.CaptureException(fmt.Errorf("[PostHog] failed to get config for app %s: %w", appName, err))
			})
		}
		return nil, false
	}

	if !config.IsActive {
		fmt.Printf("[PostHog] Config is inactive for app: %s. Skipping event.\n", appName)
		sentry.WithScope(func(scope *sentry.Scope) {
			scope.SetTag("app_name", appName)
			sentry.CaptureException(fmt.Errorf("[PostHog] config is inactive for app: %s, env: %s", appName, env))
		})
		return nil, false
	}

	return config, true
}

// extractStateAndLanguageCode extracts state_id and language_code from user metadata for DailyStoryApp
func extractStateAndLanguageCode(user *userModels.User, appName string) (stateID *string, languageCode *string) {
	if appName != constants.AppNameDailyStory || user == nil {
		return nil, nil
	}

	if user.Metadata != nil {
		if stateIDVal, ok := user.Metadata["state_id"].(string); ok && stateIDVal != "" {
			stateID = &stateIDVal
		}
		if languageCodeVal, ok := user.Metadata["language_code"].(string); ok && languageCodeVal != "" {
			languageCode = &languageCodeVal
		}
	}
	return stateID, languageCode
}

// sendPostHogEvent sends a PostHog analytics event
func (s *recurringPaymentService) sendPostHogEvent(
	eventName string,
	userID uuid.UUID,
	appName string,
	amount float64,
	stateID *string,
	languageCode *string,
	errorCode *string,
) {
	config, ok := s.getPostHogConfig(appName)
	if !ok {
		return
	}

	props := analytics.RecurringPaymentEventProperties{
		UserID:       userID,
		AppName:      appName,
		Amount:       amount,
		StateID:      stateID,
		LanguageCode: languageCode,
		ErrorCode:    errorCode,
	}

	go func() {
		if err := s.posthogClient.SendEvent(config.Host, config.APIKey, eventName, userID.String(), props.ToProperties()); err != nil {
			fmt.Printf("[PostHog ERROR] Failed to send %s event: %v\n", eventName, err)
			sentry.WithScope(func(scope *sentry.Scope) {
				scope.SetTag("app_name", appName)
				scope.SetTag("event_name", eventName)
				scope.SetTag("user_id", userID.String())
				sentry.CaptureException(fmt.Errorf("[PostHog] failed to send %s event for user %s: %w", eventName, userID, err))
			})
		}
	}()
}

// sendPostHogRecurringPaymentStartedEvent sends TrialStarted event to PostHog
func (s *recurringPaymentService) sendPostHogRecurringPaymentStartedEvent(
	recurringPayment *models.RecurringPayment,
	paymentAttempt *models.PaymentAttempt,
	user *userModels.User,
) {
	fmt.Printf("[PostHog] Processing TrialStarted event for recurring_payment: %s\n", recurringPayment.ID)

	stateID, languageCode := extractStateAndLanguageCode(user, recurringPayment.AppName)
	amount := float64(paymentAttempt.Amount) / 100.0

	s.sendPostHogEvent(
		PostHogEventTrialStarted,
		recurringPayment.UserID,
		recurringPayment.AppName,
		amount,
		stateID,
		languageCode,
		nil,
	)
}

// sendPostHogRecurringPaymentCapturedEvent sends SubscriptionCharged event to PostHog
func (s *recurringPaymentService) sendPostHogRecurringPaymentCapturedEvent(
	recurringPayment *models.RecurringPayment,
	paymentAttempt *models.PaymentAttempt,
	user *userModels.User,
) {
	fmt.Printf("[PostHog] Processing SubscriptionCharged event for recurring_payment: %s\n", recurringPayment.ID)

	stateID, languageCode := extractStateAndLanguageCode(user, recurringPayment.AppName)
	amount := float64(paymentAttempt.Amount) / 100.0

	s.sendPostHogEvent(
		PostHogEventSubscriptionCharged,
		recurringPayment.UserID,
		recurringPayment.AppName,
		amount,
		stateID,
		languageCode,
		nil,
	)
}

// sendPostHogRecurringPaymentFailedEvent sends RecurringPaymentFailed event to PostHog
func (s *recurringPaymentService) sendPostHogRecurringPaymentFailedEvent(
	recurringPayment *models.RecurringPayment,
	paymentAttempt *models.PaymentAttempt,
	user *userModels.User,
) {
	fmt.Printf("[PostHog] Processing RecurringPaymentFailed event for recurring_payment: %s\n", recurringPayment.ID)

	stateID, languageCode := extractStateAndLanguageCode(user, recurringPayment.AppName)
	amount := float64(paymentAttempt.Amount) / 100.0

	var errorCode *string
	if paymentAttempt.ErrorCode != "" {
		errorCode = &paymentAttempt.ErrorCode
	}

	s.sendPostHogEvent(
		PostHogEventRecurringPaymentFailed,
		recurringPayment.UserID,
		recurringPayment.AppName,
		amount,
		stateID,
		languageCode,
		errorCode,
	)
}

// sendPostHogRecurringPaymentCreationFailedEvent sends RecurringPaymentCreationFailed event to PostHog
func (s *recurringPaymentService) sendPostHogRecurringPaymentCreationFailedEvent(
	recurringPayment *models.RecurringPayment,
	amount int64,
	user *userModels.User,
	errorCode *string,
) {
	fmt.Printf("[PostHog] Processing RecurringPaymentCreationFailed event for recurring_payment: %s\n", recurringPayment.ID)

	stateID, languageCode := extractStateAndLanguageCode(user, recurringPayment.AppName)
	amountInr := float64(amount) / 100.0

	s.sendPostHogEvent(
		PostHogEventRecurringPaymentCreationFailed,
		recurringPayment.UserID,
		recurringPayment.AppName,
		amountInr,
		stateID,
		languageCode,
		errorCode,
	)
}

// sendPostHogRecurringPaymentCancelledEvent sends SubscriptionCancelled event to PostHog
func (s *recurringPaymentService) sendPostHogRecurringPaymentCancelledEvent(
	recurringPayment *models.RecurringPayment,
	amount int64,
	user *userModels.User,
) {
	fmt.Printf("[PostHog] Processing SubscriptionCancelled event for recurring_payment: %s\n", recurringPayment.ID)

	stateID, languageCode := extractStateAndLanguageCode(user, recurringPayment.AppName)
	amountInr := float64(amount) / 100.0

	s.sendPostHogEvent(
		PostHogEventSubscriptionCancelled,
		recurringPayment.UserID,
		recurringPayment.AppName,
		amountInr,
		stateID,
		languageCode,
		nil, // no error code for cancelled event
	)
}

// sendPostHogOrderCreationFailedEvent sends ORDER_CREATION_FAILED event to PostHog
func (s *recurringPaymentService) sendPostHogOrderCreationFailedEvent(
	recurringPayment *models.RecurringPayment,
	amount int64,
	errorCode *string,
	user *userModels.User,
) {
	fmt.Printf("[PostHog] Processing ORDER_CREATION_FAILED event for recurring_payment: %s\n", recurringPayment.ID)

	stateID, languageCode := extractStateAndLanguageCode(user, recurringPayment.AppName)
	amountInr := float64(amount) / 100.0

	config, ok := s.getPostHogConfig(recurringPayment.AppName)
	if !ok {
		return
	}

	props := analytics.OrderCreationFailedProperties{
		UserID:       recurringPayment.UserID,
		AppName:      recurringPayment.AppName,
		Amount:       amountInr,
		StateID:      stateID,
		LanguageCode: languageCode,
		ErrorCode:    errorCode,
	}

	go func() {
		if err := s.posthogClient.SendEvent(config.Host, config.APIKey, PostHogEventOrderCreationFailed, recurringPayment.UserID.String(), props.ToProperties()); err != nil {
			fmt.Printf("[PostHog ERROR] Failed to send OrderCreationFailed event: %v\n", err)
			sentry.WithScope(func(scope *sentry.Scope) {
				scope.SetTag("app_name", recurringPayment.AppName)
				scope.SetTag("recurring_payment_id", recurringPayment.ID.String())
				scope.SetTag("event_name", PostHogEventOrderCreationFailed)
				sentry.CaptureException(fmt.Errorf("[PostHog] failed to send %s event for recurring_payment %s: %w", PostHogEventOrderCreationFailed, recurringPayment.ID, err))
			})
		}
	}()
}
