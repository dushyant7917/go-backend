package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"go-backend/internal/apps/autopay/gateway"
	"go-backend/internal/apps/autopay/models"
	"go-backend/internal/apps/autopay/ratelimit"
	userModels "go-backend/internal/apps/user/models"
	"go-backend/pkg/utils"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

var currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)

// CreateRequest creates a recurring mandate or a one-time payment. Gateway is chosen by the client and stored once
// (as the config of gateway + app + environment).
type CreateRequest struct {
	UserID      uuid.UUID
	AppName     string
	Gateway     string
	PaymentType models.PaymentType
	Currency    string // defaults to INR

	// recurring
	AuthorizationAmount int64 // charged at setup (minor units)
	MaxDebitAmount      int64 // cap per later debit (minor units)
	// start of the first billing cycle: required for recurring, ignored for one-time (which always starts now)
	StartAt          time.Time
	BillingFrequency string // daily ... yearly; for one-time it is the length of the access window

	// one-time
	Amount      int64
	OneTimeKind string // gateway.OneTimePaymentKindOrder | gateway.OneTimePaymentKindPaymentLink; defaults to order

	ReturnURL string
}

// CreateResult is what the client app needs to complete the payment.
type CreateResult struct {
	AutopayMandateID uuid.UUID
	Gateway          string
	PaymentType      models.PaymentType
	Amount           int64 // charged now (authorization amount, or the one-time amount)
	Currency         string
	ClientPayload    map[string]any // opaque, gateway-specific (key id, session id, order id + token, ...)
	LinkURL          string         // one-time payment link
	Mode             string         // sandbox | production
}

// MandateView is the public view of an autopay_mandates row.
type MandateView struct {
	ID                     uuid.UUID            `json:"autopay_mandate_id"`
	UserID                 uuid.UUID            `json:"user_id"`
	AppName                string               `json:"app_name"`
	Gateway                string               `json:"gateway"`
	PaymentType            models.PaymentType   `json:"payment_type"`
	Status                 models.MandateStatus `json:"status"`
	MaxDebitAmount         int64                `json:"max_debit_amount"`
	Currency               string               `json:"currency"`
	BillingFrequency       string               `json:"billing_frequency"`
	StartAt                *time.Time           `json:"start_at,omitempty"`
	EndAt                  *time.Time           `json:"end_at,omitempty"`
	LastSuccessfulChargeAt *time.Time           `json:"last_successful_charge_at,omitempty"`
	NextChargeDueAt        *time.Time           `json:"next_charge_due_at,omitempty"`
	CreatedAt              time.Time            `json:"created_at"`
	UpdatedAt              time.Time            `json:"updated_at"`
}

// StatusView is the subscription status of a user in an app.
type StatusView struct {
	ActiveSubscription bool `json:"active_subscription"` // has a billing cycle covering now
	UsedFreeTrial      bool `json:"used_free_trial"`     // ever completed an authorization
}

// Create creates the autopay at the gateway and the local rows (mandate, cycle 0, attempt).
func (s *Service) Create(ctx context.Context, req CreateRequest) (*CreateResult, error) {
	if req.Currency == "" {
		req.Currency = "INR"
	}
	if err := s.validateCreate(req); err != nil {
		return nil, err
	}
	user, err := s.users.FindByID(req.UserID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: user %s", ErrNotFound, req.UserID)
		}
		return nil, fmt.Errorf("load user: %w", err)
	}

	cfg, err := s.repo.FindActiveConfig(req.Gateway, req.AppName, s.env)
	if err != nil || !cfg.AcceptsNewAutopay {
		return nil, fmt.Errorf("%w: no gateway %q accepting new autopays for app %q", ErrGatewayNotAvailable, req.Gateway, req.AppName)
	}
	rg, err := s.gateways.Resolve(ctx, cfg.ID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrGatewayNotAvailable, err)
	}

	if req.PaymentType == models.PaymentTypeOneTime {
		return s.createOneTime(ctx, rg, user, req)
	}
	return s.createRecurring(ctx, rg, user, req)
}

