package gateway

import "time"

// UserInfo is the customer data gateways ask for when creating customers, orders and notifications.
type UserInfo struct {
	UserID      string
	Name        string
	Email       string
	Phone       string
	CountryCode string
}

// ---- Mandate ----

// MandateRef identifies a mandate at the gateway. Which fields are set depends on the gateway and on how far the
// mandate has progressed (a Razorpay token id only exists after authorization).
type MandateRef struct {
	MerchantAutopayID string // our id (autopay_mandates.merchant_autopay_id)
	GatewayMandateID  string // Razorpay token_id | Cashfree cf_subscription_id | PhonePe subscriptionId
	GatewayCustomerID string // Razorpay customer id
	GatewayOrderID    string // the setup/authorization order, when the gateway has one
	// AuthorizationMerchantPaymentID is our id of the authorization payment (cycle 0's charge attempt). Gateways that
	// authorize through an order (PhonePe, Razorpay) use it as the order's merchant reference.
	AuthorizationMerchantPaymentID string
}

type MandateAuthorizationRequest struct {
	MerchantAutopayID string
	// MerchantPaymentID is our id for the authorization payment (cycle 0's charge attempt).
	MerchantPaymentID   string
	User                UserInfo
	AuthorizationAmount int64 // charged at setup (minor units)
	MaxDebitAmount      int64 // cap per later debit (minor units)
	Currency            string
	EndAt               time.Time // when the mandate ends (UTC)
	ReturnURL           string
}

// MandateAuthorizationSession is what the client app needs to start authorization, plus the gateway ids to store.
type MandateAuthorizationSession struct {
	GatewayMandateID  string
	GatewayCustomerID string
	GatewayOrderID    string
	// ClientPayload is opaque to the core and returned to the app as-is (key id, session id, order id + token, ...).
	ClientPayload map[string]any
}

// AuthorizationState is the outcome of mandate authorization.
type AuthorizationState string

const (
	AuthorizationPending   AuthorizationState = "pending"
	AuthorizationSucceeded AuthorizationState = "succeeded"
	AuthorizationFailed    AuthorizationState = "failed"
)

type MandateAuthorizationStatus struct {
	State              AuthorizationState
	GatewayMandateID   string
	GatewayCustomerID  string
	GatewayPaymentID   string // the authorization payment
	FailureCode        string
	FailureDescription string
}

// MandateState is a mandate's lifecycle state as the core models it.
type MandateState string

const (
	MandateAwaitingAuthorization MandateState = "awaiting_authorization"
	MandateActive                MandateState = "active"
	MandatePaused                MandateState = "paused"
	MandateCancelled             MandateState = "cancelled"
	MandateExpired               MandateState = "expired"
)

type MandateStatus struct {
	State            MandateState
	GatewayMandateID string
}

// ---- Charge attempt: pre-debit notification and debit ----

// ChargeAttemptRef identifies one charge attempt at the gateway.
type ChargeAttemptRef struct {
	Mandate                MandateRef
	MerchantPaymentID      string // our id for the attempt (autopay_charge_attempts.merchant_payment_id)
	GatewayOrderID         string
	GatewayPaymentID       string
	PreDebitNotificationID string
	DebitExecutionID       string
}

type PreDebitNotificationRequest struct {
	Mandate           MandateRef
	MerchantPaymentID string
	// PreDebitNotificationID doubles as the idempotency key for gateways that support one.
	PreDebitNotificationID string
	Amount                 int64
	Currency               string
	// EarliestDebitAt is billingwindow.EarliestDebit(now, scheduled): never in the past and at least the minimum
	// notification-to-debit gap after now. Gateways that take the debit time inside the notification (Razorpay
	// payment_after, PhonePe expiry) must be given this value.
	EarliestDebitAt time.Time
	User            UserInfo
}

// NotificationState is the outcome of a pre-debit notification.
type NotificationState string

const (
	NotificationPending   NotificationState = "pending"
	NotificationDelivered NotificationState = "delivered"
	NotificationFailed    NotificationState = "failed"
)

type PreDebitNotificationResult struct {
	GatewayOrderID         string
	PreDebitNotificationID string
	State                  NotificationState
	// DeliveredAt is the gateway's delivered/completed time (UTC); zero when the gateway gives none. The core then
	// falls back to the time the send call returned, never to the time it noticed.
	DeliveredAt time.Time
	// ValidAfter is the earliest debit time the gateway reports (PhonePe); zero when not applicable.
	ValidAfter time.Time
}

