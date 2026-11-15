package phonepe

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"go-backend/internal/apps/autopay/gateway"
	pkgphonepe "go-backend/pkg/phonepe"
)

// stub embeds the Client interface so a test overrides only the calls it exercises.
type stub struct {
	pkgphonepe.Client
	notify  func() (*pkgphonepe.NotifyResult, error)
	redeem  func() (*pkgphonepe.RedeemResult, error)
	order   func() (*pkgphonepe.Order, error)
	subscr  func() (*pkgphonepe.Subscription, error)
	cancel  func() error
	notifyN int
}

func (s *stub) NotifyRedemption(context.Context, pkgphonepe.NotifyParams) (*pkgphonepe.NotifyResult, error) {
	s.notifyN++
	return s.notify()
}
func (s *stub) Redeem(context.Context, string) (*pkgphonepe.RedeemResult, error) { return s.redeem() }
func (s *stub) FetchOrder(context.Context, string) (*pkgphonepe.Order, error)    { return s.order() }
func (s *stub) FetchSubscription(context.Context, string) (*pkgphonepe.Subscription, error) {
	return s.subscr()
}

func (s *stub) CancelSubscription(context.Context, string) error { return s.cancel() }

func newAdapter(t *testing.T, c pkgphonepe.Client) *Adapter {
	t.Helper()
	g, err := NewFactory(func(pkgphonepe.Credentials) pkgphonepe.Client { return c })(gateway.Config{
		Gateway: gateway.NamePhonePe, Environment: "local", WebhookSecret: "whsec",
		Credentials: map[string]string{"client_id": "id", "client_secret": "s", "client_version": "1", "merchant_id": "M1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return g.(*Adapter)
}

func TestFactoryRequiresCredentials(t *testing.T) {
	_, err := NewFactory(pkgphonepe.NewFactory())(gateway.Config{Credentials: map[string]string{"client_id": "x", "client_secret": "y"}})
	if err == nil {
		t.Fatal("expected an error without merchant_id")
	}
}

func parse(t *testing.T, body string) []gateway.Event {
	t.Helper()
	a := newAdapter(t, nil)
	h := http.Header{}
	h.Set("phonepe-checksum-signature", pkgphonepe.SignWebhook("whsec", []byte(body)))
	evs, err := a.VerifyAndParseWebhook(h, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	return evs
}

func TestWebhookSignatureIsVerifiedFirst(t *testing.T) {
	a := newAdapter(t, nil)
	body := []byte(`{"event":"subscription.paused","payload":{}}`)
	if _, err := a.VerifyAndParseWebhook(http.Header{}, body); !errors.Is(err, gateway.ErrInvalidSignature) {
		t.Fatalf("missing signature: want ErrInvalidSignature, got %v", err)
	}
	h := http.Header{}
	h.Set("x-phonepe-checksum-signature", pkgphonepe.SignWebhook("wrong", body))
	if _, err := a.VerifyAndParseWebhook(h, body); !errors.Is(err, gateway.ErrInvalidSignature) {
		t.Fatalf("wrong secret: want ErrInvalidSignature, got %v", err)
	}
	h.Set("x-phonepe-checksum-signature", pkgphonepe.SignWebhook("whsec", body))
	if _, err := a.VerifyAndParseWebhook(h, body); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
}

func TestWebhookEventMapping(t *testing.T) {
	cases := []struct {
		name string
		body string
		want gateway.EventType
		chk  func(*testing.T, gateway.Event)
	}{
		{"setup completed", `{"event":"subscription.setup.order.completed","payload":{"merchantOrderId":"pay0","state":"COMPLETED","paymentFlow":{"merchantSubscriptionId":"sub1","subscriptionId":"OMS1"}}}`,
			gateway.EventMandateAuthorizationSucceeded, func(t *testing.T, e gateway.Event) {
				if e.MerchantAutopayID != "sub1" || e.GatewayMandateID != "OMS1" {
					t.Fatalf("bad ids %+v", e)
				}
			}},
		{"setup order as checkout event", `{"event":"checkout.order.failed","payload":{"merchantOrderId":"pay0","state":"FAILED","errorCode":"X","paymentFlow":{"type":"SUBSCRIPTION_SETUP","merchantSubscriptionId":"sub1"}}}`,
			gateway.EventMandateAuthorizationFailed, nil},
		{"one-time completed", `{"event":"checkout.order.completed","payload":{"merchantOrderId":"ot1","orderId":"OM1","state":"COMPLETED","amount":4900}}`,
			gateway.EventOneTimePaymentSucceeded, func(t *testing.T, e gateway.Event) {
				if e.Amount != 4900 || e.MerchantPaymentID != "ot1" || e.GatewayOrderID != "OM1" {
					t.Fatalf("bad event %+v", e)
				}
			}},
		{"notification completed", `{"event":"subscription.notification.completed","payload":{"merchantOrderId":"pay1","state":"NOTIFIED","paymentFlow":{"merchantSubscriptionId":"sub1","notifiedAt":1795000000000}}}`,
			gateway.EventPreDebitNotificationDelivered, func(t *testing.T, e gateway.Event) {
				if !e.DeliveredAt.Equal(time.UnixMilli(1795000000000)) || e.DeliveredAt.Location() != time.UTC {
					t.Fatalf("delivered_at %v", e.DeliveredAt)
				}
			}},
		{"redemption order completed", `{"event":"subscription.redemption.order.completed","payload":{"merchantOrderId":"pay1","state":"COMPLETED","amount":19900,"paymentDetails":[{"transactionId":"T1","timestamp":1795000000000,"state":"COMPLETED"}]}}`,
			gateway.EventAutopayChargeSucceeded, func(t *testing.T, e gateway.Event) {
				if e.Amount != 19900 || e.GatewayPaymentID != "T1" || e.GatewayEventAt.IsZero() {
					t.Fatalf("bad event %+v", e)
				}
			}},
		{"redemption order failed", `{"event":"subscription.redemption.order.failed","payload":{"merchantOrderId":"pay1","state":"FAILED","errorCode":"AUTHORIZATION_ERROR","detailedErrorCode":"UPI_BLACKOUT"}}`,
			gateway.EventAutopayChargeFailed, func(t *testing.T, e gateway.Event) {
				if e.FailureCode != "AUTHORIZATION_ERROR" || e.FailureDescription != "UPI_BLACKOUT" {
					t.Fatalf("bad failure %+v", e)
				}
			}},
		{"transaction failed and order terminal", `{"event":"subscription.redemption.transaction.failed","payload":{"merchantOrderId":"pay1","state":"FAILED","paymentDetails":[{"transactionId":"T2","timestamp":1,"errorCode":"E"}]}}`,
			gateway.EventAutopayChargeFailed, nil},
		{"paused", `{"event":"subscription.paused","payload":{"merchantSubscriptionId":"sub1","state":"PAUSED"}}`, gateway.EventMandateStatusChanged,
			func(t *testing.T, e gateway.Event) {
				if e.MandateState != gateway.MandatePaused {
					t.Fatalf("state %v", e.MandateState)
				}
			}},
		{"revoked", `{"event":"subscription.revoked","payload":{"merchantSubscriptionId":"sub1"}}`, gateway.EventMandateStatusChanged,
			func(t *testing.T, e gateway.Event) {
				if e.MandateState != gateway.MandateCancelled {
					t.Fatalf("state %v", e.MandateState)
				}
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			evs := parse(t, c.body)
			if len(evs) != 1 || evs[0].Type != c.want {
				t.Fatalf("want one %s, got %+v", c.want, evs)
			}
			if evs[0].DedupeKey == "" {
				t.Fatal("event must carry a dedupe key")
			}
			if c.chk != nil {
				c.chk(t, evs[0])
			}
		})
	}
}

func TestTransactionEventWhileOrderPendingIsNotAFailure(t *testing.T) {
	for _, body := range []string{
		`{"event":"subscription.redemption.transaction.failed","payload":{"merchantOrderId":"pay1","state":"PENDING","paymentDetails":[{"transactionId":"T1","timestamp":1,"state":"FAILED"}]}}`,
		`{"event":"subscription.redemption.transaction.completed","payload":{"merchantOrderId":"pay1","state":"PENDING"}}`,
		`{"event":"pg.refund.completed","payload":{}}`,
		`{"event":"checkout.order.completed","payload":{"merchantOrderId":"x","state":"FAILED"}}`, // body contradicts the event name
	} {
		if evs := parse(t, body); len(evs) != 0 {
			t.Fatalf("expected no event for %s, got %+v", body, evs)
		}
	}
}

func TestDedupeKeyStableAndOutcomeSensitive(t *testing.T) {
	ok := `{"event":"subscription.redemption.order.completed","payload":{"merchantOrderId":"pay1","state":"COMPLETED"}}`
	bad := `{"event":"subscription.redemption.order.failed","payload":{"merchantOrderId":"pay1","state":"FAILED"}}`
	first, again := parse(t, ok)[0].DedupeKey, parse(t, ok)[0].DedupeKey
	if first != again {
		t.Fatal("the same delivery must produce the same key")
	}
	if first == parse(t, bad)[0].DedupeKey {
		t.Fatal("different outcomes must not share a key")
	}
}

func TestNotifyDuplicateAdoptsExistingOrder(t *testing.T) {
	s := &stub{
		notify: func() (*pkgphonepe.NotifyResult, error) {
			return nil, &pkgphonepe.APIError{HTTPStatus: 409, Code: "DUPLICATE_ORDER"}
		},
		order: func() (*pkgphonepe.Order, error) {
			return &pkgphonepe.Order{OrderID: "OM1", State: pkgphonepe.OrderNotified, NotifiedAt: time.Unix(1795000000, 0), ValidAfter: time.Unix(1795086400, 0)}, nil
		},
	}
	res, err := newAdapter(t, s).SendPreDebitNotification(context.Background(), gateway.PreDebitNotificationRequest{
		MerchantPaymentID: "pay1", PreDebitNotificationID: "n1", Mandate: gateway.MandateRef{MerchantAutopayID: "sub1"}, EarliestDebitAt: time.Now().Add(26 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.State != gateway.NotificationDelivered || res.GatewayOrderID != "OM1" || res.ValidAfter.IsZero() || res.DeliveredAt.IsZero() {
		t.Fatalf("duplicate must adopt the existing order: %+v", res)
	}
}

func TestRedeemErrorClassification(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want gateway.ErrorKind
	}{
		{"transport error is unknown", errors.New("read: connection reset by peer"), gateway.ErrorKindOutcomeUnknown},
		{"duplicate means already processed", &pkgphonepe.APIError{HTTPStatus: 409, Code: "DUPLICATE_REQUEST_ID"}, gateway.ErrorKindAlreadyProcessed},
		{"5xx transient", &pkgphonepe.APIError{HTTPStatus: 502}, gateway.ErrorKindTransient},
		{"429 transient", &pkgphonepe.APIError{HTTPStatus: 429}, gateway.ErrorKindTransient},
		{"4xx is temporary on a debit", &pkgphonepe.APIError{HTTPStatus: 400, Code: "UPI_BLACKOUT"}, gateway.ErrorKindRejectedTemporarily},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &stub{redeem: func() (*pkgphonepe.RedeemResult, error) { return nil, c.err }}
			_, err := newAdapter(t, s).SubmitDebit(context.Background(), gateway.DebitRequest{Attempt: gateway.ChargeAttemptRef{MerchantPaymentID: "pay1"}, DebitExecutionID: "e1"})
			var ge *gateway.Error
			if !errors.As(err, &ge) || ge.Kind != c.want {
				t.Fatalf("want %s, got %v", c.want, err)
			}
		})
	}
}

func TestFetchChargeAttemptStatus(t *testing.T) {
	fetch := func(t *testing.T, o *pkgphonepe.Order, err error, ref gateway.ChargeAttemptRef) *gateway.ChargeAttemptStatus {
		t.Helper()
		a := newAdapter(t, &stub{order: func() (*pkgphonepe.Order, error) { return o, err }})
		ref.MerchantPaymentID = "pay1"
		st, e := a.FetchChargeAttemptStatus(context.Background(), ref)
		if e != nil {
			t.Fatal(e)
		}
		return st
	}
	t.Run("not found: nothing notified, nothing debited", func(t *testing.T) {
		st := fetch(t, nil, &pkgphonepe.APIError{HTTPStatus: 404}, gateway.ChargeAttemptRef{DebitExecutionID: "e"})
		if st.DebitExists || st.Notification != gateway.NotificationPending {
			t.Fatalf("%+v", st)
		}
	})
	t.Run("notified, never redeemed", func(t *testing.T) {
		st := fetch(t, &pkgphonepe.Order{State: pkgphonepe.OrderNotified}, nil, gateway.ChargeAttemptRef{})
		if st.Notification != gateway.NotificationDelivered || st.DebitExists {
			t.Fatalf("%+v", st)
		}
	})
	t.Run("order died before any debit: failed notification", func(t *testing.T) {
		st := fetch(t, &pkgphonepe.Order{State: pkgphonepe.OrderFailed, ErrorCode: "NOTIFY_FAILED"}, nil, gateway.ChargeAttemptRef{})
		if st.Notification != gateway.NotificationFailed || st.FailureCode != "NOTIFY_FAILED" {
			t.Fatalf("%+v", st)
		}
	})
	t.Run("redeemed and failed", func(t *testing.T) {
		st := fetch(t, &pkgphonepe.Order{State: pkgphonepe.OrderFailed, ErrorCode: "INSUFFICIENT"}, nil, gateway.ChargeAttemptRef{DebitExecutionID: "e"})
		if st.Notification != gateway.NotificationDelivered || !st.DebitExists || st.Debit != gateway.PaymentFailed || st.FailureCode != "INSUFFICIENT" {
			t.Fatalf("%+v", st)
		}
	})
	t.Run("redeemed and completed", func(t *testing.T) {
		st := fetch(t, &pkgphonepe.Order{State: pkgphonepe.OrderCompleted, Attempts: []pkgphonepe.PaymentAttempt{{TransactionID: "T9"}}}, nil, gateway.ChargeAttemptRef{DebitExecutionID: "e"})
		if st.Debit != gateway.PaymentSucceeded || st.GatewayPaymentID != "T9" {
			t.Fatalf("%+v", st)
		}
	})
	t.Run("a gateway outage is an error, not 'no debit'", func(t *testing.T) {
		a := newAdapter(t, &stub{order: func() (*pkgphonepe.Order, error) { return nil, &pkgphonepe.APIError{HTTPStatus: 503} }})
		if _, err := a.FetchChargeAttemptStatus(context.Background(), gateway.ChargeAttemptRef{MerchantPaymentID: "pay1"}); err == nil {
			t.Fatal("expected an error")
		}
	})
}

func TestAuthorizationStatus(t *testing.T) {
	notFound := func() (*pkgphonepe.Subscription, error) { return nil, &pkgphonepe.APIError{HTTPStatus: 404} }
	ref := gateway.MandateRef{MerchantAutopayID: "sub1", AuthorizationMerchantPaymentID: "pay0"}

	st, err := newAdapter(t, &stub{subscr: notFound, order: func() (*pkgphonepe.Order, error) {
		return &pkgphonepe.Order{State: pkgphonepe.OrderFailed, ErrorCode: "USER_CANCELLED"}, nil
	}}).FetchMandateAuthorizationStatus(context.Background(), ref)
	if err != nil || st.State != gateway.AuthorizationFailed || st.FailureCode != "USER_CANCELLED" {
		t.Fatalf("a failed setup order must fail the authorization: %+v %v", st, err)
	}

	st, err = newAdapter(t, &stub{subscr: notFound, order: func() (*pkgphonepe.Order, error) { return &pkgphonepe.Order{State: pkgphonepe.OrderPending}, nil }}).
		FetchMandateAuthorizationStatus(context.Background(), ref)
	if err != nil || st.State != gateway.AuthorizationPending {
		t.Fatalf("a pending setup order is pending: %+v %v", st, err)
	}

	st, err = newAdapter(t, &stub{subscr: func() (*pkgphonepe.Subscription, error) {
		return &pkgphonepe.Subscription{State: pkgphonepe.SubscriptionActive, SubscriptionID: "OMS1"}, nil
	}}).FetchMandateAuthorizationStatus(context.Background(), ref)
	if err != nil || st.State != gateway.AuthorizationSucceeded || st.GatewayMandateID != "OMS1" {
		t.Fatalf("active subscription: %+v %v", st, err)
	}
}

func TestMandateStateMapping(t *testing.T) {
	for state, want := range map[string]gateway.MandateState{
		pkgphonepe.SubscriptionActive: gateway.MandateActive, pkgphonepe.SubscriptionPaused: gateway.MandatePaused,
		pkgphonepe.SubscriptionRevoked: gateway.MandateCancelled, pkgphonepe.SubscriptionCancelInProgress: gateway.MandateCancelled,
		pkgphonepe.SubscriptionExpired: gateway.MandateExpired, pkgphonepe.SubscriptionActivationInProgress: gateway.MandateAwaitingAuthorization,
	} {
		if got := mandateState(state); got != want {
			t.Fatalf("%s -> %s, want %s", state, got, want)
		}
	}
}

func TestOneTimePaymentLinkUnsupported(t *testing.T) {
	_, err := newAdapter(t, &stub{}).CreateOneTimePayment(context.Background(), gateway.OneTimePaymentRequest{Kind: gateway.OneTimePaymentKindPaymentLink})
	if !errors.Is(err, gateway.ErrUnsupported) {
		t.Fatalf("want ErrUnsupported, got %v", err)
	}
}

func TestRateLimitIsReportedWithRetryAfter(t *testing.T) {
	s := &stub{redeem: func() (*pkgphonepe.RedeemResult, error) {
		return nil, &pkgphonepe.APIError{HTTPStatus: 429, Code: "TOO_MANY_REQUESTS", RetryAfter: 30 * time.Second}
	}}
	_, err := newAdapter(t, s).SubmitDebit(context.Background(), gateway.DebitRequest{Attempt: gateway.ChargeAttemptRef{MerchantPaymentID: "pay1"}, DebitExecutionID: "e1"})
	if wait, ok := gateway.RateLimitOf(err); !ok || wait != 30*time.Second {
		t.Fatalf("%v", err)
	}
}

func TestCancelMandateTreatsAnAlreadyEndedSubscriptionAsDone(t *testing.T) {
	rejection := &pkgphonepe.APIError{HTTPStatus: 400, Code: "INVALID_STATE", Message: "not cancellable"}
	cancel := func(cancelErr error, state string, fetchErr error) error {
		a := newAdapter(t, &stub{
			cancel: func() error { return cancelErr },
			subscr: func() (*pkgphonepe.Subscription, error) { return &pkgphonepe.Subscription{State: state}, fetchErr },
		})
		return a.CancelMandate(context.Background(), gateway.MandateRef{MerchantAutopayID: "sub1"})
	}
	if err := cancel(nil, "", nil); err != nil {
		t.Fatalf("a successful cancel is success: %v", err)
	}
	for _, state := range []string{pkgphonepe.SubscriptionCancelled, pkgphonepe.SubscriptionRevoked, pkgphonepe.SubscriptionExpired} {
		if err := cancel(rejection, state, nil); !gateway.IsKind(err, gateway.ErrorKindAlreadyProcessed) {
			t.Fatalf("%s: got %v", state, err)
		}
	}
	if err := cancel(rejection, pkgphonepe.SubscriptionActive, nil); !gateway.IsKind(err, gateway.ErrorKindRejectedPermanently) {
		t.Fatalf("an active subscription that cannot be cancelled is a real rejection, got %v", err)
	}
	if err := cancel(&pkgphonepe.APIError{HTTPStatus: 503}, "", nil); !gateway.IsKind(err, gateway.ErrorKindTransient) {
		t.Fatalf("a 5xx stays transient, got %v", err)
	}
}
