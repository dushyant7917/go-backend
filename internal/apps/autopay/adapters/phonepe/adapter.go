// Package phonepe adapts PhonePe UPI Autopay (Standard Checkout, merchant-controlled on-demand mandates) to the
// autopay gateway contract. Every charge attempt is its own PhonePe order (merchantOrderId = our merchant payment id),
// so a retry is a new notify + redeem. PhonePe has no idempotency keys: a duplicate answer means "it may already
// exist", and the adapter reads the order instead of failing.
package phonepe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go-backend/internal/apps/autopay/adapters/adapterutil"
	"go-backend/internal/apps/autopay/gateway"
	"go-backend/internal/common/billingwindow"
	pkgphonepe "go-backend/pkg/phonepe"
	"go-backend/pkg/utils"
)

const (
	// authOrderExpiry is how long the customer has to approve the mandate in the PhonePe SDK.
	authOrderExpiry = 30 * time.Minute
	// oneTimeOrderExpiry is how long a one-time checkout order stays payable.
	oneTimeOrderExpiry = time.Hour
	// notifiedOrderGrace keeps a notified order redeemable beyond the earliest debit time (a missed night's catch-up).
	notifiedOrderGrace = 24 * time.Hour
)

// Adapter is the PhonePe implementation of gateway.Gateway for one merchant account.
type Adapter struct {
	cfg    gateway.Config
	client pkgphonepe.Client
}

// NewFactory returns the gateway.Factory for PhonePe. Pass pkgphonepe.NewFactory() in production; tests pass a fake.
func NewFactory(newClient pkgphonepe.ClientFactory) gateway.Factory {
	return func(cfg gateway.Config) (gateway.Gateway, error) {
		c := cfg.Credentials
		if c["client_id"] == "" || c["client_secret"] == "" || c["merchant_id"] == "" {
			return nil, errors.New("phonepe credentials need client_id, client_secret and merchant_id")
		}
		version := 1
		if v := c["client_version"]; v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				return nil, fmt.Errorf("phonepe client_version %q is not a number", v)
			}
			version = n
		}
		return &Adapter{cfg: cfg, client: newClient(pkgphonepe.Credentials{
			ClientID: c["client_id"], ClientSecret: c["client_secret"], ClientVersion: version, Sandbox: cfg.IsSandbox(),
		})}, nil
	}
}

func (a *Adapter) Name() gateway.Name { return gateway.NamePhonePe }

func (a *Adapter) Capabilities() gateway.Capabilities {
	return gateway.Capabilities{
		ChargeAttemptIdentity:       gateway.OrderPerChargeAttempt,
		SupportsIdempotencyKeys:     false,
		PushesMandateStatusWebhooks: true,
		OneTimePaymentKinds:         []string{gateway.OneTimePaymentKindOrder},
		SupportsMandateCancellation: true,
		MinAmountMinorUnits:         pkgphonepe.MinAmountPaise,
		MaxMandateAmountMinorUnits:  pkgphonepe.MaxMandateAmountPaise,
		MinNotificationToDebitGap:   billingwindow.Min,
		MinDebitRetryGap:            pkgphonepe.MinRedemptionRetryGap,
		RequestsPerMinuteLimit:      60, // NOT published: no rate limit found in PhonePe's PG docs (checked 2026-11-12); 60 is our chosen default, override per account with gateway_ratelimit_rpm
	}
}

func (a *Adapter) clientPayload(orderID, merchantOrderID, token string) map[string]any {
	return map[string]any{"order_id": orderID, "merchant_order_id": merchantOrderID, "token": token, "merchant_id": a.cfg.Credentials["merchant_id"], "mode": a.cfg.Mode()}
}

// ==================== Errors ====================

// classify maps a PhonePe error to a gateway.Error. A non-API error on a mutating call means the request was sent and
// no usable answer came back, so its outcome is unknown. debit says that an unrecognized 4xx is most likely a UPI
// restriction (blackout window, daily limit): temporary.
func classify(err error, mutating, debit bool) error {
	if err == nil {
		return nil
	}
	var ge *gateway.Error
	if errors.As(err, &ge) {
		return err
	}
	if apiErr, ok := pkgphonepe.AsAPIError(err); ok {
		switch {
		case apiErr.HTTPStatus == http.StatusTooManyRequests:
			return gateway.NewRateLimitedError(apiErr.Code, apiErr.Message, apiErr.RetryAfter, err)
		case apiErr.IsDuplicate():
			return gateway.NewError(gateway.ErrorKindAlreadyProcessed, apiErr.Code, apiErr.Message, err)
		case apiErr.IsRetryable():
			return gateway.NewError(gateway.ErrorKindTransient, apiErr.Code, apiErr.Message, err)
		case debit:
			return gateway.NewError(gateway.ErrorKindRejectedTemporarily, apiErr.Code, apiErr.Message, err)
		default:
			return gateway.NewError(gateway.ErrorKindRejectedPermanently, apiErr.Code, apiErr.Message, err)
		}
	}
	return adapterutil.TransportError(err, mutating)
}