type PreDebitNotificationStatus struct {
	State              NotificationState
	DeliveredAt        time.Time
	ValidAfter         time.Time
	FailureCode        string
	FailureDescription string
}

// PaymentState is the outcome of a debit or of a one-time payment.
type PaymentState string

const (
	PaymentPending   PaymentState = "pending"
	PaymentSucceeded PaymentState = "succeeded"
	PaymentFailed    PaymentState = "failed"
)

type DebitRequest struct {
	Attempt ChargeAttemptRef
	Amount  int64
	// DebitExecutionID is the idempotency key: a retry of an outcome-unknown submission must reuse it.
	DebitExecutionID string
	Currency         string
	User             UserInfo
}

// DebitSubmission is the synchronous answer to a debit submission. State is usually pending; some gateways answer
// success or failure immediately.
type DebitSubmission struct {
	State            PaymentState
	GatewayPaymentID string
	DebitExecutionID string
}

// ChargeAttemptStatus is the gateway's view of a charge attempt (notification and debit).
type ChargeAttemptStatus struct {
	Notification            NotificationState
	NotificationDeliveredAt time.Time
	ValidAfter              time.Time
	Debit                   PaymentState
	// DebitExists is false when the gateway has no record of a debit for this attempt (used to settle an
	// outcome-unknown submission: no record means it never happened).
	DebitExists        bool
	GatewayOrderID     string
	GatewayPaymentID   string
	FailureCode        string
	FailureDescription string
	FailureTemporary   bool // a timing problem (e.g. UPI blackout), see ErrorKindRejectedTemporarily
}

// ---- One-time payments ----

type OneTimePaymentRequest struct {
	MerchantAutopayID string
	MerchantPaymentID string
	User              UserInfo
	Amount            int64
	Currency          string
	Kind              string // OneTimePaymentKindOrder | OneTimePaymentKindPaymentLink
	ReturnURL         string
}

type OneTimePaymentSession struct {
	Kind                 string
	GatewayOrderID       string
	GatewayPaymentLinkID string
	LinkURL              string
	ClientPayload        map[string]any
}

type OneTimePaymentRef struct {
	MerchantAutopayID    string
	MerchantPaymentID    string // our id of the payment (PhonePe looks orders up by it)
	Kind                 string
	GatewayOrderID       string
	GatewayPaymentLinkID string
	GatewayPaymentID     string
}

type OneTimePaymentStatus struct {
	State              PaymentState
	GatewayOrderID     string
	GatewayPaymentID   string
	FailureCode        string
	FailureDescription string
}

// ---- Webhook events ----

// EventType is a normalized webhook event. Names say exactly what happened.
type EventType string

const (
	EventMandateAuthorizationSucceeded EventType = "mandate_authorization_succeeded"
	EventMandateAuthorizationFailed    EventType = "mandate_authorization_failed"
	EventMandateStatusChanged          EventType = "mandate_status_changed"
	EventPreDebitNotificationDelivered EventType = "pre_debit_notification_delivered"
	EventPreDebitNotificationFailed    EventType = "pre_debit_notification_failed"
	EventAutopayChargeSucceeded        EventType = "autopay_charge_succeeded"
	EventAutopayChargeFailed           EventType = "autopay_charge_failed"
	EventOneTimePaymentSucceeded       EventType = "one_time_payment_succeeded"
	EventOneTimePaymentFailed          EventType = "one_time_payment_failed"
)

// Event is one normalized webhook event. Apart from Type it only carries identifiers (to find the entity) and the
// facts the payload itself states; the core applies it through the state-transition rules.
type Event struct {
	Type EventType
	// DedupeKey is the gateway's event id when it has one, else a hash of identifying payload keys, else a hash of
	// the raw body (autopay_webhook_events.event_dedupe_key).
	DedupeKey        string
	GatewayEventType string    // the gateway's own event name, for logs and audit
	GatewayEventAt   time.Time // UTC; zero when the payload has no event time

	MerchantAutopayID    string
	MerchantPaymentID    string
	GatewayMandateID     string
	GatewayOrderID       string
	GatewayPaymentID     string
	GatewayPaymentLinkID string

	// Amount is the amount (minor units) the payload says was paid, 0 when it does not say. The core ignores a success
	// whose Amount is below the attempt's amount (an underpayment must never settle a cycle).
	Amount int64

	MandateState       MandateState // EventMandateStatusChanged
	DeliveredAt        time.Time    // EventPreDebitNotificationDelivered, when the payload carries it
	FailureCode        string
	FailureDescription string
	FailureTemporary   bool
}