func (s *Service) validateCreate(req CreateRequest) error {
	switch {
	case req.UserID == uuid.Nil:
		return fmt.Errorf("%w: user_id is required", ErrInvalidRequest)
	case req.AppName == "":
		return fmt.Errorf("%w: app_name is required", ErrInvalidRequest)
	case !s.gateways.Registered(gateway.Name(req.Gateway)):
		return fmt.Errorf("%w: unknown gateway %q", ErrInvalidRequest, req.Gateway)
	case req.PaymentType != models.PaymentTypeRecurring && req.PaymentType != models.PaymentTypeOneTime:
		return fmt.Errorf("%w: payment_type must be %q or %q", ErrInvalidRequest, models.PaymentTypeRecurring, models.PaymentTypeOneTime)
	case !currencyPattern.MatchString(req.Currency):
		return fmt.Errorf("%w: currency must be a 3-letter ISO 4217 code", ErrInvalidRequest)
	}
	// A one-time payment has no start_at (it starts when it is created); a recurring mandate must say when cycle 1 starts.
	base := s.now()
	if req.PaymentType == models.PaymentTypeRecurring {
		if req.StartAt.IsZero() {
			return fmt.Errorf("%w: start_at is required for recurring", ErrInvalidRequest)
		}
		base = req.StartAt
	}
	if _, err := NextChargeDueAt(req.BillingFrequency, base); err != nil {
		return fmt.Errorf("%w: billing_frequency: %v", ErrInvalidRequest, err)
	}
	return nil
}

func (s *Service) createRecurring(ctx context.Context, rg *ResolvedGateway, user *userModels.User, req CreateRequest) (*CreateResult, error) {
	caps := rg.Gateway.Capabilities()
	switch {
	case req.AuthorizationAmount < caps.MinAmountMinorUnits:
		return nil, fmt.Errorf("%w: authorization_amount must be at least %d", ErrInvalidRequest, caps.MinAmountMinorUnits)
	case req.MaxDebitAmount < req.AuthorizationAmount:
		return nil, fmt.Errorf("%w: max_debit_amount (%d) cannot be less than authorization_amount (%d)", ErrInvalidRequest, req.MaxDebitAmount, req.AuthorizationAmount)
	case caps.MaxMandateAmountMinorUnits > 0 && req.MaxDebitAmount > caps.MaxMandateAmountMinorUnits:
		return nil, fmt.Errorf("%w: max_debit_amount cannot exceed %d for %s", ErrInvalidRequest, caps.MaxMandateAmountMinorUnits, req.Gateway)
	case req.StartAt.Before(s.now().Add(minAuthorizationLeadTime)):
		return nil, fmt.Errorf("%w: start_at must be at least %.0f hours in the future", ErrInvalidRequest, minAuthorizationLeadTime.Hours())
	}

	mandateID := uuid.New()
	merchantID := NewMerchantAutopayID()
	endAt := req.StartAt.AddDate(mandateTenureYears, 0, 0).UTC() // the mandate expires mandateTenureYears after the first cycle

	session, err := Call(ctx, s.policyFor(rg, ratelimit.Interactive), func(ctx context.Context) (*gateway.MandateAuthorizationSession, error) {
		return rg.Gateway.CreateMandateAuthorization(ctx, gateway.MandateAuthorizationRequest{
			MerchantAutopayID: merchantID, MerchantPaymentID: AuthorizationMerchantPaymentID(mandateID), User: userInfo(user),
			AuthorizationAmount: req.AuthorizationAmount, MaxDebitAmount: req.MaxDebitAmount, Currency: req.Currency,
			EndAt: endAt, BillingFrequency: req.BillingFrequency, ReturnURL: req.ReturnURL,
		})
	})
	if err != nil {
		return nil, mapGatewayError("create mandate", err)
	}

	now := s.now()
	start := req.StartAt.UTC()
	mandate := &models.AutopayMandate{
		ID: mandateID, UserID: req.UserID, AppName: req.AppName, GatewayConfigID: rg.Config.ID, PaymentType: models.PaymentTypeRecurring,
		MerchantAutopayID: merchantID, GatewayMandateID: strPtr(session.GatewayMandateID), GatewayCustomerID: strPtr(session.GatewayCustomerID),
		Status: models.MandateStatusAwaitingAuthorization, MaxDebitAmount: req.MaxDebitAmount, Currency: req.Currency,
		BillingFrequency: req.BillingFrequency, StartAt: &start, EndAt: &endAt, Metadata: utils.Metadata{},
	}
	cycle := &models.AutopayBillingCycle{
		ID: uuid.New(), AutopayMandateID: mandateID, CycleNumber: 0, StartAt: now, EndAt: &start, Amount: req.AuthorizationAmount, // cycle 0 runs until cycle 1 starts
		Status: models.BillingCycleStatusAwaitingPayment, Metadata: utils.Metadata{},
	}
	attempt := &models.AutopayChargeAttempt{
		ID: uuid.New(), BillingCycleID: cycle.ID, GatewayConfigID: rg.Config.ID, AttemptNumber: 1, CountsTowardAttemptLimit: true,
		MerchantPaymentID: AuthorizationMerchantPaymentID(mandateID), GatewayOrderID: strPtr(session.GatewayOrderID),
		Status: models.ChargeAttemptStatusDebitSubmitted, // the authorization payment is in the customer's hands
		Amount: req.AuthorizationAmount, Metadata: utils.Metadata{},
	}
	if err := s.persistNew(mandate, cycle, attempt); err != nil {
		return nil, err
	}
	return &CreateResult{
		AutopayMandateID: mandateID, Gateway: req.Gateway, PaymentType: models.PaymentTypeRecurring,
		Amount: req.AuthorizationAmount, Currency: req.Currency, ClientPayload: session.ClientPayload, Mode: gateway.ModeOf(rg.Config.Environment),
	}, nil
}