func isNotFound(err error) bool {
	apiErr, ok := pkgphonepe.AsAPIError(err)
	return ok && apiErr.IsNotFound()
}

// ==================== Mandate ====================

func (a *Adapter) CreateMandateAuthorization(ctx context.Context, req gateway.MandateAuthorizationRequest) (*gateway.MandateAuthorizationSession, error) {
	order, err := a.client.CreateSubscriptionOrder(ctx, pkgphonepe.CreateSubscriptionOrderParams{
		MerchantOrderID: req.MerchantPaymentID, MerchantSubscriptionID: req.MerchantAutopayID,
		AmountPaise: req.AuthorizationAmount, MaxAmountPaise: req.MaxDebitAmount,
		SubscriptionExpiry: req.EndAt, OrderExpireAfter: authOrderExpiry,
	})
	if err != nil {
		return nil, classify(err, true, false)
	}
	return &gateway.MandateAuthorizationSession{
		GatewayOrderID: order.OrderID, // PhonePe issues the subscription id only once the customer starts the SDK
		ClientPayload:  a.clientPayload(order.OrderID, req.MerchantPaymentID, order.Token),
	}, nil
}

func (a *Adapter) FetchMandateAuthorizationStatus(ctx context.Context, ref gateway.MandateRef) (*gateway.MandateAuthorizationStatus, error) {
	sub, err := a.client.FetchSubscription(ctx, ref.MerchantAutopayID)
	if err != nil {
		if !isNotFound(err) {
			return nil, classify(err, false, false)
		}
		// PhonePe has no subscription until the customer opens the SDK; the order says whether the attempt died.
		if ref.AuthorizationMerchantPaymentID == "" {
			return &gateway.MandateAuthorizationStatus{State: gateway.AuthorizationPending}, nil
		}
		order, oerr := a.client.FetchOrder(ctx, ref.AuthorizationMerchantPaymentID)
		if oerr != nil {
			if isNotFound(oerr) {
				return &gateway.MandateAuthorizationStatus{State: gateway.AuthorizationPending}, nil
			}
			return nil, classify(oerr, false, false)
		}
		if order.State == pkgphonepe.OrderFailed || order.State == pkgphonepe.OrderExpired {
			code, detail := order.FailureCodes()
			return &gateway.MandateAuthorizationStatus{State: gateway.AuthorizationFailed, FailureCode: adapterutil.FailureCode(code, detail, "order_"+strings.ToLower(order.State)), FailureDescription: detail}, nil
		}
		return &gateway.MandateAuthorizationStatus{State: gateway.AuthorizationPending}, nil
	}
	st := &gateway.MandateAuthorizationStatus{State: gateway.AuthorizationPending, GatewayMandateID: sub.SubscriptionID}
	switch sub.State {
	case pkgphonepe.SubscriptionActive:
		st.State = gateway.AuthorizationSucceeded
	case pkgphonepe.SubscriptionFailed, pkgphonepe.SubscriptionExpired, pkgphonepe.SubscriptionCancelled, pkgphonepe.SubscriptionCancelInProgress,
		pkgphonepe.SubscriptionRevoked, pkgphonepe.SubscriptionRevokeInProgress:
		st.State, st.FailureCode = gateway.AuthorizationFailed, "mandate_"+strings.ToLower(sub.State)
	}
	return st, nil
}

func (a *Adapter) FetchMandateStatus(ctx context.Context, ref gateway.MandateRef) (*gateway.MandateStatus, error) {
	sub, err := a.client.FetchSubscription(ctx, ref.MerchantAutopayID)
	if err != nil {
		return nil, classify(err, false, false)
	}
	return &gateway.MandateStatus{State: mandateState(sub.State), GatewayMandateID: sub.SubscriptionID}, nil
}

