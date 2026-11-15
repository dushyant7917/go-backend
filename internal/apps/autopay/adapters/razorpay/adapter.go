// Package razorpay adapts Razorpay UPI Autopay (customer + order + token) to the autopay gateway contract.
//
// The pre-debit notification is a side effect of creating the order that will later be debited
// (order.notification{token_id, payment_after}); the debit is POST /payments/create/recurring against that order. Razorpay
// enforces one charge per order id, which is what makes a retried debit safe.
package razorpay

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go-backend/internal/apps/autopay/adapters/adapterutil"
	"go-backend/internal/apps/autopay/gateway"
	"go-backend/internal/common/billingwindow"
	pkgrazorpay "go-backend/pkg/razorpay"
	"go-backend/pkg/utils"
)

const (
	oneTimeValidity = 24 * time.Hour
	// receiptMaxLen is Razorpay's limit for an order receipt / payment-link reference id.
	receiptMaxLen = 40
)

// Notes keys we put on every order so a webhook can be matched without a lookup.
const (
	noteMerchantAutopayID = "merchant_autopay_id"
	noteMerchantPaymentID = "merchant_payment_id"
	notePurpose           = "purpose"
	purposeAuthorization  = "authorization"
	purposeAutopayCharge  = "autopay_charge"
	purposeOneTime        = "one_time_payment"
)

// Adapter is the Razorpay implementation of gateway.Gateway for one merchant account.
type Adapter struct {
	cfg    gateway.Config
	client pkgrazorpay.Client
	now    func() time.Time
}

// NewFactory returns the gateway.Factory for Razorpay. Pass pkgrazorpay.NewFactory() in production; tests pass a fake.
func NewFactory(newClient pkgrazorpay.ClientFactory) gateway.Factory {
	return func(cfg gateway.Config) (gateway.Gateway, error) {
		id, secret := cfg.Credentials["key_id"], cfg.Credentials["key_secret"]
		if id == "" || secret == "" {
			return nil, errors.New("razorpay credentials need key_id and key_secret")
		}
		return &Adapter{cfg: cfg, now: func() time.Time { return time.Now().UTC() }, client: newClient(pkgrazorpay.Credentials{KeyID: id, KeySecret: secret})}, nil
	}
}

func (a *Adapter) Name() gateway.Name { return gateway.NameRazorpay }

func (a *Adapter) Capabilities() gateway.Capabilities {
	return gateway.Capabilities{
		ChargeAttemptIdentity:       gateway.OrderPerChargeAttempt,
		SupportsIdempotencyKeys:     false,
		PushesMandateStatusWebhooks: true, // token.paused / token.cancelled
		OneTimePaymentKinds:         []string{gateway.OneTimePaymentKindOrder, gateway.OneTimePaymentKindPaymentLink},
		SupportsMandateCancellation: true, // PUT /customers/:id/tokens/:id/cancel; asynchronous, token.cancelled confirms
		MinAmountMinorUnits:         100,
		MaxMandateAmountMinorUnits:  0, // no cap of our own: Razorpay's limit depends on the merchant's category and it rejects an amount it does not allow
		MinNotificationToDebitGap:   billingwindow.Min,
		RequestsPerMinuteLimit:      60, // NOT published: Razorpay documents only that it returns 429 (checked 2026-11-12); 60 is our chosen default, override per account with gateway_ratelimit_rpm
	}
}

// ==================== Helpers ====================

// contact is the customer phone in the international format Razorpay expects.
func contact(u gateway.UserInfo) string {
	return "+" + strings.TrimPrefix(u.CountryCode, "+") + u.Phone
}

func requireCustomer(u gateway.UserInfo) error {
	if u.Email == "" || u.Phone == "" || u.CountryCode == "" {
		return adapterutil.Rejected("customer_details_missing", "user email, phone and country_code are required by Razorpay")
	}
	return nil
}

// fit40 returns id when it fits Razorpay's 40-character receipt limit, else a stable 40-character hash of it, so two
// long ids that share a prefix never collide.
func fit40(id string) string {
	if len(id) <= receiptMaxLen {
		return id
	}
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:])[:receiptMaxLen]
}

