// Package cashfree is a thin wrapper around Cashfree Subscriptions for UPI Autopay
// on-demand mandates with merchant-controlled charging (notify, then execute).
//
// Create/fetch/manage calls go through the official cashfree-pg v6 SDK. The SDK has no
// controlled-flow endpoints (notify-mandate, execute-mandate, fetch notification/execution),
// so those use plain net/http. Everything is exposed through the Client interface so the
// services can be tested with a fake.
//
// Amounts: this package takes and returns paise (int64) and converts to/from the rupee
// floats Cashfree uses on the wire.
package cashfree

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	SandboxBaseURL    = "https://sandbox.cashfree.com/pg"
	ProductionBaseURL = "https://api.cashfree.com/pg"
	APIVersion        = "2026-01-01"
)

// IST is the timezone Cashfree uses for dates.
var IST = time.FixedZone("IST", 5*3600+30*60)

// Credentials identify a Cashfree merchant account and environment.
type Credentials struct {
	ClientID     string
	ClientSecret string
	Sandbox      bool
}

// BaseURL returns the Cashfree PG base URL for the credentials' environment.
func (c Credentials) BaseURL() string {
	if c.Sandbox {
		return SandboxBaseURL
	}
	return ProductionBaseURL
}

// ClientFactory builds a Client for a set of credentials (one per app config).
type ClientFactory func(creds Credentials) Client

// Client is the Cashfree surface the recurring-payment service needs.
type Client interface {
	CreateSubscription(ctx context.Context, p CreateSubscriptionParams) (*Subscription, error)
	FetchSubscription(ctx context.Context, subscriptionID string) (*Subscription, error)
	// ManageSubscription performs CANCEL, PAUSE or ACTIVATE.
	ManageSubscription(ctx context.Context, subscriptionID, action string) (*Subscription, error)
	FetchPayment(ctx context.Context, subscriptionID, paymentID string) (*Payment, error)

	// One-time payments: a PG order paid through the SDK checkout (payment_session_id), or a hosted payment link.
	CreateOrder(ctx context.Context, p CreateOrderParams) (*Order, error)
	FetchOrder(ctx context.Context, orderID string) (*Order, error)
	CreatePaymentLink(ctx context.Context, p CreatePaymentLinkParams) (*PaymentLink, error)
	FetchPaymentLink(ctx context.Context, linkID string) (*PaymentLink, error)

	// Merchant-controlled flow (UPI only).
	NotifyMandate(ctx context.Context, p NotifyParams) (*NotifyResult, error)
	ExecuteMandate(ctx context.Context, p ExecuteParams) (*ExecuteResult, error)
	FetchNotification(ctx context.Context, paymentID, notificationID string) (*Notification, error)
	FetchExecution(ctx context.Context, paymentID, executionID string) (*Execution, error)
}

// CreateSubscriptionParams creates an ON_DEMAND UPI subscription (mandate).
type CreateSubscriptionParams struct {
	SubscriptionID string
	PlanName       string
	// MaxAmountPaise is the maximum chargeable amount per debit (plan_max_amount).
	MaxAmountPaise int64
	// AuthorizationAmountPaise is charged at mandate setup (UPI minimum is 100 paise).
	AuthorizationAmountPaise  int64
	RefundAuthorizationAmount bool
	CustomerName              string
	CustomerEmail             string
	CustomerPhone             string
	ExpiryTime                time.Time
	ReturnURL                 string
	IdempotencyKey            string
}

// Subscription is the subset of a Cashfree subscription we use.
type Subscription struct {
	SubscriptionID   string
	CFSubscriptionID string
	SessionID        string
	Status           string // INITIALIZED, BANK_APPROVAL_PENDING, ACTIVE, ON_HOLD, ...
	AuthStatus       string // authorization_status: INITIALIZED, PENDING, SUCCESS/ACTIVE, FAILED
	AuthPaymentID    string
	AuthReference    string // UMN for UPI
}

// Payment is a subscription payment (AUTH or CHARGE).
type Payment struct {
	PaymentID     string
	CFPaymentID   string
	Type          string
	Status        string // INITIALIZED, PENDING, SUCCESS, FAILED, CANCELLED
	FailureReason string
	RetryAttempts int
}

// NotifyParams raises a pre-debit notification (PDN) for a payment.
type NotifyParams struct {
	SubscriptionID string
	PaymentID      string
	NotificationID string
	AmountPaise    int64
	// ScheduleDate is the intended debit date (IST). Only the date part is used by Cashfree
	// and it must be within T+1..T+7.
	ScheduleDate   time.Time
	Remarks        string
	IdempotencyKey string
}

// NotifyResult is the synchronous response of notify-mandate; the outcome arrives via webhook/fetch.
type NotifyResult struct {
	CFNotificationID string
	CFPaymentID      string
	NotificationID   string
	Status           string
	PaymentStatus    string
}

// ExecuteParams triggers the debit for a previously notified payment.
type ExecuteParams struct {
	PaymentID      string
	ExecutionID    string
	IdempotencyKey string
}

// ExecuteResult is the synchronous response of execute-mandate.
type ExecuteResult struct {
	CFExecutionID string
	ExecutionID   string
	Status        string
	PaymentStatus string
}

