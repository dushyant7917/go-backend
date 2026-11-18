// Package cashfree adapts Cashfree Subscriptions (UPI Autopay on-demand mandates with merchant-controlled charging:
// we send the pre-debit notification via notify-mandate and debit via execute-mandate) to the autopay gateway contract.
package cashfree

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go-backend/internal/apps/autopay/adapters/adapterutil"
	"go-backend/internal/apps/autopay/gateway"
	"go-backend/internal/common/billingwindow"
	pkgcashfree "go-backend/pkg/cashfree"
	"go-backend/pkg/utils"
)

// oneTimeValidity is how long a one-time order or payment link stays payable.
const oneTimeValidity = 24 * time.Hour

// Adapter is the Cashfree implementation of gateway.Gateway for one merchant account.
type Adapter struct {
	cfg    gateway.Config
	client pkgcashfree.Client
	now    func() time.Time
}

// NewFactory returns the gateway.Factory for Cashfree. Pass pkgcashfree.NewFactory() in production; tests pass a fake.
func NewFactory(newClient pkgcashfree.ClientFactory) gateway.Factory {
	return func(cfg gateway.Config) (gateway.Gateway, error) {
		id, secret := cfg.Credentials["client_id"], cfg.Credentials["client_secret"]
		if id == "" || secret == "" {
			return nil, errors.New("cashfree credentials need client_id and client_secret")
		}
		return &Adapter{
			cfg: cfg, now: func() time.Time { return time.Now().UTC() },
			client: newClient(pkgcashfree.Credentials{ClientID: id, ClientSecret: secret, Sandbox: cfg.IsSandbox()}),
		}, nil
	}
}

func (a *Adapter) Name() gateway.Name { return gateway.NameCashfree }

func (a *Adapter) Capabilities() gateway.Capabilities {
	return gateway.Capabilities{
		ChargeAttemptIdentity:   gateway.PaymentIDUnderMandate,
		SupportsIdempotencyKeys: true,
		// Cashfree's dashboard does not offer the status-changed event today, so the core polls FetchMandateStatus
		// before a notification and before a debit. Set to true once Cashfree enables SUBSCRIPTION_STATUS_CHANGED.
		PushesMandateStatusWebhooks: false,
		OneTimePaymentKinds:         []string{gateway.OneTimePaymentKindOrder, gateway.OneTimePaymentKindPaymentLink},
		SupportsMandateCancellation: true,
		MinAmountMinorUnits:         100,
		MinNotificationToDebitGap:   billingwindow.Min,
		RequestsPerMinuteLimit:      60, // documented: subscription APIs 60/min per account (PG Create Order 200, Get Order 400 are higher); one bucket per account at the lowest
	}
}

// ==================== Errors ====================

// classify maps a Cashfree error to a gateway.Error. mutating says whether the call changes state at Cashfree (a
// timeout then leaves the outcome unknown); debit says that an unrecognized 4xx is most likely a UPI blackout window.
func classify(err error, mutating, debit bool) error {
	if err == nil {
		return nil
	}
	var ge *gateway.Error
	if errors.As(err, &ge) {
		return err
	}
	if apiErr, ok := pkgcashfree.AsAPIError(err); ok {
		code := apiErr.Code
		switch {
		case apiErr.HTTPStatus == http.StatusTooManyRequests:
			return gateway.NewRateLimitedError(code, apiErr.Message, apiErr.RetryAfter, err)
		case apiErr.IsInProgress():
			return gateway.NewError(gateway.ErrorKindAlreadyProcessed, code, apiErr.Message, err)
		case strings.EqualFold(code, "subscription_not_active"):
			return gateway.NewError(gateway.ErrorKindMandateInvalid, code, apiErr.Message, err)
		case apiErr.IsRetryable():
			return gateway.NewError(gateway.ErrorKindTransient, code, apiErr.Message, err)
		case debit:
			return gateway.NewError(gateway.ErrorKindRejectedTemporarily, code, apiErr.Message, err)
		default:
			return gateway.NewError(gateway.ErrorKindRejectedPermanently, code, apiErr.Message, err)
		}
	}
	return adapterutil.TransportError(err, mutating)
}