func (s *Service) createOneTime(ctx context.Context, rg *ResolvedGateway, user *userModels.User, req CreateRequest) (*CreateResult, error) {
	caps := rg.Gateway.Capabilities()
	if req.Amount < caps.MinAmountMinorUnits {
		return nil, fmt.Errorf("%w: amount must be at least %d", ErrInvalidRequest, caps.MinAmountMinorUnits)
	}
	kind := req.OneTimeKind
	if kind == "" {
		kind = gateway.OneTimePaymentKindOrder
	}
	if !caps.SupportsOneTimePaymentKind(kind) {
		return nil, fmt.Errorf("%w: %s does not support one-time %s", ErrInvalidRequest, req.Gateway, kind)
	}

	mandateID, cycleID := uuid.New(), uuid.New()
	merchantID := NewMerchantAutopayID()
	merchantPaymentID := MerchantPaymentIDFor(cycleID, 1, 0)
	start := s.now().UTC()                                 // always now: any start_at the client sent is ignored
	end, _ := NextChargeDueAt(req.BillingFrequency, start) // validated above

	session, err := Call(ctx, s.policyFor(rg, ratelimit.Interactive), func(ctx context.Context) (*gateway.OneTimePaymentSession, error) {
		return rg.Gateway.CreateOneTimePayment(ctx, gateway.OneTimePaymentRequest{
			MerchantAutopayID: merchantID, MerchantPaymentID: merchantPaymentID, User: userInfo(user),
			Amount: req.Amount, Currency: req.Currency, Kind: kind, ReturnURL: req.ReturnURL,
		})
	})
	if err != nil {
		return nil, mapGatewayError("create one-time payment", err)
	}

	mandate := &models.AutopayMandate{
		ID: mandateID, UserID: req.UserID, AppName: req.AppName, GatewayConfigID: rg.Config.ID, PaymentType: models.PaymentTypeOneTime,
		MerchantAutopayID: merchantID, Status: models.MandateStatusAwaitingAuthorization, MaxDebitAmount: req.Amount, Currency: req.Currency,
		BillingFrequency: req.BillingFrequency, StartAt: &start, EndAt: &end, Metadata: utils.Metadata{"one_time_kind": kind},
	}
	cycle := &models.AutopayBillingCycle{
		ID: cycleID, AutopayMandateID: mandateID, CycleNumber: 0, StartAt: start, EndAt: &end, Amount: req.Amount,
		Status: models.BillingCycleStatusAwaitingPayment, Metadata: utils.Metadata{},
	}
	attempt := &models.AutopayChargeAttempt{
		ID: uuid.New(), BillingCycleID: cycleID, GatewayConfigID: rg.Config.ID, AttemptNumber: 1, CountsTowardAttemptLimit: true,
		MerchantPaymentID: merchantPaymentID, GatewayOrderID: strPtr(session.GatewayOrderID), GatewayPaymentLinkID: strPtr(session.GatewayPaymentLinkID),
		Status: models.ChargeAttemptStatusAwaitingNotification, Amount: req.Amount, Metadata: utils.Metadata{},
	}
	if err := s.persistNew(mandate, cycle, attempt); err != nil {
		return nil, err
	}
	return &CreateResult{
		AutopayMandateID: mandateID, Gateway: req.Gateway, PaymentType: models.PaymentTypeOneTime, Amount: req.Amount, Currency: req.Currency,
		ClientPayload: session.ClientPayload, LinkURL: session.LinkURL, Mode: gateway.ModeOf(rg.Config.Environment),
	}, nil
}

