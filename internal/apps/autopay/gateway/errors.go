package gateway

import (
	"errors"
	"fmt"
	"time"
)

// ErrorKind classifies a gateway error so the core can decide what to do without knowing the gateway.
type ErrorKind string

const (
	// ErrorKindTransient: a network or gateway-side problem and the request was NOT processed (5xx, 429, connection
	// refused, "try again"). Retried with backoff; if it persists the attempt is released, not failed, and does not
	// use up one of the 9 counted attempts.
	ErrorKindTransient ErrorKind = "transient"
	// ErrorKindOutcomeUnknown: the request may or may not have been processed (timeout or reset after the request
	// was sent). The core verifies with the gateway before deciding.
	ErrorKindOutcomeUnknown ErrorKind = "outcome_unknown"
	// ErrorKindAlreadyProcessed: the gateway says the operation was already done (duplicate redeem, paid order).
	ErrorKindAlreadyProcessed ErrorKind = "already_processed"
	// ErrorKindRejectedPermanently: a definite business rejection (bank/customer decline, mandate revoked, limit
	// exceeded). The attempt fails and counts toward the 9.
	ErrorKindRejectedPermanently ErrorKind = "rejected_permanently"
	// ErrorKindRejectedTemporarily: a rejection that is only a timing problem (UPI blackout window, temporary
	// restriction). Retried at the next debit runs up to a small limit, then fails.
	ErrorKindRejectedTemporarily ErrorKind = "rejected_temporarily"
	// ErrorKindMandateInvalid: the mandate/token is not usable (cancelled, expired, not confirmed).
	ErrorKindMandateInvalid ErrorKind = "mandate_invalid"
)

// Error is the error type every adapter returns. Use errors.As to read it.
type Error struct {
	Kind    ErrorKind
	Code    string // the gateway's own error code, when it has one
	Message string
	Cause   error
	// RateLimited marks an HTTP 429: the merchant account exceeded the gateway's request limit. It is a transient
	// error (nothing was processed), but the whole account must slow down, not just this call: see Penalize in the
	// rate limiter. RetryAfter is the wait the gateway asked for (0 when it did not say).
	RateLimited bool
	RetryAfter  time.Duration
}

func (e *Error) Error() string {
	msg := fmt.Sprintf("gateway error (%s)", e.Kind)
	if e.Code != "" {
		msg += " [" + e.Code + "]"
	}
	if e.Message != "" {
		msg += ": " + e.Message
	}
	if e.Cause != nil {
		msg += ": " + e.Cause.Error()
	}
	return msg
}

func (e *Error) Unwrap() error { return e.Cause }

// NewError builds an *Error.
func NewError(kind ErrorKind, code, message string, cause error) *Error {
	return &Error{Kind: kind, Code: code, Message: message, Cause: cause}
}

// NewRateLimitedError builds the transient error for an HTTP 429. retryAfter is what the gateway asked for, 0 if unsaid.
func NewRateLimitedError(code, message string, retryAfter time.Duration, cause error) *Error {
	return &Error{Kind: ErrorKindTransient, Code: code, Message: message, Cause: cause, RateLimited: true, RetryAfter: retryAfter}
}

// RateLimitOf reports whether err is (or wraps) a rate-limit error, and the wait the gateway asked for.
func RateLimitOf(err error) (retryAfter time.Duration, ok bool) {
	var ge *Error
	if errors.As(err, &ge) && ge.RateLimited {
		return ge.RetryAfter, true
	}
	return 0, false
}

// KindOf returns the ErrorKind of err, and false when err is not (or does not wrap) an *Error.
func KindOf(err error) (ErrorKind, bool) {
	var ge *Error
	if errors.As(err, &ge) {
		return ge.Kind, true
	}
	return "", false
}

// IsKind reports whether err is (or wraps) an *Error of the given kind.
func IsKind(err error, kind ErrorKind) bool {
	k, ok := KindOf(err)
	return ok && k == kind
}

// IsRetryable reports whether a call that failed with err should be retried inside the same run: only errors where
// retrying the same operation is safe and useful (transient, or outcome unknown).
func IsRetryable(err error) bool {
	k, ok := KindOf(err)
	return ok && (k == ErrorKindTransient || k == ErrorKindOutcomeUnknown)
}

var (
	// ErrUnsupported is returned for an operation the gateway does not support (see Capabilities).
	ErrUnsupported = errors.New("gateway: operation not supported")
	// ErrInvalidSignature is returned by VerifyAndParseWebhook when the signature does not verify.
	ErrInvalidSignature = errors.New("gateway: invalid webhook signature")
	// ErrUnknownGateway is returned by the Registry for a gateway name nobody registered.
	ErrUnknownGateway = errors.New("gateway: unknown gateway")
)