// ==================== Mandate ====================

func (a *Adapter) CreateMandateAuthorization(ctx context.Context, req gateway.MandateAuthorizationRequest) (*gateway.MandateAuthorizationSession, error) {
	if req.User.Email == "" || req.User.Phone == "" {
		return nil, adapterutil.Rejected("customer_details_missing", "user email and phone are required by Cashfree")
	}
	sub, err := a.client.CreateSubscription(ctx, pkgcashfree.CreateSubscriptionParams{
		SubscriptionID: req.MerchantAutopayID, PlanName: utils.Truncate(a.cfg.AppName+" mandate", 40),
		MaxAmountPaise: req.MaxDebitAmount, AuthorizationAmountPaise: req.AuthorizationAmount,
		CustomerName: customerName(req.User.UserID), CustomerEmail: req.User.Email, CustomerPhone: req.User.Phone,
		ExpiryTime: req.EndAt, ReturnURL: req.ReturnURL, IdempotencyKey: req.MerchantAutopayID,
	})
	if err != nil {
		return nil, classify(err, true, false)
	}
	if sub.SessionID == "" {
		return nil, gateway.NewError(gateway.ErrorKindRejectedPermanently, "no_session", "cashfree did not return a subscription_session_id", nil)
	}
	return &gateway.MandateAuthorizationSession{
		GatewayMandateID: sub.CFSubscriptionID,
		ClientPayload:    map[string]any{"subscription_session_id": sub.SessionID, "subscription_id": req.MerchantAutopayID, "mode": a.cfg.Mode()},
	}, nil
}

func (a *Adapter) FetchMandateAuthorizationStatus(ctx context.Context, ref gateway.MandateRef) (*gateway.MandateAuthorizationStatus, error) {
	sub, err := a.client.FetchSubscription(ctx, ref.MerchantAutopayID)
	if err != nil {
		return nil, classify(err, false, false)
	}
	st := &gateway.MandateAuthorizationStatus{State: gateway.AuthorizationPending, GatewayMandateID: sub.CFSubscriptionID, GatewayPaymentID: sub.AuthPaymentID}
	switch normalize(sub.AuthStatus) {
	case "SUCCESS", "ACTIVE":
		st.State = gateway.AuthorizationSucceeded
	case "FAILED":
		st.State = gateway.AuthorizationFailed
		st.FailureCode = "authorization_failed"
	}
	if normalize(sub.Status) == "ACTIVE" {
		st.State = gateway.AuthorizationSucceeded
	}
	return st, nil
}

func (a *Adapter) FetchMandateStatus(ctx context.Context, ref gateway.MandateRef) (*gateway.MandateStatus, error) {
	sub, err := a.client.FetchSubscription(ctx, ref.MerchantAutopayID)
	if err != nil {
		return nil, classify(err, false, false)
	}
	return &gateway.MandateStatus{State: mandateState(sub.Status), GatewayMandateID: sub.CFSubscriptionID}, nil
}

// CancelMandate cancels the subscription. Cashfree rejects a cancel of a subscription that is not cancellable with a 4xx
// and documents no code for it, so a rejection is settled by the subscription's own state (see CancelAlreadyDone).
func (a *Adapter) CancelMandate(ctx context.Context, ref gateway.MandateRef) error {
	_, err := a.client.ManageSubscription(ctx, ref.MerchantAutopayID, "CANCEL")
	if err == nil {
		return nil
	}
	return adapterutil.CancelAlreadyDone(classify(err, true, false), err, func() (gateway.MandateState, error) {
		sub, ferr := a.client.FetchSubscription(ctx, ref.MerchantAutopayID)
		if ferr != nil {
			return "", ferr
		}
		return mandateState(sub.Status), nil
	})
}

