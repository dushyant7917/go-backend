// Package phonepe is a thin wrapper around PhonePe Autopay (Standard Checkout) for UPI
// on-demand mandates with merchant-controlled charging: we notify the customer, then redeem
// (debit) at least a day later, and we retry on our own schedule.
//
// PhonePe has no Go SDK, so everything is plain net/http. It is exposed through the Client
// interface so the services can be tested with a fake.
//
// Amounts: PhonePe works in paise on the wire, and so does this package.
// Timestamps: PhonePe uses epoch milliseconds; this package converts to/from time.Time.
package phonepe

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
)

const (
	SandboxBaseURL     = "https://api-preprod.phonepe.com/apis/pg-sandbox"
	ProductionBaseURL  = "https://api.phonepe.com/apis/pg"
	SandboxTokenURL    = "https://api-preprod.phonepe.com/apis/pg-sandbox/v1/oauth/token"
	ProductionTokenURL = "https://api.phonepe.com/apis/identity-manager/v1/oauth/token"
)

// Wire values we rely on.
const (
	// Subscription (mandate) states, from the subscription status API.
	SubscriptionActivationInProgress = "ACTIVATION_IN_PROGRESS"
	SubscriptionActive               = "ACTIVE"
	SubscriptionExpired              = "EXPIRED"
	SubscriptionFailed               = "FAILED"
	SubscriptionCancelInProgress     = "CANCEL_IN_PROGRESS"
	SubscriptionCancelled            = "CANCELLED"
	SubscriptionRevokeInProgress     = "REVOKE_IN_PROGRESS"
	SubscriptionRevoked              = "REVOKED"
	SubscriptionPauseInProgress      = "PAUSE_IN_PROGRESS"
	SubscriptionPaused               = "PAUSED"
	SubscriptionUnpauseInProgress    = "UNPAUSE_IN_PROGRESS"

	// Order / payment states. Setup, one-time and redemption orders are PENDING, COMPLETED or FAILED;
	// notification orders are NOTIFICATION_IN_PROGRESS then NOTIFIED. EXPIRED is reported for orders
	// that ran out their expireAt.
	OrderPending                = "PENDING"
	OrderCompleted              = "COMPLETED"
	OrderFailed                 = "FAILED"
	OrderExpired                = "EXPIRED"
	OrderNotificationInProgress = "NOTIFICATION_IN_PROGRESS"
	OrderNotified               = "NOTIFIED"

	// Notify retry strategies.
	RetryStrategyStandard = "STANDARD"
	RetryStrategyCustom   = "CUSTOM"
)

// MaxMandateAmountPaise is the most PhonePe lets a UPI mandate cap at (₹15,000).
const MaxMandateAmountPaise int64 = 1_500_000

// MinAmountPaise is the smallest amount PhonePe accepts for a setup transaction or a redemption (₹1).
const MinAmountPaise int64 = 100

// Credentials identify a PhonePe merchant account and environment.
type Credentials struct {
	ClientID      string
	ClientSecret  string
	ClientVersion int
	Sandbox       bool
}

// BaseURL returns the PhonePe PG base URL for the credentials' environment.
func (c Credentials) BaseURL() string {
	if c.Sandbox {
		return SandboxBaseURL
	}
	return ProductionBaseURL
}

// TokenURL returns the OAuth token URL. It is not just the base URL plus a path: production uses the
// identity-manager service.
func (c Credentials) TokenURL() string {
	if c.Sandbox {
		return SandboxTokenURL
	}
	return ProductionTokenURL
}

// ClientFactory builds a Client for a set of credentials (one per app config).
type ClientFactory func(creds Credentials) Client

// Client is the PhonePe surface the recurring-payment service needs.
type Client interface {
	// CreateSubscriptionOrder creates the mandate-setup order and returns the SDK token for the app.
	CreateSubscriptionOrder(ctx context.Context, p CreateSubscriptionOrderParams) (*SDKOrder, error)
	// CreateOneTimeOrder creates a one-time PG checkout order and returns the SDK token for the app.
	CreateOneTimeOrder(ctx context.Context, p CreateOneTimeOrderParams) (*SDKOrder, error)
	// FetchOrder returns the order status. It serves setup, notification, redemption and one-time orders.
	FetchOrder(ctx context.Context, merchantOrderID string) (*Order, error)
	FetchSubscription(ctx context.Context, merchantSubscriptionID string) (*Subscription, error)
	// NotifyRedemption sends the pre-debit notice and creates the redemption order.
	NotifyRedemption(ctx context.Context, p NotifyParams) (*NotifyResult, error)
	// Redeem debits a notified order (only valid when the notice was sent with autoDebit=false).
	Redeem(ctx context.Context, merchantOrderID string) (*RedeemResult, error)
	CancelSubscription(ctx context.Context, merchantSubscriptionID string) error
}

// CreateSubscriptionOrderParams creates an ON_DEMAND UPI mandate together with its authorization payment.
type CreateSubscriptionOrderParams struct {
	MerchantOrderID        string
	MerchantSubscriptionID string
	// AmountPaise is charged to the customer at setup (TRANSACTION workflow: min 100).
	AmountPaise int64
	// MaxAmountPaise caps every later debit (max 1,500,000).
	MaxAmountPaise int64
	// SubscriptionExpiry is when the mandate ends.
	SubscriptionExpiry time.Time
	// OrderExpireAfter is how long the customer has to approve (PhonePe allows 5 to 60 minutes).
	OrderExpireAfter time.Duration
	// MetaInfo is echoed back in status calls and webhooks (udf1..udf15).
	MetaInfo map[string]string
}