// mandateInvalidReasons are Razorpay error reasons/codes that say the token can no longer be debited.
var mandateInvalidReasons = map[string]bool{
	"invalid_token": true, "mandate_cancelled": true, "mandate_expired": true, "mandate_not_active": true,
	"token_cancelled": true, "token_paused": true, "token_expired": true,
}

// classify maps a Razorpay error to a gateway.Error. mutating says the call changes state at Razorpay (a timeout then
// leaves the outcome unknown); debit says an unrecognized 4xx is most likely a UPI restriction: temporary.
func classify(err error, mutating, debit bool) error {
	if err == nil {
		return nil
	}
	var ge *gateway.Error
	if errors.As(err, &ge) {
		return err
	}
	if apiErr, ok := pkgrazorpay.AsAPIError(err); ok {
		code := utils.FirstNonEmpty(apiErr.Reason, apiErr.Code)
		switch {
		case apiErr.HTTPStatus == http.StatusTooManyRequests:
			return gateway.NewRateLimitedError(code, apiErr.Description, apiErr.RetryAfter, err)
		case mandateInvalidReasons[strings.ToLower(apiErr.Reason)] || mandateInvalidReasons[strings.ToLower(apiErr.Code)]:
			return gateway.NewError(gateway.ErrorKindMandateInvalid, code, apiErr.Description, err)
		case apiErr.IsRetryable():
			return gateway.NewError(gateway.ErrorKindTransient, code, apiErr.Description, err)
		case debit && isDuplicateDebit(apiErr):
			return gateway.NewError(gateway.ErrorKindAlreadyProcessed, code, apiErr.Description, err)
		case debit:
			return gateway.NewError(gateway.ErrorKindRejectedTemporarily, code, apiErr.Description, err)
		default:
			return gateway.NewError(gateway.ErrorKindRejectedPermanently, code, apiErr.Description, err)
		}
	}
	return adapterutil.TransportError(err, mutating)
}

// isCancelNothingToDo recognizes Razorpay's answer to a cancel of a token that is not in a cancellable state
// (already cancelled, rejected, expired): the mandate is dead either way, so the caller treats it as done.
func isCancelNothingToDo(e *pkgrazorpay.APIError) bool {
	return strings.EqualFold(e.Reason, "invalid_mandate_state") || strings.EqualFold(e.Code, "invalid_mandate_state")
}

// isDuplicateDebit recognizes Razorpay's order-level idempotency rejection: a payment was already created for the order.
func isDuplicateDebit(e *pkgrazorpay.APIError) bool {
	d := strings.ToLower(e.Description)
	return strings.Contains(d, "already") && (strings.Contains(d, "paid") || strings.Contains(d, "payment") || strings.Contains(d, "processed"))
}

// ==================== Mandate ====================

func (a *Adapter) CreateMandateAuthorization(ctx context.Context, req gateway.MandateAuthorizationRequest) (*gateway.MandateAuthorizationSession, error) {
	if err := requireCustomer(req.User); err != nil {
		return nil, err
	}
	customerID, err := a.client.CreateCustomer(ctx, req.User.Email, contact(req.User))
	if err != nil {
		return nil, classify(err, false, false) // fail_existing=0 makes the call repeatable
	}
	order, err := a.client.CreateOrder(ctx, map[string]any{
		"amount": req.AuthorizationAmount, "currency": req.Currency, "customer_id": customerID, "method": "upi",
		"payment_capture": true, "receipt": fit40(req.MerchantPaymentID),
		"token": map[string]any{"max_amount": req.MaxDebitAmount, "expire_at": req.EndAt.Unix(), "frequency": "as_presented"}, // NPCI pattern: an adapter constant
		"notes": map[string]any{notePurpose: purposeAuthorization, noteMerchantAutopayID: req.MerchantAutopayID, noteMerchantPaymentID: req.MerchantPaymentID},
	})
	if err != nil {
		return nil, classify(err, true, false)
	}
	return &gateway.MandateAuthorizationSession{
		GatewayCustomerID: customerID, GatewayOrderID: order.ID, // the token id exists only after the customer authorizes
		ClientPayload: map[string]any{
			"key_id": a.cfg.Credentials["key_id"], "order_id": order.ID, "customer_id": customerID,
			"amount": req.AuthorizationAmount, "currency": req.Currency, "mode": a.cfg.Mode(),
		},
	}, nil
}