// FailureDetails carries Cashfree's failure info for notifications and executions.
type FailureDetails struct {
	Reason       string
	ErrorCode    string
	SubErrorCode string
}

// Notification is the state of a controlled notification.
type Notification struct {
	NotificationID   string
	CFNotificationID string
	CFPaymentID      string
	Status           string // SUCCESS, FAILED, ... (full enum undocumented)
	PaymentStatus    string
	CompletedTime    *time.Time
	Failure          FailureDetails
}

// Execution is the state of a controlled execution.
type Execution struct {
	ExecutionID   string
	CFExecutionID string
	CFPaymentID   string
	Status        string
	PaymentStatus string
	CompletedTime *time.Time
	Failure       FailureDetails
}

// CreateOrderParams creates a PG order for a one-time payment.
type CreateOrderParams struct {
	OrderID       string
	AmountPaise   int64
	CustomerID    string
	CustomerName  string
	CustomerEmail string
	CustomerPhone string
	ExpiryTime    time.Time
	Note          string
	ReturnURL     string
	// IdempotencyKey is mapped to a UUID header; retries with the same key do not create a second order.
	IdempotencyKey string
}

// Order is the subset of a PG order we use. Status is ACTIVE, PAID, EXPIRED or TERMINATED.
type Order struct {
	OrderID     string
	CFOrderID   string
	SessionID   string
	Status      string
	AmountPaise int64
}

// CreatePaymentLinkParams creates a hosted payment link for a one-time payment.
type CreatePaymentLinkParams struct {
	LinkID         string
	AmountPaise    int64
	Purpose        string
	CustomerName   string
	CustomerEmail  string
	CustomerPhone  string
	ExpiryTime     time.Time
	ReturnURL      string
	IdempotencyKey string
}

// PaymentLink is the subset of a payment link we use. Status is ACTIVE, PAID, PARTIALLY_PAID, EXPIRED or CANCELLED.
type PaymentLink struct {
	LinkID          string
	CFLinkID        string
	URL             string
	Status          string
	AmountPaise     int64
	AmountPaidPaise int64
}

// Order and payment-link statuses we branch on.
const (
	OrderStatusPaid = "PAID"
	LinkStatusPaid  = "PAID"
)

// APIError is a non-2xx response from Cashfree.
type APIError struct {
	HTTPStatus int
	Code       string
	Type       string
	Message    string
	Body       string
	// RetryAfter is how long Cashfree asks the client to wait (x-ratelimit-retry / Retry-After on a 429); 0 when unsaid.
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	return fmt.Sprintf("cashfree api error (http %d, code %q, type %q): %s", e.HTTPStatus, e.Code, e.Type, e.Message)
}

// IsRetryable reports whether the same request may succeed if repeated later
// (transport-level problems are handled by the caller; this covers API responses).
func (e *APIError) IsRetryable() bool {
	return e.HTTPStatus == 429 || e.HTTPStatus >= 500
}

// IsInProgress reports Cashfree's "previous PDN/execution in progress" rejections. It keys off the
// documented error codes only, never the message text.
func (e *APIError) IsInProgress() bool {
	switch strings.ToLower(e.Code) {
	case "prev_pdn_in_progress", "prev_execution_in_progress":
		return true
	}
	return false
}

// AsAPIError extracts an *APIError from err, if any.
func AsAPIError(err error) (*APIError, bool) {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr, true
	}
	return nil, false
}

// Controlled-flow status constants. Cashfree does not document the full enum; these are the
// values seen in docs and webhook samples.
const (
	StatusSuccess   = "SUCCESS"
	StatusFailed    = "FAILED"
	StatusCancelled = "CANCELLED"
)

// PaiseToRupees converts paise to the rupee float Cashfree expects.
func PaiseToRupees(paise int64) float64 {
	return float64(paise) / 100
}

// RupeesToPaise converts a Cashfree rupee amount to paise, rounding to the nearest paisa.
func RupeesToPaise(rupees float64) int64 {
	if rupees < 0 {
		return -int64(-rupees*100 + 0.5)
	}
	return int64(rupees*100 + 0.5)
}

// ParseTime parses Cashfree's ISO-8601 timestamps. Values without an offset are IST.
func ParseTime(s string) *time.Time {
	if s == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"} {
		var (
			t   time.Time
			err error
		)
		if layout == time.RFC3339 {
			t, err = time.Parse(layout, s)
		} else {
			t, err = time.ParseInLocation(layout, s, IST)
		}
		if err == nil {
			return &t
		}
	}
	return nil
}

// idempotencyNamespace is the namespace for deriving UUIDv5 idempotency keys (the RFC 4122 URL namespace).
var idempotencyNamespace = uuid.MustParse("6ba7b811-9dad-11d1-80b4-00c04fd430c8")

// idempotencyHeader returns the value for Cashfree's x-idempotency-key header, which the docs specify as a UUID.
// Our natural keys (subscription id, notification id, ...) are not UUIDs, so they are mapped to a UUIDv5 of the
// key: the same key always yields the same header (retries still dedupe) and different keys never collide.
func idempotencyHeader(key string) string {
	if key == "" {
		return ""
	}
	if _, err := uuid.Parse(key); err == nil {
		return key
	}
	return uuid.NewSHA1(idempotencyNamespace, []byte(key)).String()
}