// CreateOneTimeOrderParams creates a plain one-time checkout order.
type CreateOneTimeOrderParams struct {
	MerchantOrderID  string
	AmountPaise      int64
	OrderExpireAfter time.Duration
	RedirectURL      string
	MetaInfo         map[string]string
}

// SDKOrder is the result of creating an order for the mobile SDK.
type SDKOrder struct {
	OrderID  string
	State    string
	Token    string
	ExpireAt time.Time
}

// Subscription is the subset of a PhonePe subscription we use.
type Subscription struct {
	MerchantSubscriptionID string
	SubscriptionID         string
	State                  string
	AuthWorkflowType       string
	AmountType             string
	MaxAmountPaise         int64
	Frequency              string
	ExpireAt               time.Time
	PauseStartDate         time.Time
	PauseEndDate           time.Time
}

// PaymentAttempt is one payment try inside an order.
type PaymentAttempt struct {
	TransactionID     string
	PaymentMode       string
	State             string
	AmountPaise       int64
	Timestamp         time.Time
	ErrorCode         string
	DetailedErrorCode string
	UTR               string
	UMN               string
}

// Order is the subset of a PhonePe order status we use.
type Order struct {
	OrderID                string
	MerchantOrderID        string
	State                  string
	AmountPaise            int64
	ExpireAt               time.Time
	FlowType               string
	MerchantSubscriptionID string
	SubscriptionID         string
	// Redemption window and notice time, present on notification/redemption orders.
	ValidAfter time.Time
	ValidUpto  time.Time
	NotifiedAt time.Time
	// Order-level failure details (redemption and setup webhooks carry them there).
	ErrorCode         string
	DetailedErrorCode string
	Attempts          []PaymentAttempt
}

// LatestAttempt returns the most recent payment attempt, or nil when there is none.
func (o *Order) LatestAttempt() *PaymentAttempt {
	if o == nil || len(o.Attempts) == 0 {
		return nil
	}
	latest := &o.Attempts[0]
	for i := range o.Attempts {
		if o.Attempts[i].Timestamp.After(latest.Timestamp) {
			latest = &o.Attempts[i]
		}
	}
	return latest
}

// FailureCodes returns the best error code and detail for a failed order: the order's own, else the
// latest attempt's.
func (o *Order) FailureCodes() (code, detail string) {
	if o == nil {
		return "", ""
	}
	code, detail = o.ErrorCode, o.DetailedErrorCode
	if code == "" {
		if a := o.LatestAttempt(); a != nil {
			code, detail = a.ErrorCode, a.DetailedErrorCode
		}
	}
	return code, detail
}

// IsTerminal reports whether the order can no longer change state.
func (o *Order) IsTerminal() bool {
	if o == nil {
		return false
	}
	switch o.State {
	case OrderCompleted, OrderFailed, OrderExpired:
		return true
	}
	return false
}

// NotifyParams raises the pre-debit notice and creates the redemption order for one billing attempt.
type NotifyParams struct {
	MerchantOrderID        string
	MerchantSubscriptionID string
	AmountPaise            int64
	// ExpireAt is when the redemption order dies if it has not reached a terminal state. PhonePe defaults
	// to 48 hours, which is shorter than our notice window, so callers set it explicitly.
	ExpireAt time.Time
	// RetryStrategy defaults to CUSTOM (we do our own retries). AutoDebit is always false for us.
	RetryStrategy string
}

// NotifyResult is the response to a notify call.
type NotifyResult struct {
	OrderID  string
	State    string // NOTIFICATION_IN_PROGRESS (sometimes NOTIFIED)
	ExpireAt time.Time
}

// RedeemResult is the response to a redeem call.
type RedeemResult struct {
	TransactionID string
	State         string // PENDING until the bank answers
}

// APIError is an error response from PhonePe.
type APIError struct {
	HTTPStatus int
	Code       string
	Message    string
	Body       string
	// RetryAfter is the wait a 429/503 response asks for (Retry-After); 0 when it says nothing.
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	return "phonepe: http " + strconv.Itoa(e.HTTPStatus) + " " + e.Code + ": " + e.Message
}

// IsRetryable reports whether retrying the same call later might succeed (rate limit or server error).
func (e *APIError) IsRetryable() bool {
	return e.HTTPStatus == 429 || e.HTTPStatus >= 500
}

// IsDuplicate reports whether PhonePe rejected the call because the merchant order or subscription id was
// already used. PhonePe has no idempotency keys, so callers treat this as "it may already exist" and
// fetch the object instead of failing.
func (e *APIError) IsDuplicate() bool {
	switch strings.ToUpper(e.Code) {
	case "INVALID_TRANSACTION_ID", "DUPLICATE_REQUEST_ID", "ORDER_ALREADY_EXISTS", "DUPLICATE_ORDER", "DUPLICATE_TRANSACTION":
		return true
	}
	return e.HTTPStatus == 409
}

// IsNotFound reports whether PhonePe says the order or subscription does not exist.
func (e *APIError) IsNotFound() bool {
	return e.HTTPStatus == 404 || strings.EqualFold(e.Code, "PAYMENT_NOT_FOUND") || strings.EqualFold(e.Code, "ORDER_NOT_FOUND")
}

// AsAPIError extracts an *APIError from err's chain.
func AsAPIError(err error) (*APIError, bool) {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr, true
	}
	return nil, false
}