// mandateState maps Cashfree's subscription status onto the core's mandate states. Unknown statuses are treated as
// "awaiting authorization" (nothing to do for UPI on-demand: INITIALIZED, BANK_APPROVAL_PENDING).
func mandateState(status string) gateway.MandateState {
	switch normalize(status) {
	case "ACTIVE":
		return gateway.MandateActive
	case "CUSTOMER_PAUSED", "PAUSED", "ON_HOLD":
		return gateway.MandatePaused
	case "CANCELLED", "CUSTOMER_CANCELLED":
		return gateway.MandateCancelled
	case "EXPIRED", "COMPLETED", "LINK_EXPIRED", "CARD_EXPIRED":
		return gateway.MandateExpired
	default:
		return gateway.MandateAwaitingAuthorization
	}
}

func normalize(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }

// ==================== Pre-debit notification and debit ====================

func (a *Adapter) SendPreDebitNotification(ctx context.Context, req gateway.PreDebitNotificationRequest) (*gateway.PreDebitNotificationResult, error) {
	res, err := a.client.NotifyMandate(ctx, pkgcashfree.NotifyParams{
		SubscriptionID: req.Mandate.MerchantAutopayID, PaymentID: req.MerchantPaymentID, NotificationID: req.PreDebitNotificationID,
		AmountPaise: req.Amount, ScheduleDate: scheduleDateFor(req.EarliestDebitAt, a.now()),
		Remarks: utils.Truncate("autopay "+req.MerchantPaymentID, 60), IdempotencyKey: req.PreDebitNotificationID,
	})
	if err != nil {
		return nil, classify(err, true, false)
	}
	return &gateway.PreDebitNotificationResult{PreDebitNotificationID: req.PreDebitNotificationID, State: notificationState(res.Status)}, nil
}

func (a *Adapter) FetchPreDebitNotificationStatus(ctx context.Context, ref gateway.ChargeAttemptRef) (*gateway.PreDebitNotificationStatus, error) {
	n, err := a.client.FetchNotification(ctx, ref.MerchantPaymentID, ref.PreDebitNotificationID)
	if err != nil {
		return nil, classify(err, false, false)
	}
	st := &gateway.PreDebitNotificationStatus{State: notificationState(n.Status), FailureCode: n.Failure.ErrorCode, FailureDescription: n.Failure.Reason}
	if n.CompletedTime != nil {
		st.DeliveredAt = n.CompletedTime.UTC()
	}
	return st, nil
}

func notificationState(status string) gateway.NotificationState {
	switch normalize(status) {
	case pkgcashfree.StatusSuccess:
		return gateway.NotificationDelivered
	case pkgcashfree.StatusFailed, pkgcashfree.StatusCancelled:
		return gateway.NotificationFailed
	default:
		return gateway.NotificationPending
	}
}

func paymentState(status string) gateway.PaymentState {
	switch normalize(status) {
	case pkgcashfree.StatusSuccess:
		return gateway.PaymentSucceeded
	case pkgcashfree.StatusFailed, pkgcashfree.StatusCancelled:
		return gateway.PaymentFailed
	default:
		return gateway.PaymentPending
	}
}

func (a *Adapter) SubmitDebit(ctx context.Context, req gateway.DebitRequest) (*gateway.DebitSubmission, error) {
	res, err := a.client.ExecuteMandate(ctx, pkgcashfree.ExecuteParams{
		PaymentID: req.Attempt.MerchantPaymentID, ExecutionID: req.DebitExecutionID, IdempotencyKey: req.DebitExecutionID,
	})
	if err != nil {
		return nil, classify(err, true, true)
	}
	return &gateway.DebitSubmission{State: paymentState(res.Status), DebitExecutionID: req.DebitExecutionID}, nil
}