func (a *Adapter) FetchMandateAuthorizationStatus(ctx context.Context, ref gateway.MandateRef) (*gateway.MandateAuthorizationStatus, error) {
	if ref.GatewayMandateID != "" && ref.GatewayCustomerID != "" {
		tok, err := a.client.FetchToken(ctx, ref.GatewayCustomerID, ref.GatewayMandateID)
		if err != nil {
			return nil, classify(err, false, false)
		}
		st := &gateway.MandateAuthorizationStatus{State: gateway.AuthorizationPending, GatewayMandateID: tok.ID, GatewayCustomerID: ref.GatewayCustomerID}
		switch strings.ToLower(tok.Status) {
		case "confirmed":
			st.State = gateway.AuthorizationSucceeded
		case "rejected", "cancelled":
			st.State, st.FailureCode, st.FailureDescription = gateway.AuthorizationFailed, "token_"+strings.ToLower(tok.Status), tok.FailureReason
		}
		return st, nil
	}
	if ref.GatewayOrderID == "" {
		return &gateway.MandateAuthorizationStatus{State: gateway.AuthorizationPending}, nil
	}
	payments, err := a.client.FetchOrderPayments(ctx, ref.GatewayOrderID)
	if err != nil {
		return nil, classify(err, false, false)
	}
	st := &gateway.MandateAuthorizationStatus{State: gateway.AuthorizationPending, GatewayCustomerID: ref.GatewayCustomerID}
	var failed *pkgrazorpay.Payment
	open := false
	for i := range payments {
		p := payments[i]
		switch strings.ToLower(p.Status) {
		case "captured":
			st.State, st.GatewayPaymentID, st.GatewayMandateID = gateway.AuthorizationSucceeded, p.ID, p.TokenID
			return st, nil
		case "failed":
			failed = &payments[i]
		default:
			open = true // created / authorized: the customer is still in the flow
		}
	}
	if failed != nil && !open {
		st.State, st.FailureCode, st.FailureDescription = gateway.AuthorizationFailed, adapterutil.FailureCode(failed.ErrorCode, failed.ErrorReason, "payment_failed"), failed.ErrorDescription
	}
	return st, nil
}

func (a *Adapter) FetchMandateStatus(ctx context.Context, ref gateway.MandateRef) (*gateway.MandateStatus, error) {
	if ref.GatewayMandateID == "" || ref.GatewayCustomerID == "" {
		return &gateway.MandateStatus{State: gateway.MandateAwaitingAuthorization}, nil
	}
	tok, err := a.client.FetchToken(ctx, ref.GatewayCustomerID, ref.GatewayMandateID)
	if err != nil {
		return nil, classify(err, false, false)
	}
	return &gateway.MandateStatus{State: mandateState(tok.Status), GatewayMandateID: tok.ID}, nil
}

// CancelMandate asks Razorpay to cancel the token. Razorpay only acknowledges ("cancellation_initiated"): the final
// "cancelled" state arrives with the token.cancelled webhook, and from the acknowledgement on every debit against the
// token fails, so the core cancels the mandate locally right away and stops debiting it.
func (a *Adapter) CancelMandate(ctx context.Context, ref gateway.MandateRef) error {
	if ref.GatewayMandateID == "" || ref.GatewayCustomerID == "" {
		return gateway.NewError(gateway.ErrorKindMandateInvalid, "token_missing", "the mandate has no Razorpay token to cancel", nil)
	}
	_, err := a.client.CancelToken(ctx, ref.GatewayCustomerID, ref.GatewayMandateID)
	if apiErr, ok := pkgrazorpay.AsAPIError(err); ok {
		code := utils.FirstNonEmpty(apiErr.Reason, apiErr.Code)
		switch {
		case isCancelNothingToDo(apiErr): // already cancelled / rejected / expired: the mandate is dead either way
			return gateway.NewError(gateway.ErrorKindAlreadyProcessed, code, apiErr.Description, err)
		case strings.EqualFold(apiErr.Reason, "concurrent_request_in_progress") || strings.EqualFold(apiErr.Code, "concurrent_request_in_progress"):
			return gateway.NewError(gateway.ErrorKindTransient, code, apiErr.Description, err) // Razorpay: wait 60s and retry
		}
	}
	return classify(err, true, false)
}