// CancelMandate cancels the subscription. A cancel PhonePe rejects is settled by the subscription's own state: one that
// is already cancelled, revoked or expired is a success for the caller (see CancelAlreadyDone).
func (a *Adapter) CancelMandate(ctx context.Context, ref gateway.MandateRef) error {
	err := a.client.CancelSubscription(ctx, ref.MerchantAutopayID)
	if err == nil {
		return nil
	}
	return adapterutil.CancelAlreadyDone(classify(err, true, false), err, func() (gateway.MandateState, error) {
		sub, ferr := a.client.FetchSubscription(ctx, ref.MerchantAutopayID)
		if ferr != nil {
			return "", ferr
		}
		return mandateState(sub.State), nil
	})
}

// mandateState maps PhonePe's subscription state onto the core's mandate states.
func mandateState(state string) gateway.MandateState {
	switch strings.ToUpper(state) {
	case pkgphonepe.SubscriptionActive:
		return gateway.MandateActive
	case pkgphonepe.SubscriptionPaused, pkgphonepe.SubscriptionPauseInProgress, pkgphonepe.SubscriptionUnpauseInProgress:
		return gateway.MandatePaused
	case pkgphonepe.SubscriptionCancelled, pkgphonepe.SubscriptionCancelInProgress, pkgphonepe.SubscriptionRevoked, pkgphonepe.SubscriptionRevokeInProgress:
		return gateway.MandateCancelled
	case pkgphonepe.SubscriptionExpired, pkgphonepe.SubscriptionFailed:
		return gateway.MandateExpired
	default: // ACTIVATION_IN_PROGRESS
		return gateway.MandateAwaitingAuthorization
	}
}

// ==================== Pre-debit notification and debit ====================

func (a *Adapter) SendPreDebitNotification(ctx context.Context, req gateway.PreDebitNotificationRequest) (*gateway.PreDebitNotificationResult, error) {
	res, err := a.client.NotifyRedemption(ctx, pkgphonepe.NotifyParams{
		MerchantOrderID: req.MerchantPaymentID, MerchantSubscriptionID: req.Mandate.MerchantAutopayID, AmountPaise: req.Amount,
		// The debit cannot happen before the notice has aged, so the order must stay redeemable until then (+ catch-up).
		ExpireAt: req.EarliestDebitAt.Add(notifiedOrderGrace), RetryStrategy: pkgphonepe.RetryStrategyCustom,
	})
	if err != nil {
		apiErr, isAPI := pkgphonepe.AsAPIError(err)
		if !isAPI || !apiErr.IsDuplicate() {
			return nil, classify(err, true, false)
		}
		// PhonePe has no idempotency keys: a duplicate means an earlier notice (whose answer we lost) created the order.
		order, ferr := a.client.FetchOrder(ctx, req.MerchantPaymentID)
		if ferr != nil {
			return nil, classify(ferr, false, false)
		}
		st := notificationOf(order, false)
		return &gateway.PreDebitNotificationResult{GatewayOrderID: order.OrderID, PreDebitNotificationID: req.PreDebitNotificationID, State: st.State, DeliveredAt: st.DeliveredAt, ValidAfter: st.ValidAfter}, nil
	}
	state := gateway.NotificationPending
	if strings.EqualFold(res.State, pkgphonepe.OrderNotified) {
		state = gateway.NotificationDelivered // already final in the synchronous answer; no timestamp, so the core uses the send time
	}
	return &gateway.PreDebitNotificationResult{GatewayOrderID: res.OrderID, PreDebitNotificationID: req.PreDebitNotificationID, State: state}, nil
}

// notificationOf reads the notification outcome from an order. An order that has moved past the notification stage
// (redeemed or finished) was necessarily notified; an order that died before any debit was submitted died as a
// failed notification.
func notificationOf(order *pkgphonepe.Order, debitSubmitted bool) *gateway.PreDebitNotificationStatus {
	st := &gateway.PreDebitNotificationStatus{State: gateway.NotificationPending}
	if !order.NotifiedAt.IsZero() {
		st.DeliveredAt = order.NotifiedAt.UTC()
	}
	if !order.ValidAfter.IsZero() {
		st.ValidAfter = order.ValidAfter.UTC()
	}
	switch order.State {
	case pkgphonepe.OrderNotified, pkgphonepe.OrderPending, pkgphonepe.OrderCompleted:
		st.State = gateway.NotificationDelivered
	case pkgphonepe.OrderFailed, pkgphonepe.OrderExpired:
		if debitSubmitted {
			st.State = gateway.NotificationDelivered
		} else {
			code, detail := order.FailureCodes()
			st.State, st.FailureCode, st.FailureDescription = gateway.NotificationFailed, adapterutil.FailureCode(code, detail, "notification_failed"), detail
		}
	}
	return st
}