func (a *Adapter) FetchChargeAttemptStatus(ctx context.Context, ref gateway.ChargeAttemptRef) (*gateway.ChargeAttemptStatus, error) {
	out := &gateway.ChargeAttemptStatus{Notification: gateway.NotificationPending, Debit: gateway.PaymentPending}
	if ref.PreDebitNotificationID != "" {
		n, err := a.client.FetchNotification(ctx, ref.MerchantPaymentID, ref.PreDebitNotificationID)
		if err == nil {
			out.Notification = notificationState(n.Status)
			if n.CompletedTime != nil {
				out.NotificationDeliveredAt = n.CompletedTime.UTC()
			}
			out.FailureCode, out.FailureDescription = n.Failure.ErrorCode, n.Failure.Reason
		} else if apiErr, ok := pkgcashfree.AsAPIError(err); !ok || apiErr.IsRetryable() {
			return nil, classify(err, false, false)
		}
	}
	if ref.DebitExecutionID == "" {
		return out, nil // no debit was ever submitted
	}
	e, err := a.client.FetchExecution(ctx, ref.MerchantPaymentID, ref.DebitExecutionID)
	if err != nil {
		// Cashfree answers an unknown execution id with HTTP 400, not 404: a non-retryable API error means "no such debit".
		if apiErr, ok := pkgcashfree.AsAPIError(err); ok && !apiErr.IsRetryable() {
			return out, nil
		}
		return nil, classify(err, false, false)
	}
	out.DebitExists = true
	out.Debit = paymentState(e.Status)
	out.GatewayPaymentID = e.CFPaymentID
	if out.Debit == gateway.PaymentFailed {
		out.FailureCode, out.FailureDescription = e.Failure.ErrorCode, e.Failure.Reason
	}
	return out, nil
}

// scheduleDateFor picks the payment_schedule_date for a notification. Cashfree uses only the date and requires
// T+1..T+7 in IST, so the intended debit time is clamped into that range.
func scheduleDateFor(debitAt, now time.Time) time.Time {
	nowIST := now.In(pkgcashfree.IST)
	today := time.Date(nowIST.Year(), nowIST.Month(), nowIST.Day(), 0, 0, 0, 0, pkgcashfree.IST)
	minDate, maxDate := today.AddDate(0, 0, 1), today.AddDate(0, 0, 7)
	t := debitAt.In(pkgcashfree.IST)
	d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, pkgcashfree.IST)
	if d.Before(minDate) {
		return minDate
	}
	if d.After(maxDate) {
		return maxDate
	}
	return d
}

// ==================== One-time payments ====================

func (a *Adapter) CreateOneTimePayment(ctx context.Context, req gateway.OneTimePaymentRequest) (*gateway.OneTimePaymentSession, error) {
	if req.User.Phone == "" {
		return nil, adapterutil.Rejected("customer_details_missing", "user phone is required by Cashfree")
	}
	expiry := a.now().Add(oneTimeValidity)
	switch req.Kind {
	case gateway.OneTimePaymentKindPaymentLink:
		link, err := a.client.CreatePaymentLink(ctx, pkgcashfree.CreatePaymentLinkParams{
			LinkID: req.MerchantPaymentID, AmountPaise: req.Amount, Purpose: "One-time Payment", CustomerName: customerName(req.User.UserID),
			CustomerEmail: req.User.Email, CustomerPhone: req.User.Phone, ExpiryTime: expiry, ReturnURL: req.ReturnURL, IdempotencyKey: req.MerchantPaymentID,
		})
		if err != nil {
			return nil, classify(err, true, false)
		}
		if link.URL == "" {
			return nil, gateway.NewError(gateway.ErrorKindRejectedPermanently, "no_link_url", "cashfree did not return a link_url", nil)
		}
		return &gateway.OneTimePaymentSession{Kind: req.Kind, GatewayPaymentLinkID: req.MerchantPaymentID, LinkURL: link.URL}, nil
	default:
		order, err := a.client.CreateOrder(ctx, pkgcashfree.CreateOrderParams{
			OrderID: req.MerchantPaymentID, AmountPaise: req.Amount, CustomerID: req.User.UserID, CustomerName: customerName(req.User.UserID),
			CustomerEmail: req.User.Email, CustomerPhone: req.User.Phone, ExpiryTime: expiry, Note: "one_time_payment", ReturnURL: req.ReturnURL, IdempotencyKey: req.MerchantPaymentID,
		})
		if err != nil {
			return nil, classify(err, true, false)
		}
		if order.SessionID == "" {
			return nil, gateway.NewError(gateway.ErrorKindRejectedPermanently, "no_session", "cashfree did not return a payment_session_id", nil)
		}
		return &gateway.OneTimePaymentSession{
			Kind: gateway.OneTimePaymentKindOrder, GatewayOrderID: req.MerchantPaymentID,
			ClientPayload: map[string]any{"payment_session_id": order.SessionID, "order_id": req.MerchantPaymentID, "mode": a.cfg.Mode()},
		}, nil
	}
}