func mandateState(status string) gateway.MandateState {
	switch strings.ToLower(status) {
	case "confirmed":
		return gateway.MandateActive
	case "paused":
		return gateway.MandatePaused
	case "cancelled", "cancellation_initiated": // from the acknowledgement on, every debit against the token fails
		return gateway.MandateCancelled
	case "expired", "rejected":
		return gateway.MandateExpired
	default: // initiated
		return gateway.MandateAwaitingAuthorization
	}
}

// ==================== Pre-debit notification and debit ====================

func (a *Adapter) SendPreDebitNotification(ctx context.Context, req gateway.PreDebitNotificationRequest) (*gateway.PreDebitNotificationResult, error) {
	if req.Mandate.GatewayMandateID == "" {
		return nil, gateway.NewError(gateway.ErrorKindMandateInvalid, "token_missing", "the mandate has no Razorpay token yet", nil)
	}
	receipt := fit40(req.PreDebitNotificationID) // unique per send, so a re-send never adopts the first send's order
	order, err := a.client.CreateOrder(ctx, map[string]any{
		"amount": req.Amount, "currency": req.Currency, "payment_capture": true, "receipt": receipt,
		"notification": map[string]any{"token_id": req.Mandate.GatewayMandateID, "payment_after": req.EarliestDebitAt.Unix()},
		"notes":        map[string]any{notePurpose: purposeAutopayCharge, noteMerchantAutopayID: req.Mandate.MerchantAutopayID, noteMerchantPaymentID: req.MerchantPaymentID},
	})
	if err != nil {
		cerr := classify(err, true, false)
		if !gateway.IsKind(cerr, gateway.ErrorKindOutcomeUnknown) {
			return nil, cerr
		}
		// The request may have created the order: look it up by receipt rather than sending a second notification.
		existing, ferr := a.client.FindOrderByReceipt(ctx, receipt)
		if ferr != nil || existing == nil {
			return nil, cerr
		}
		order = existing
	}
	n := notificationOf(order)
	return &gateway.PreDebitNotificationResult{GatewayOrderID: order.ID, PreDebitNotificationID: req.PreDebitNotificationID, State: n.State, DeliveredAt: n.DeliveredAt}, nil
}

// notificationOf reads the notification outcome from an order. An order that has been attempted or paid was
// necessarily past the notification stage.
func notificationOf(order *pkgrazorpay.Order) *gateway.PreDebitNotificationStatus {
	st := &gateway.PreDebitNotificationStatus{State: gateway.NotificationPending}
	if n := order.Notification; n != nil {
		st.DeliveredAt = n.DeliveredAt.UTC()
		s := strings.ToLower(n.Status)
		switch {
		case strings.Contains(s, "fail") || strings.Contains(s, "reject"):
			st.State, st.FailureCode, st.FailureDescription = gateway.NotificationFailed, "notification_failed", n.FailureReason
		case strings.Contains(s, "deliver") || s == "sent" || s == "success":
			st.State = gateway.NotificationDelivered
		}
	}
	if st.State == gateway.NotificationPending && (order.Status == "attempted" || order.Status == "paid") {
		st.State = gateway.NotificationDelivered
	}
	return st
}

func (a *Adapter) FetchPreDebitNotificationStatus(ctx context.Context, ref gateway.ChargeAttemptRef) (*gateway.PreDebitNotificationStatus, error) {
	if ref.GatewayOrderID == "" {
		return &gateway.PreDebitNotificationStatus{State: gateway.NotificationPending}, nil
	}
	order, err := a.client.FetchOrder(ctx, ref.GatewayOrderID)
	if err != nil {
		return nil, classify(err, false, false)
	}
	return notificationOf(order), nil
}