func (a *Adapter) FetchPreDebitNotificationStatus(ctx context.Context, ref gateway.ChargeAttemptRef) (*gateway.PreDebitNotificationStatus, error) {
	order, err := a.client.FetchOrder(ctx, ref.MerchantPaymentID)
	if err != nil {
		if isNotFound(err) {
			return &gateway.PreDebitNotificationStatus{State: gateway.NotificationPending}, nil
		}
		return nil, classify(err, false, false)
	}
	return notificationOf(order, ref.DebitExecutionID != ""), nil
}

func paymentState(state string) gateway.PaymentState {
	switch strings.ToUpper(state) {
	case pkgphonepe.OrderCompleted:
		return gateway.PaymentSucceeded
	case pkgphonepe.OrderFailed, pkgphonepe.OrderExpired:
		return gateway.PaymentFailed
	default:
		return gateway.PaymentPending
	}
}

func (a *Adapter) SubmitDebit(ctx context.Context, req gateway.DebitRequest) (*gateway.DebitSubmission, error) {
	res, err := a.client.Redeem(ctx, req.Attempt.MerchantPaymentID)
	if err != nil {
		return nil, classify(err, true, true)
	}
	return &gateway.DebitSubmission{State: paymentState(res.State), GatewayPaymentID: res.TransactionID, DebitExecutionID: req.DebitExecutionID}, nil
}

func (a *Adapter) FetchChargeAttemptStatus(ctx context.Context, ref gateway.ChargeAttemptRef) (*gateway.ChargeAttemptStatus, error) {
	out := &gateway.ChargeAttemptStatus{Notification: gateway.NotificationPending, Debit: gateway.PaymentPending}
	order, err := a.client.FetchOrder(ctx, ref.MerchantPaymentID)
	if err != nil {
		if isNotFound(err) {
			return out, nil // PhonePe has no such order: nothing was notified and nothing was debited
		}
		return nil, classify(err, false, false)
	}
	out.GatewayOrderID = order.OrderID
	n := notificationOf(order, ref.DebitExecutionID != "")
	out.Notification, out.NotificationDeliveredAt, out.ValidAfter = n.State, n.DeliveredAt, n.ValidAfter
	if n.State == gateway.NotificationFailed {
		out.FailureCode, out.FailureDescription = n.FailureCode, n.FailureDescription
		return out, nil
	}
	// An order still in the notification stage has not been redeemed: there is no debit to speak of.
	if order.State == pkgphonepe.OrderNotified || order.State == pkgphonepe.OrderNotificationInProgress {
		return out, nil
	}
	out.DebitExists = ref.DebitExecutionID != "" || order.State == pkgphonepe.OrderCompleted || len(order.Attempts) > 0
	out.Debit = paymentState(order.State)
	if t := order.LatestAttempt(); t != nil {
		out.GatewayPaymentID = t.TransactionID
	}
	if out.Debit == gateway.PaymentFailed {
		code, detail := order.FailureCodes()
		out.FailureCode, out.FailureDescription = adapterutil.FailureCode(code, detail, "execution_"+strings.ToLower(order.State)), detail
	}
	return out, nil
}

// ==================== One-time payments ====================

func (a *Adapter) CreateOneTimePayment(ctx context.Context, req gateway.OneTimePaymentRequest) (*gateway.OneTimePaymentSession, error) {
	if req.Kind != "" && req.Kind != gateway.OneTimePaymentKindOrder {
		return nil, gateway.ErrUnsupported
	}
	order, err := a.client.CreateOneTimeOrder(ctx, pkgphonepe.CreateOneTimeOrderParams{
		MerchantOrderID: req.MerchantPaymentID, AmountPaise: req.Amount, OrderExpireAfter: oneTimeOrderExpiry, RedirectURL: req.ReturnURL,
	})
	if err != nil {
		return nil, classify(err, true, false)
	}
	return &gateway.OneTimePaymentSession{
		Kind: gateway.OneTimePaymentKindOrder, GatewayOrderID: order.OrderID,
		ClientPayload: a.clientPayload(order.OrderID, req.MerchantPaymentID, order.Token),
	}, nil
}

