// Package gateway is the contract between the autopay core and the payment gateways (Razorpay, Cashfree,
// PhonePe, ...). The core never imports a gateway package: it talks to a Gateway obtained from a Registry,
// and each adapter translates between this contract and one gateway's API.
//
// Conventions every adapter must follow:
//   - Amounts are integers in the currency's minor unit (paise for INR). Adapters convert rupee floats etc.
//   - Every time.Time that crosses this boundary is UTC. Adapters convert gateway times (IST offsets, IST date
//     strings, epoch seconds/milliseconds) with .UTC() before returning them.
//   - Errors are returned as *Error so the core can decide whether to retry, verify or fail (see errors.go).
package gateway

import (
	"context"
	"net/http"
	"time"
)

// Gateway is one payment gateway, bound to one merchant account (one Config).
type Gateway interface {
	// Name is the gateway identifier, e.g. "razorpay".
	Name() Name
	Capabilities() Capabilities

	// CreateMandateAuthorization creates the mandate and returns what the client app needs to authorize it.
	CreateMandateAuthorization(ctx context.Context, req MandateAuthorizationRequest) (*MandateAuthorizationSession, error)
	// FetchMandateAuthorizationStatus asks the gateway whether the customer has authorized the mandate.
	FetchMandateAuthorizationStatus(ctx context.Context, ref MandateRef) (*MandateAuthorizationStatus, error)
	// FetchMandateStatus returns the mandate's current state (active, paused, cancelled, expired).
	FetchMandateStatus(ctx context.Context, ref MandateRef) (*MandateStatus, error)
	// CancelMandate cancels the mandate. Returns ErrUnsupported when !Capabilities().SupportsMandateCancellation.
	CancelMandate(ctx context.Context, ref MandateRef) error

	// SendPreDebitNotification sends the pre-debit notification for a charge attempt. For gateways whose notification
	// is a side effect of creating the order (Razorpay) this creates that order.
	SendPreDebitNotification(ctx context.Context, req PreDebitNotificationRequest) (*PreDebitNotificationResult, error)
	// FetchPreDebitNotificationStatus reports whether, and when, the notification was delivered.
	FetchPreDebitNotificationStatus(ctx context.Context, ref ChargeAttemptRef) (*PreDebitNotificationStatus, error)

	// SubmitDebit submits the debit for a notified charge attempt. A successful return only means the debit was
	// submitted: the bank result is asynchronous (webhook / FetchChargeAttemptStatus).
	SubmitDebit(ctx context.Context, req DebitRequest) (*DebitSubmission, error)
	// FetchChargeAttemptStatus is the source of truth for a charge attempt (used by sync and by webhook exceptions).
	FetchChargeAttemptStatus(ctx context.Context, ref ChargeAttemptRef) (*ChargeAttemptStatus, error)

	// CreateOneTimePayment creates an order or payment link for a one-time payment.
	CreateOneTimePayment(ctx context.Context, req OneTimePaymentRequest) (*OneTimePaymentSession, error)
	FetchOneTimePaymentStatus(ctx context.Context, ref OneTimePaymentRef) (*OneTimePaymentStatus, error)

	// VerifyAndParseWebhook verifies the signature over rawBody with the config's webhook secret and normalizes the
	// payload into Events. It returns ErrInvalidSignature when verification fails. It must not trust any body field
	// before the signature verifies, and must emit a failure event only when the payload itself says the entity
	// reached a terminal failed state.
	VerifyAndParseWebhook(headers http.Header, rawBody []byte) ([]Event, error)
}

// Config is one decrypted payment_gateway_configs row: the credentials of one merchant account (one app).
type Config struct {
	ID          string // payment_gateway_configs.id (UUID string)
	Gateway     Name
	AppName     string
	Environment string
	// Credentials holds the gateway-specific credentials, already decrypted
	// (razorpay: key_id, key_secret; cashfree: client_id, client_secret; phonepe: client_id, client_secret, client_version, merchant_id).
	Credentials   map[string]string
	WebhookSecret string // decrypted
	// RequestsPerMinuteLimit overrides Capabilities.RequestsPerMinuteLimit when > 0.
	RequestsPerMinuteLimit int
}