// persistNew stores a new mandate with its cycle and attempt atomically.
func (s *Service) persistNew(m *models.AutopayMandate, c *models.AutopayBillingCycle, a *models.AutopayChargeAttempt) error {
	return s.inTx(func(tx *gorm.DB) error {
		if err := s.repo.CreateMandate(tx, m); err != nil {
			return fmt.Errorf("create autopay mandate: %w", err)
		}
		if err := s.repo.CreateBillingCycle(tx, c); err != nil {
			return fmt.Errorf("create billing cycle: %w", err)
		}
		if err := s.repo.CreateChargeAttempt(tx, a); err != nil {
			return fmt.Errorf("create charge attempt: %w", err)
		}
		return nil
	})
}

// mapGatewayError turns an adapter error into one the HTTP layer understands.
func mapGatewayError(what string, err error) error {
	if kind, ok := gateway.KindOf(err); ok {
		switch kind {
		case gateway.ErrorKindRejectedPermanently, gateway.ErrorKindRejectedTemporarily, gateway.ErrorKindMandateInvalid:
			return fmt.Errorf("%w: %s: %v", ErrInvalidRequest, what, err)
		}
	}
	return fmt.Errorf("%w: %s: %v", ErrGatewayUnavailable, what, err)
}

// ==================== Verify / cancel / read ====================

// Verify confirms the authorization (recurring) or the payment (one-time) with the gateway after the client reports
// completion; the client callback itself is unsigned and never trusted.
func (s *Service) Verify(ctx context.Context, id uuid.UUID) (*MandateView, error) {
	m, err := s.findMandate(id)
	if err != nil {
		return nil, err
	}
	if m.Status != models.MandateStatusAwaitingAuthorization {
		return s.view(m) // already settled: idempotent
	}
	rg, err := s.gateways.Resolve(ctx, m.GatewayConfigID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrGatewayNotAvailable, err)
	}
	attempt, err := s.repo.FindAuthorizationChargeAttempt(m.ID)
	if err != nil {
		return nil, fmt.Errorf("find authorization attempt: %w", err)
	}
	if err := s.syncAuthorizationOrPayment(ctx, rg, m, attempt, ratelimit.Interactive); err != nil {
		return nil, mapGatewayError("verify", err)
	}
	return s.Get(id)
}

// syncAuthorizationOrPayment fetches the authorization (recurring) or the payment (one-time) and applies it.
func (s *Service) syncAuthorizationOrPayment(ctx context.Context, rg *ResolvedGateway, m *models.AutopayMandate, attempt *models.AutopayChargeAttempt, class ratelimit.Class) error {
	if m.PaymentType == models.PaymentTypeOneTime {
		ref := gateway.OneTimePaymentRef{
			MerchantAutopayID: m.MerchantAutopayID, MerchantPaymentID: attempt.MerchantPaymentID, Kind: oneTimeKind(m),
			GatewayOrderID: deref(attempt.GatewayOrderID), GatewayPaymentLinkID: deref(attempt.GatewayPaymentLinkID), GatewayPaymentID: deref(attempt.GatewayPaymentID),
		}
		st, err := Call(ctx, s.policyFor(rg, class), func(ctx context.Context) (*gateway.OneTimePaymentStatus, error) {
			return rg.Gateway.FetchOneTimePaymentStatus(ctx, ref)
		})
		if err != nil {
			return err
		}
		return s.applyOneTimeStatus(attempt.ID, st)
	}
	st, err := Call(ctx, s.policyFor(rg, class), func(ctx context.Context) (*gateway.MandateAuthorizationStatus, error) {
		ref := mandateRef(m)
		ref.GatewayOrderID = deref(attempt.GatewayOrderID) // the authorization order (Razorpay finds the token through its payment)
		return rg.Gateway.FetchMandateAuthorizationStatus(ctx, ref)
	})
	if err != nil {
		return err
	}
	return s.applyAuthorizationStatus(attempt.ID, st)
}

func (s *Service) applyAuthorizationStatus(attemptID uuid.UUID, st *gateway.MandateAuthorizationStatus) error {
	switch st.State {
	case gateway.AuthorizationSucceeded:
		return s.mutateAttempt(attemptID, func(state State) Effects {
			fx := s.engine.OnMandateAuthorizationSucceeded(state, st.GatewayMandateID, st.GatewayPaymentID)
			if st.GatewayCustomerID != "" && state.Mandate.GatewayCustomerID == nil {
				state.Mandate.GatewayCustomerID = strPtr(st.GatewayCustomerID)
				fx.Changed = true
			}
			return fx
		})
	case gateway.AuthorizationFailed:
		return s.mutateAttempt(attemptID, func(state State) Effects {
			return s.engine.OnChargeFailed(state, permanentFailure(st.FailureCode, st.FailureDescription))
		})
	}
	return nil // pending: nothing to apply yet
}