func (a *Adapter) FetchOneTimePaymentStatus(ctx context.Context, ref gateway.OneTimePaymentRef) (*gateway.OneTimePaymentStatus, error) {
	if ref.Kind == gateway.OneTimePaymentKindPaymentLink {
		link, err := a.client.FetchPaymentLink(ctx, ref.GatewayPaymentLinkID)
		if err != nil {
			return nil, classify(err, false, false)
		}
		st := &gateway.OneTimePaymentStatus{State: gateway.PaymentPending}
		switch normalize(link.Status) {
		case pkgcashfree.LinkStatusPaid:
			st.State = gateway.PaymentSucceeded
		case "EXPIRED", "CANCELLED":
			st.State, st.FailureCode = gateway.PaymentFailed, "link_"+strings.ToLower(link.Status)
		}
		return st, nil
	}
	order, err := a.client.FetchOrder(ctx, ref.GatewayOrderID)
	if err != nil {
		return nil, classify(err, false, false)
	}
	st := &gateway.OneTimePaymentStatus{State: gateway.PaymentPending, GatewayOrderID: order.OrderID}
	switch normalize(order.Status) {
	case pkgcashfree.OrderStatusPaid:
		st.State = gateway.PaymentSucceeded
	case "EXPIRED", "TERMINATED":
		st.State, st.FailureCode = gateway.PaymentFailed, "order_"+strings.ToLower(order.Status)
	}
	return st, nil
}

// ==================== Webhooks ====================

type envelope struct {
	Type      string                 `json:"type"`
	EventTime string                 `json:"event_time"`
	Data      map[string]interface{} `json:"data"`
}

// VerifyAndParseWebhook verifies base64(HMAC-SHA256(x-webhook-timestamp + rawBody, client secret)) and normalizes the
// event. The payload is applied directly by the core; events that only say a notification was *sent*
// (SUBSCRIPTION_PAYMENT_NOTIFICATION_INITIATED) produce no event, because "sent" is not "delivered" - the sync run
// confirms delivery by fetching it.
func (a *Adapter) VerifyAndParseWebhook(headers http.Header, rawBody []byte) ([]gateway.Event, error) {
	secret := a.cfg.Credentials["client_secret"]
	if err := pkgcashfree.VerifyWebhookSignature(secret, headers.Get("x-webhook-timestamp"), rawBody, headers.Get("x-webhook-signature")); err != nil {
		return nil, gateway.ErrInvalidSignature
	}
	var env envelope
	if err := json.Unmarshal(rawBody, &env); err != nil {
		return nil, fmt.Errorf("cashfree webhook is not valid JSON: %w", err)
	}
	return a.normalize(headers, &env)
}