func (a *Adapter) SubmitDebit(ctx context.Context, req gateway.DebitRequest) (*gateway.DebitSubmission, error) {
	if err := requireCustomer(req.User); err != nil {
		return nil, err
	}
	m := req.Attempt.Mandate
	paymentID, err := a.client.CreateRecurringPayment(ctx, map[string]any{
		"email": req.User.Email, "contact": contact(req.User), "amount": req.Amount, "currency": req.Currency,
		"order_id": req.Attempt.GatewayOrderID, "customer_id": m.GatewayCustomerID, "token": m.GatewayMandateID, "recurring": true,
		"notes": map[string]any{noteMerchantPaymentID: req.Attempt.MerchantPaymentID},
	})
	if err != nil {
		return nil, classify(err, true, true)
	}
	return &gateway.DebitSubmission{State: gateway.PaymentPending, GatewayPaymentID: paymentID, DebitExecutionID: req.DebitExecutionID}, nil
}

func (a *Adapter) FetchChargeAttemptStatus(ctx context.Context, ref gateway.ChargeAttemptRef) (*gateway.ChargeAttemptStatus, error) {
	out := &gateway.ChargeAttemptStatus{Notification: gateway.NotificationPending, Debit: gateway.PaymentPending}
	if ref.GatewayOrderID == "" {
		return out, nil // no order was ever created
	}
	order, err := a.client.FetchOrder(ctx, ref.GatewayOrderID)
	if err != nil {
		return nil, classify(err, false, false)
	}
	out.GatewayOrderID = order.ID
	n := notificationOf(order)
	out.Notification, out.NotificationDeliveredAt = n.State, n.DeliveredAt
	if n.State == gateway.NotificationFailed {
		out.FailureCode, out.FailureDescription = n.FailureCode, n.FailureDescription
		return out, nil
	}
	if order.Status != "paid" && order.Attempts == 0 {
		return out, nil // not debited yet
	}
	payments, err := a.client.FetchOrderPayments(ctx, ref.GatewayOrderID)
	if err != nil {
		return nil, classify(err, false, false)
	}
	var last *pkgrazorpay.Payment
	for i := range payments {
		p := &payments[i]
		if strings.EqualFold(p.Status, "captured") {
			out.DebitExists, out.Debit, out.GatewayPaymentID = true, gateway.PaymentSucceeded, p.ID
			return out, nil
		}
		if last == nil || p.CreatedAt.After(last.CreatedAt) {
			last = p
		}
	}
	if last == nil {
		if order.Status == "paid" {
			out.DebitExists, out.Debit = true, gateway.PaymentSucceeded
		}
		return out, nil
	}
	out.DebitExists, out.GatewayPaymentID = true, last.ID
	if strings.EqualFold(last.Status, "failed") {
		out.Debit = gateway.PaymentFailed
		out.FailureCode, out.FailureDescription = adapterutil.FailureCode(last.ErrorCode, last.ErrorReason, "payment_failed"), last.ErrorDescription
	}
	return out, nil
}

// ==================== One-time payments ====================

func (a *Adapter) CreateOneTimePayment(ctx context.Context, req gateway.OneTimePaymentRequest) (*gateway.OneTimePaymentSession, error) {
	notes := map[string]any{notePurpose: purposeOneTime, noteMerchantAutopayID: req.MerchantAutopayID, noteMerchantPaymentID: req.MerchantPaymentID}
	if req.Kind == gateway.OneTimePaymentKindPaymentLink {
		if err := requireCustomer(req.User); err != nil {
			return nil, err
		}
		data := map[string]any{
			"amount": req.Amount, "currency": req.Currency, "description": "One-time Payment", "reference_id": fit40(req.MerchantPaymentID),
			"customer":  map[string]any{"email": req.User.Email, "contact": contact(req.User)},
			"expire_by": a.now().Add(oneTimeValidity).Unix(), "notify": map[string]any{"sms": false, "email": false}, "notes": notes,
		}
		if req.ReturnURL != "" {
			data["callback_url"], data["callback_method"] = req.ReturnURL, "get"
		}
		link, err := a.client.CreatePaymentLink(ctx, data)
		if err != nil {
			return nil, classify(err, true, false)
		}
		if link.ID == "" || link.ShortURL == "" {
			return nil, adapterutil.Rejected("no_link", "razorpay did not return a payment link")
		}
		return &gateway.OneTimePaymentSession{Kind: req.Kind, GatewayPaymentLinkID: link.ID, LinkURL: link.ShortURL}, nil
	}
	order, err := a.client.CreateOrder(ctx, map[string]any{
		"amount": req.Amount, "currency": req.Currency, "payment_capture": true, "receipt": fit40(req.MerchantPaymentID), "notes": notes,
	})
	if err != nil {
		return nil, classify(err, true, false)
	}
	return &gateway.OneTimePaymentSession{
		Kind: gateway.OneTimePaymentKindOrder, GatewayOrderID: order.ID,
		ClientPayload: map[string]any{"key_id": a.cfg.Credentials["key_id"], "order_id": order.ID, "amount": req.Amount, "currency": req.Currency, "mode": a.cfg.Mode()},
	}, nil
}