func (s *Service) applyOneTimeStatus(attemptID uuid.UUID, st *gateway.OneTimePaymentStatus) error {
	switch st.State {
	case gateway.PaymentSucceeded:
		return s.mutateAttempt(attemptID, func(state State) Effects {
			if st.GatewayPaymentID != "" {
				state.Attempt.GatewayPaymentID = strPtr(st.GatewayPaymentID)
			}
			fx := s.engine.OnOneTimePaymentSucceeded(state)
			return fx
		})
	case gateway.PaymentFailed:
		return s.mutateAttempt(attemptID, func(state State) Effects {
			return s.engine.OnChargeFailed(state, permanentFailure(st.FailureCode, st.FailureDescription))
		})
	}
	return nil
}

// Cancel cancels a recurring mandate at the gateway (when it supports it) and locally.
func (s *Service) Cancel(ctx context.Context, id uuid.UUID) (*MandateView, error) {
	m, err := s.findMandate(id)
	if err != nil {
		return nil, err
	}
	if m.PaymentType != models.PaymentTypeRecurring {
		return nil, fmt.Errorf("%w: only recurring mandates can be cancelled", ErrInvalidRequest)
	}
	if m.Status == models.MandateStatusCancelled || m.Status == models.MandateStatusExpired {
		return s.view(m)
	}
	rg, err := s.gateways.Resolve(ctx, m.GatewayConfigID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrGatewayNotAvailable, err)
	}
	if rg.Gateway.Capabilities().SupportsMandateCancellation && m.GatewayMandateID != nil {
		_, err := Call(ctx, s.policyFor(rg, ratelimit.Interactive), func(ctx context.Context) (struct{}, error) {
			return struct{}{}, rg.Gateway.CancelMandate(ctx, mandateRef(m))
		})
		if err != nil && !gateway.IsKind(err, gateway.ErrorKindAlreadyProcessed) {
			return nil, mapGatewayError("cancel mandate", err)
		}
	}
	if err := s.mutateMandate(id, func(mm *models.AutopayMandate, latest *models.AutopayBillingCycle) Effects {
		return s.engine.OnMandateStatusChanged(mm, latest, gateway.MandateCancelled)
	}); err != nil {
		return nil, err
	}
	return s.Get(id)
}

// Get returns the public view of one autopay.
func (s *Service) Get(id uuid.UUID) (*MandateView, error) {
	m, err := s.findMandate(id)
	if err != nil {
		return nil, err
	}
	return s.view(m)
}

// Status answers whether a user has an active subscription and has used the free trial in this system.
func (s *Service) Status(userID uuid.UUID, appName string) (*StatusView, error) {
	now := s.now()
	active, err := s.repo.IsSubscriptionActive(userID, appName, now)
	if err != nil {
		return nil, err
	}
	used, err := s.repo.HasCompletedAuthorizationPayment(userID, appName)
	if err != nil {
		return nil, err
	}
	return &StatusView{ActiveSubscription: active, UsedFreeTrial: used}, nil
}

func (s *Service) findMandate(id uuid.UUID) (*models.AutopayMandate, error) {
	m, err := s.repo.FindMandateByID(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("%w: autopay %s", ErrNotFound, id)
		}
		return nil, err
	}
	return m, nil
}

func (s *Service) view(m *models.AutopayMandate) (*MandateView, error) {
	cfg, err := s.repo.FindConfigByID(m.GatewayConfigID)
	if err != nil {
		return nil, fmt.Errorf("load gateway config: %w", err)
	}
	return &MandateView{
		ID: m.ID, UserID: m.UserID, AppName: m.AppName, Gateway: cfg.Gateway, PaymentType: m.PaymentType, Status: m.Status,
		MaxDebitAmount: m.MaxDebitAmount, Currency: m.Currency, BillingFrequency: m.BillingFrequency, StartAt: m.StartAt, EndAt: m.EndAt,
		LastSuccessfulChargeAt: m.LastSuccessfulChargeAt, NextChargeDueAt: m.NextChargeDueAt, CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt,
	}, nil
}

// oneTimeKind returns the kind (order | payment_link) a one-time payment was created with.
func oneTimeKind(m *models.AutopayMandate) string {
	if k, ok := m.Metadata["one_time_kind"].(string); ok {
		return k
	}
	return gateway.OneTimePaymentKindOrder
}