// ProductionEnvironment is the Config.Environment value that selects the gateway's production API; anything else
// selects the sandbox.
const ProductionEnvironment = "prod"

// IsSandbox reports whether this config talks to the gateway's sandbox.
func (c Config) IsSandbox() bool { return c.Environment != ProductionEnvironment }

// Mode is "sandbox" or "production": what the client app is told so it starts the gateway SDK in the right environment.
func (c Config) Mode() string { return ModeOf(c.Environment) }

// ModeOf maps a config environment to "sandbox" or "production" (anything but "prod" is sandbox).
func ModeOf(environment string) string {
	if environment == ProductionEnvironment {
		return "production"
	}
	return "sandbox"
}

// EffectiveRequestsPerMinuteLimit returns the config's override, or fallback (the adapter's documented limit).
func (c Config) EffectiveRequestsPerMinuteLimit(fallback int) int {
	if c.RequestsPerMinuteLimit > 0 {
		return c.RequestsPerMinuteLimit
	}
	return fallback
}

// Name identifies a gateway. It is stored in payment_gateway_configs.gateway.
type Name string

const (
	NameRazorpay Name = "razorpay"
	NameCashfree Name = "cashfree"
	NamePhonePe  Name = "phonepe"
)

// ChargeAttemptIdentity says how a gateway identifies one charge attempt.
type ChargeAttemptIdentity string

const (
	// OrderPerChargeAttempt: every attempt is its own gateway order (Razorpay, PhonePe).
	OrderPerChargeAttempt ChargeAttemptIdentity = "order_per_charge_attempt"
	// PaymentIDUnderMandate: attempts are payment ids under one subscription/mandate (Cashfree).
	PaymentIDUnderMandate ChargeAttemptIdentity = "payment_id_under_mandate"
)

// One-time payment kinds.
const (
	OneTimePaymentKindOrder       = "order"
	OneTimePaymentKindPaymentLink = "payment_link"
)

// Capabilities describes what a gateway supports and the limits the core must respect.
type Capabilities struct {
	// ChargeAttemptIdentity says how the gateway identifies one charge attempt. For OrderPerChargeAttempt the debit is
	// made against the gateway order created when the notification was sent, so the core refuses to debit an attempt
	// that has no gateway_order_id (and alerts) instead of sending a debit that cannot be matched.
	ChargeAttemptIdentity ChargeAttemptIdentity
	// SupportsIdempotencyKeys says a repeated call with the same key is safe (Cashfree). Without it a repeated call after
	// an unknown outcome could duplicate the effect, so the core does NOT blindly retry an unknown outcome: it verifies
	// with the gateway (the debit outcome table) or leaves the notification for the sync run.
	SupportsIdempotencyKeys bool
	// PushesMandateStatusWebhooks is false when the gateway does not push mandate pause/cancel/expiry events (today:
	// Cashfree, until its status-changed event is enabled). The core then calls FetchMandateStatus before sending a
	// notification and before debiting.
	PushesMandateStatusWebhooks bool
	OneTimePaymentKinds         []string // OneTimePaymentKindOrder, OneTimePaymentKindPaymentLink
	SupportsMandateCancellation bool
	MinAmountMinorUnits         int64
	MaxMandateAmountMinorUnits  int64 // 0 = no limit
	// MinNotificationToDebitGap is the least age of a delivered pre-debit notification before a debit.
	MinNotificationToDebitGap time.Duration
	// MinDebitRetryGap is the least time between two tries of the same debit attempt (a try that was rejected or hit a
	// gateway problem is released and retried by a later debit run). 0 = none documented. PhonePe documents 1.5h.
	// The time since the last try is read from the attempt's updated_at, which is when it was released, so a later
	// write to a released attempt can only make the retry wait a little longer, never shorter.
	MinDebitRetryGap time.Duration
	// RequestsPerMinuteLimit is the gateway's documented limit per merchant account (Config may override).
	RequestsPerMinuteLimit int
}

// SupportsOneTimePaymentKind reports whether the gateway can create the given kind of one-time payment.
func (c Capabilities) SupportsOneTimePaymentKind(kind string) bool {
	for _, k := range c.OneTimePaymentKinds {
		if k == kind {
			return true
		}
	}
	return false
}