func (a *Adapter) FetchOneTimePaymentStatus(ctx context.Context, ref gateway.OneTimePaymentRef) (*gateway.OneTimePaymentStatus, error) {
	if ref.Kind == gateway.OneTimePaymentKindPaymentLink {
		link, err := a.client.FetchPaymentLink(ctx, ref.GatewayPaymentLinkID)
		if err != nil {
			return nil, classify(err, false, false)
		}
		st := &gateway.OneTimePaymentStatus{State: gateway.PaymentPending}
		if len(link.PaymentIDs) > 0 {
			st.GatewayPaymentID = link.PaymentIDs[len(link.PaymentIDs)-1]
		}
		switch strings.ToLower(link.Status) {
		case "paid":
			st.State = gateway.PaymentSucceeded
		case "expired", "cancelled":
			st.State, st.FailureCode = gateway.PaymentFailed, "link_"+strings.ToLower(link.Status)
		}
		return st, nil
	}
	order, err := a.client.FetchOrder(ctx, ref.GatewayOrderID)
	if err != nil {
		return nil, classify(err, false, false)
	}
	st := &gateway.OneTimePaymentStatus{State: gateway.PaymentPending, GatewayOrderID: order.ID}
	if order.Status == "paid" {
		st.State = gateway.PaymentSucceeded
		if payments, perr := a.client.FetchOrderPayments(ctx, ref.GatewayOrderID); perr == nil {
			for _, p := range payments {
				if strings.EqualFold(p.Status, "captured") {
					st.GatewayPaymentID = p.ID
				}
			}
		}
	}
	return st, nil
}

// ==================== Webhooks ====================