func (a *Adapter) normalize(headers http.Header, env *envelope) ([]gateway.Event, error) {
	d := env.Data
	at := time.Time{}
	if t := pkgcashfree.ParseTime(env.EventTime); t != nil {
		at = t.UTC()
	}
	base := gateway.Event{
		GatewayEventType: env.Type, GatewayEventAt: at,
		MerchantAutopayID: utils.FirstNonEmpty(adapterutil.Dig(d, "subscription_id"), adapterutil.Dig(d, "subscription_details", "subscription_id")),
		GatewayMandateID:  utils.FirstNonEmpty(adapterutil.Dig(d, "cf_subscription_id"), adapterutil.Dig(d, "subscription_details", "cf_subscription_id")),
	}
	key := func(ev gateway.Event, status string) gateway.Event {
		ev.DedupeKey = utils.FirstNonEmpty(headers.Get("x-idempotency-key"),
			adapterutil.HashKey(env.Type, ev.MerchantAutopayID, ev.MerchantPaymentID, ev.GatewayPaymentID, ev.GatewayPaymentLinkID, status, env.EventTime))
		return ev
	}
	failure := func(path ...string) (code, desc string) {
		return utils.FirstNonEmpty(adapterutil.Dig(d, append(path, "failure_error_code")...), adapterutil.Dig(d, append(path, "failure_sub_error_code")...)),
			adapterutil.Dig(d, append(path, "failure_reason")...)
	}

	switch strings.ToUpper(env.Type) {
	case "SUBSCRIPTION_AUTH_STATUS":
		status := normalize(adapterutil.Dig(d, "authorization_details", "authorization_status"))
		ev := base
		ev.GatewayPaymentID = utils.FirstNonEmpty(adapterutil.Dig(d, "payment_id"), adapterutil.Dig(d, "authorization_details", "payment_id"))
		switch status {
		case "SUCCESS", "ACTIVE":
			ev.Type = gateway.EventMandateAuthorizationSucceeded
		case "FAILED":
			ev.Type, ev.FailureCode, ev.FailureDescription = gateway.EventMandateAuthorizationFailed, "authorization_failed", adapterutil.Dig(d, "failure_details", "failure_reason")
		default:
			return nil, nil
		}
		return []gateway.Event{key(ev, status)}, nil

	case "SUBSCRIPTION_STATUS_CHANGED":
		status := adapterutil.Dig(d, "subscription_details", "subscription_status")
		state := mandateState(status)
		if state == gateway.MandateAwaitingAuthorization {
			return nil, nil
		}
		ev := base
		ev.Type, ev.MandateState = gateway.EventMandateStatusChanged, state
		return []gateway.Event{key(ev, status)}, nil

	case "SUBSCRIPTION_CONTROLLED_NOTIFICATION_STATUS", "SUBSCRIPTION_PAYMENT_CONTROLLED_NOTIFICATION_STATUS":
		ev := base
		ev.MerchantPaymentID = adapterutil.Dig(d, "payment_id")
		status := adapterutil.Dig(d, "notification_status")
		switch notificationState(status) {
		case gateway.NotificationDelivered:
			ev.Type = gateway.EventPreDebitNotificationDelivered
			if t := pkgcashfree.ParseTime(adapterutil.Dig(d, "notification_completed_time")); t != nil {
				ev.DeliveredAt = t.UTC()
			}
		case gateway.NotificationFailed:
			ev.Type = gateway.EventPreDebitNotificationFailed
			ev.FailureCode, ev.FailureDescription = failure("notification_failure_details")
		default:
			return nil, nil
		}
		return []gateway.Event{key(ev, status)}, nil

	case "SUBSCRIPTION_CONTROLLED_EXECUTION_STATUS", "SUBSCRIPTION_PAYMENT_CONTROLLED_EXECUTION_STATUS":
		ev := base
		ev.MerchantPaymentID = adapterutil.Dig(d, "payment_id")
		status := adapterutil.Dig(d, "execution_status")
		switch paymentState(status) {
		case gateway.PaymentSucceeded:
			ev.Type = gateway.EventAutopayChargeSucceeded
		case gateway.PaymentFailed:
			ev.Type = gateway.EventAutopayChargeFailed
			ev.FailureCode, ev.FailureDescription = failure("failure_details")
		default:
			return nil, nil
		}
		return []gateway.Event{key(ev, status)}, nil

	case "SUBSCRIPTION_PAYMENT_SUCCESS", "SUBSCRIPTION_PAYMENT_FAILED", "SUBSCRIPTION_PAYMENT_CANCELLED":
		status := utils.FirstNonEmpty(adapterutil.Dig(d, "payment_status"), strings.TrimPrefix(strings.ToUpper(env.Type), "SUBSCRIPTION_PAYMENT_"))
		ok := paymentState(status) == gateway.PaymentSucceeded
		ev := base
		ev.GatewayPaymentID = adapterutil.Dig(d, "cf_payment_id")
		if amount, has := adapterutil.DigNumber(d, "payment_amount"); has {
			ev.Amount = pkgcashfree.RupeesToPaise(amount)
		}
		desc := adapterutil.Dig(d, "failure_details", "failure_reason")
		if strings.EqualFold(adapterutil.Dig(d, "payment_type"), "AUTH") { // Cashfree generates the authorization payment id: match through the subscription
			ev.GatewayPaymentID = adapterutil.Dig(d, "payment_id")
			if ok {
				ev.Type = gateway.EventMandateAuthorizationSucceeded
			} else {
				ev.Type, ev.FailureCode, ev.FailureDescription = gateway.EventMandateAuthorizationFailed, "authorization_failed", desc
			}
			return []gateway.Event{key(ev, status)}, nil
		}
		ev.MerchantPaymentID = adapterutil.Dig(d, "payment_id")
		if ok {
			ev.Type = gateway.EventAutopayChargeSucceeded
		} else {
			ev.Type, ev.FailureCode, ev.FailureDescription = gateway.EventAutopayChargeFailed, "payment_"+strings.ToLower(status), desc
		}
		return []gateway.Event{key(ev, status)}, nil

	case "PAYMENT_SUCCESS_WEBHOOK", "PAYMENT_FAILED_WEBHOOK":
		ev := gateway.Event{GatewayEventType: env.Type, GatewayEventAt: at, MerchantPaymentID: adapterutil.Dig(d, "order", "order_id"), GatewayOrderID: adapterutil.Dig(d, "order", "order_id"),
			GatewayPaymentID: adapterutil.Dig(d, "payment", "cf_payment_id")}
		if ev.MerchantPaymentID == "" {
			return nil, nil
		}
		if strings.EqualFold(env.Type, "PAYMENT_SUCCESS_WEBHOOK") {
			if st := normalize(adapterutil.Dig(d, "payment", "payment_status")); st != "" && st != pkgcashfree.StatusSuccess {
				return nil, nil
			}
			if amount, has := adapterutil.DigNumber(d, "order", "order_amount"); has {
				ev.Amount = pkgcashfree.RupeesToPaise(amount)
			}
			ev.Type = gateway.EventOneTimePaymentSucceeded
		} else {
			ev.Type = gateway.EventOneTimePaymentFailed
			ev.FailureCode = utils.FirstNonEmpty(adapterutil.Dig(d, "error_details", "error_code"), adapterutil.Dig(d, "payment", "error_details", "error_code"), "payment_failed")
			ev.FailureDescription = utils.FirstNonEmpty(adapterutil.Dig(d, "error_details", "error_description"), adapterutil.Dig(d, "payment", "error_details", "error_description"), adapterutil.Dig(d, "payment", "payment_message"))
		}
		return []gateway.Event{key(ev, env.Type)}, nil

	case "PAYMENT_LINK_EVENT":
		ev := gateway.Event{GatewayEventType: env.Type, GatewayEventAt: at, GatewayPaymentLinkID: adapterutil.Dig(d, "link_id"), MerchantPaymentID: adapterutil.Dig(d, "link_id")}
		status := normalize(adapterutil.Dig(d, "link_status"))
		switch status {
		case pkgcashfree.LinkStatusPaid:
			ev.Type = gateway.EventOneTimePaymentSucceeded
			if paid, has := adapterutil.DigNumber(d, "link_amount_paid"); has {
				ev.Amount = pkgcashfree.RupeesToPaise(paid)
			}
		case "EXPIRED", "CANCELLED":
			ev.Type, ev.FailureCode = gateway.EventOneTimePaymentFailed, "link_"+strings.ToLower(status)
		default:
			return nil, nil // ACTIVE, PARTIALLY_PAID
		}
		return []gateway.Event{key(ev, status)}, nil
	}
	return nil, nil // refunds, user-dropped, card expiry reminders, notification-initiated, ...
}