func (a *Adapter) FetchOneTimePaymentStatus(ctx context.Context, ref gateway.OneTimePaymentRef) (*gateway.OneTimePaymentStatus, error) {
	order, err := a.client.FetchOrder(ctx, ref.MerchantPaymentID)
	if err != nil {
		return nil, classify(err, false, false)
	}
	st := &gateway.OneTimePaymentStatus{State: gateway.PaymentPending, GatewayOrderID: order.OrderID}
	if t := order.LatestAttempt(); t != nil {
		st.GatewayPaymentID = t.TransactionID
	}
	switch order.State {
	case pkgphonepe.OrderCompleted:
		st.State = gateway.PaymentSucceeded
	case pkgphonepe.OrderFailed, pkgphonepe.OrderExpired:
		code, detail := order.FailureCodes()
		st.State, st.FailureCode, st.FailureDescription = gateway.PaymentFailed, adapterutil.FailureCode(code, detail, "order_"+strings.ToLower(order.State)), detail
	}
	return st, nil
}

// ==================== Webhooks ====================

// VerifyAndParseWebhook verifies the HMAC-SHA256 of the raw body under the config's webhook secret (header
// phonepe-checksum-signature, with or without the x- prefix) and normalizes the event. PhonePe documents no event id
// and no ordering, so the dedupe key hashes the identifying payload fields, and a transaction-level failure that does
// not end the order produces no event (the order's own state is what counts).
func (a *Adapter) VerifyAndParseWebhook(headers http.Header, rawBody []byte) ([]gateway.Event, error) {
	sig := utils.FirstNonEmpty(headers.Get("phonepe-checksum-signature"), headers.Get("x-phonepe-checksum-signature"))
	if !pkgphonepe.VerifyWebhookSignature(a.cfg.WebhookSecret, rawBody, sig) {
		return nil, gateway.ErrInvalidSignature
	}
	var env struct {
		Event   string                 `json:"event"`
		Type    string                 `json:"type"` // legacy name of event
		Payload map[string]interface{} `json:"payload"`
	}
	if err := json.Unmarshal(rawBody, &env); err != nil {
		return nil, fmt.Errorf("phonepe webhook is not valid JSON: %w", err)
	}
	event := strings.ToLower(utils.FirstNonEmpty(env.Event, env.Type))
	p := env.Payload

	orderID := adapterutil.Dig(p, "orderId")
	merchantOrderID := adapterutil.Dig(p, "merchantOrderId")
	merchantSubscriptionID := utils.FirstNonEmpty(adapterutil.Dig(p, "merchantSubscriptionId"), adapterutil.Dig(p, "paymentFlow", "merchantSubscriptionId"))
	subscriptionID := utils.FirstNonEmpty(adapterutil.Dig(p, "subscriptionId"), adapterutil.Dig(p, "paymentFlow", "subscriptionId"))
	state := strings.ToUpper(adapterutil.Dig(p, "state"))
	amount := int64(0)
	if n, ok := adapterutil.DigNumber(p, "amount"); ok {
		amount = int64(n)
	}
	code, detail := adapterutil.Dig(p, "errorCode"), adapterutil.Dig(p, "detailedErrorCode")
	latestTxn, latestAt, txnCode, txnDetail := latestTransaction(p)
	if code == "" {
		code, detail = txnCode, txnDetail
	}

	base := gateway.Event{
		GatewayEventType: env.Event, GatewayEventAt: latestAt,
		MerchantAutopayID: merchantSubscriptionID, MerchantPaymentID: merchantOrderID, GatewayMandateID: subscriptionID,
		GatewayOrderID: orderID, GatewayPaymentID: latestTxn,
	}
	emit := func(t gateway.EventType, mutate func(*gateway.Event)) []gateway.Event {
		ev := base
		ev.Type = t
		if mutate != nil {
			mutate(&ev)
		}
		ev.DedupeKey = adapterutil.HashKey(event, orderID, merchantOrderID, merchantSubscriptionID, state, latestTxn, strconv.FormatInt(latestAt.UnixMilli(), 10))
		return []gateway.Event{ev}
	}
	failed := func(ev *gateway.Event) {
		ev.FailureCode, ev.FailureDescription = adapterutil.FailureCode(code, detail, "payment_failed"), detail
	}

	switch event {
	case "subscription.setup.order.completed":
		return emit(gateway.EventMandateAuthorizationSucceeded, nil), nil
	case "subscription.setup.order.failed":
		return emit(gateway.EventMandateAuthorizationFailed, failed), nil

	case "checkout.order.completed", "checkout.order.failed":
		completed := event == "checkout.order.completed"
		if state != "" && completed != (state == pkgphonepe.OrderCompleted) {
			return nil, nil // the body contradicts the event name: wait for the order's real state (sync)
		}
		// A checkout order is the mandate setup order when it carries a subscription, else a one-time payment.
		isSetup := strings.Contains(strings.ToUpper(adapterutil.Dig(p, "paymentFlow", "type")), "SUBSCRIPTION")
		switch {
		case isSetup && completed:
			return emit(gateway.EventMandateAuthorizationSucceeded, nil), nil
		case isSetup:
			return emit(gateway.EventMandateAuthorizationFailed, failed), nil
		case completed:
			return emit(gateway.EventOneTimePaymentSucceeded, func(ev *gateway.Event) { ev.Amount = amount }), nil
		default:
			return emit(gateway.EventOneTimePaymentFailed, failed), nil
		}

	case "subscription.notification.completed":
		return emit(gateway.EventPreDebitNotificationDelivered, func(ev *gateway.Event) {
			if ms, ok := adapterutil.DigNumber(p, "paymentFlow", "notifiedAt"); ok && ms > 0 {
				ev.DeliveredAt = time.UnixMilli(int64(ms)).UTC()
			}
		}), nil
	case "subscription.notification.failed":
		return emit(gateway.EventPreDebitNotificationFailed, func(ev *gateway.Event) {
			ev.FailureCode, ev.FailureDescription = adapterutil.FailureCode(code, detail, "notification_failed"), detail
		}), nil

	case "subscription.redemption.order.completed":
		return emit(gateway.EventAutopayChargeSucceeded, func(ev *gateway.Event) { ev.Amount = amount }), nil
	case "subscription.redemption.order.failed", "subscription.redemption.order.cancelled":
		return emit(gateway.EventAutopayChargeFailed, func(ev *gateway.Event) {
			ev.FailureCode, ev.FailureDescription = adapterutil.FailureCode(code, detail, "execution_"+strings.TrimPrefix(event, "subscription.redemption.order.")), detail
		}), nil

	case "subscription.redemption.transaction.completed", "subscription.redemption.transaction.failed":
		// Docs show a transaction event arriving while the order is still PENDING (and under CUSTOM retry a failed
		// transaction does not end the order): only the order's own state counts, so act on it when the payload
		// says the order is terminal and otherwise leave it to the order events and the sync.
		switch state {
		case pkgphonepe.OrderCompleted:
			return emit(gateway.EventAutopayChargeSucceeded, func(ev *gateway.Event) { ev.Amount = amount }), nil
		case pkgphonepe.OrderFailed:
			return emit(gateway.EventAutopayChargeFailed, failed), nil
		}
		return nil, nil

	case "subscription.paused":
		return emit(gateway.EventMandateStatusChanged, func(ev *gateway.Event) { ev.MandateState = gateway.MandatePaused }), nil
	case "subscription.unpaused":
		return emit(gateway.EventMandateStatusChanged, func(ev *gateway.Event) { ev.MandateState = gateway.MandateActive }), nil
	case "subscription.revoked", "subscription.cancelled":
		return emit(gateway.EventMandateStatusChanged, func(ev *gateway.Event) { ev.MandateState = gateway.MandateCancelled }), nil
	}
	return nil, nil // pg.refund.*, unknown future events
}

// latestTransaction returns the newest payment try of the payload: its id, time (UTC) and failure codes.
func latestTransaction(p map[string]interface{}) (id string, at time.Time, code, detail string) {
	list, _ := p["paymentDetails"].([]interface{})
	var best int64 = -1
	for _, item := range list {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		ts, _ := adapterutil.DigNumber(m, "timestamp")
		if int64(ts) > best {
			best = int64(ts)
			id = adapterutil.Dig(m, "transactionId")
			code, detail = adapterutil.Dig(m, "errorCode"), adapterutil.Dig(m, "detailedErrorCode")
			if ts > 0 {
				at = time.UnixMilli(int64(ts)).UTC()
			}
		}
	}
	return id, at, code, detail
}