// VerifyAndParseWebhook verifies X-Razorpay-Signature (HMAC-SHA256 hex of the raw body under the webhook secret) and
// normalizes the event. Razorpay delivers at least once and not in order; x-razorpay-event-id is the dedupe key and
// created_at (unix seconds) the event time.
func (a *Adapter) VerifyAndParseWebhook(headers http.Header, rawBody []byte) ([]gateway.Event, error) {
	if !pkgrazorpay.VerifyWebhookSignature(a.cfg.WebhookSecret, rawBody, headers.Get("X-Razorpay-Signature")) {
		return nil, gateway.ErrInvalidSignature
	}
	var env struct {
		Event     string                 `json:"event"`
		CreatedAt int64                  `json:"created_at"`
		Payload   map[string]interface{} `json:"payload"`
	}
	if err := json.Unmarshal(rawBody, &env); err != nil {
		return nil, fmt.Errorf("razorpay webhook is not valid JSON: %w", err)
	}
	p := env.Payload
	at := time.Time{}
	if env.CreatedAt > 0 {
		at = time.Unix(env.CreatedAt, 0).UTC()
	}
	str := func(path ...string) string { return adapterutil.Dig(p, path...) }
	num := func(path ...string) int64 {
		f, _ := adapterutil.DigNumber(p, path...)
		return int64(f)
	}

	base := gateway.Event{GatewayEventType: env.Event, GatewayEventAt: at}
	emit := func(t gateway.EventType, ev gateway.Event) []gateway.Event {
		ev.Type, ev.GatewayEventType, ev.GatewayEventAt = t, env.Event, at
		ev.DedupeKey = utils.FirstNonEmpty(headers.Get("x-razorpay-event-id"),
			adapterutil.HashKey(env.Event, ev.GatewayOrderID, ev.GatewayPaymentID, ev.GatewayPaymentLinkID, ev.GatewayMandateID, fmt.Sprint(env.CreatedAt)))
		return []gateway.Event{ev}
	}

	switch env.Event {
	case "payment.captured", "payment.failed":
		notes := func(key string) string { return str("payment", "entity", "notes", key) }
		ev := base
		ev.GatewayPaymentID, ev.GatewayOrderID, ev.GatewayMandateID = str("payment", "entity", "id"), str("payment", "entity", "order_id"), str("payment", "entity", "token_id")
		ev.MerchantPaymentID, ev.MerchantAutopayID = notes(noteMerchantPaymentID), notes(noteMerchantAutopayID)
		oneTime := notes(notePurpose) == purposeOneTime
		if env.Event == "payment.captured" {
			ev.Amount = num("payment", "entity", "amount")
			if oneTime {
				return emit(gateway.EventOneTimePaymentSucceeded, ev), nil
			}
			return emit(gateway.EventAutopayChargeSucceeded, ev), nil
		}
		ev.FailureCode = adapterutil.FailureCode(str("payment", "entity", "error_code"), str("payment", "entity", "error_reason"), "payment_failed")
		ev.FailureDescription = str("payment", "entity", "error_description")
		if oneTime {
			return emit(gateway.EventOneTimePaymentFailed, ev), nil
		}
		return emit(gateway.EventAutopayChargeFailed, ev), nil

	case "order.notification.delivered", "order.notification.failed":
		ev := base
		ev.GatewayOrderID = str("order", "entity", "id")
		ev.MerchantPaymentID, ev.MerchantAutopayID = str("order", "entity", "notes", noteMerchantPaymentID), str("order", "entity", "notes", noteMerchantAutopayID)
		if env.Event == "order.notification.delivered" {
			if d := num("order", "entity", "notification", "delivered_at"); d > 0 {
				ev.DeliveredAt = time.Unix(d, 0).UTC()
			} else {
				ev.DeliveredAt = at // Razorpay's own event time, never the time we received it
			}
			return emit(gateway.EventPreDebitNotificationDelivered, ev), nil
		}
		ev.FailureCode, ev.FailureDescription = "notification_failed", str("order", "entity", "notification", "failure_reason")
		return emit(gateway.EventPreDebitNotificationFailed, ev), nil

	case "token.confirmed":
		ev := base
		ev.GatewayMandateID = str("token", "entity", "id")
		return emit(gateway.EventMandateAuthorizationSucceeded, ev), nil
	case "token.rejected":
		ev := base
		ev.GatewayMandateID = str("token", "entity", "id")
		ev.FailureCode, ev.FailureDescription = "token_rejected", str("token", "entity", "recurring_details", "failure_reason")
		return emit(gateway.EventMandateAuthorizationFailed, ev), nil
	case "token.paused":
		ev := base
		ev.GatewayMandateID, ev.MandateState = str("token", "entity", "id"), gateway.MandatePaused
		return emit(gateway.EventMandateStatusChanged, ev), nil
	case "token.cancelled":
		ev := base
		ev.GatewayMandateID, ev.MandateState = str("token", "entity", "id"), gateway.MandateCancelled
		return emit(gateway.EventMandateStatusChanged, ev), nil

	case "payment_link.paid":
		ev := base
		ev.GatewayPaymentLinkID, ev.GatewayPaymentID = str("payment_link", "entity", "id"), str("payment", "entity", "id")
		ev.MerchantPaymentID = str("payment_link", "entity", "notes", noteMerchantPaymentID)
		ev.Amount = num("payment_link", "entity", "amount_paid")
		return emit(gateway.EventOneTimePaymentSucceeded, ev), nil
	case "payment_link.expired", "payment_link.cancelled":
		ev := base
		ev.GatewayPaymentLinkID = str("payment_link", "entity", "id")
		ev.MerchantPaymentID = str("payment_link", "entity", "notes", noteMerchantPaymentID)
		ev.FailureCode = "link_" + strings.TrimPrefix(env.Event, "payment_link.")
		return emit(gateway.EventOneTimePaymentFailed, ev), nil
	}
	return nil, nil // payment.authorized (orders capture automatically), refunds, orders.paid, ...
}
