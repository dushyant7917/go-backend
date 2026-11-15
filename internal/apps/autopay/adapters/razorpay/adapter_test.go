package razorpay

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"go-backend/internal/apps/autopay/gateway"
	pkgrazorpay "go-backend/pkg/razorpay"
)

// fake records the orders it was asked to create and answers from canned values.
type fake struct {
	pkgrazorpay.Client
	createOrder  func(map[string]any) (*pkgrazorpay.Order, error)
	byReceipt    func(string) (*pkgrazorpay.Order, error)
	order        func() (*pkgrazorpay.Order, error)
	payments     func() ([]pkgrazorpay.Payment, error)
	token        func() (*pkgrazorpay.Token, error)
	recurring    func(map[string]any) (string, error)
	cancelToken  func() (string, error)
	orderCreates []map[string]any
}

func (f *fake) CreateCustomer(context.Context, string, string) (string, error) { return "cust_1", nil }
func (f *fake) CreateOrder(_ context.Context, d map[string]any) (*pkgrazorpay.Order, error) {
	f.orderCreates = append(f.orderCreates, d)
	return f.createOrder(d)
}
func (f *fake) FindOrderByReceipt(_ context.Context, r string) (*pkgrazorpay.Order, error) {
	return f.byReceipt(r)
}
func (f *fake) FetchOrder(context.Context, string) (*pkgrazorpay.Order, error) { return f.order() }
func (f *fake) FetchOrderPayments(context.Context, string) ([]pkgrazorpay.Payment, error) {
	return f.payments()
}
func (f *fake) FetchToken(context.Context, string, string) (*pkgrazorpay.Token, error) {
	return f.token()
}
func (f *fake) CreateRecurringPayment(_ context.Context, d map[string]any) (string, error) {
	return f.recurring(d)
}

func (f *fake) CancelToken(context.Context, string, string) (string, error) { return f.cancelToken() }

func newAdapter(t *testing.T, c pkgrazorpay.Client) *Adapter {
	t.Helper()
	g, err := NewFactory(func(pkgrazorpay.Credentials) pkgrazorpay.Client { return c })(gateway.Config{
		Gateway: gateway.NameRazorpay, Environment: "local", WebhookSecret: "whsec",
		Credentials: map[string]string{"key_id": "rzp_test_1", "key_secret": "s"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return g.(*Adapter)
}

var user = gateway.UserInfo{UserID: "u", Email: "a@b.c", Phone: "9999999999", CountryCode: "91"}

func TestFactoryRequiresCredentials(t *testing.T) {
	if _, err := NewFactory(pkgrazorpay.NewFactory())(gateway.Config{Credentials: map[string]string{"key_id": "x"}}); err == nil {
		t.Fatal("expected an error without key_secret")
	}
}

func TestCreateAuthorizationBuildsTokenOrder(t *testing.T) {
	f := &fake{createOrder: func(map[string]any) (*pkgrazorpay.Order, error) { return &pkgrazorpay.Order{ID: "order_a"}, nil }}
	end := time.Date(2027, 11, 12, 0, 0, 0, 0, time.UTC)
	s, err := newAdapter(t, f).CreateMandateAuthorization(context.Background(), gateway.MandateAuthorizationRequest{
		MerchantAutopayID: "ap_1", MerchantPaymentID: "auth_1", User: user, AuthorizationAmount: 100, MaxDebitAmount: 49900, Currency: "INR", EndAt: end,
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.GatewayOrderID != "order_a" || s.GatewayCustomerID != "cust_1" || s.ClientPayload["key_id"] != "rzp_test_1" || s.ClientPayload["mode"] != "sandbox" {
		t.Fatalf("bad session %+v", s)
	}
	d := f.orderCreates[0]
	tok := d["token"].(map[string]any)
	if tok["max_amount"] != int64(49900) || tok["expire_at"] != end.Unix() || d["method"] != "upi" || d["customer_id"] != "cust_1" {
		t.Fatalf("bad order %+v", d)
	}
	if _, err := newAdapter(t, f).CreateMandateAuthorization(context.Background(), gateway.MandateAuthorizationRequest{User: gateway.UserInfo{Email: "x"}}); !gateway.IsKind(err, gateway.ErrorKindRejectedPermanently) {
		t.Fatalf("missing customer details must be a permanent rejection, got %v", err)
	}
}

func TestSendNotificationCreatesOrderWithNotification(t *testing.T) {
	f := &fake{createOrder: func(map[string]any) (*pkgrazorpay.Order, error) {
		return &pkgrazorpay.Order{ID: "order_n", Notification: &pkgrazorpay.OrderNotification{Status: "pending"}}, nil
	}}
	when := time.Date(2026, 11, 14, 22, 0, 0, 0, time.UTC)
	res, err := newAdapter(t, f).SendPreDebitNotification(context.Background(), gateway.PreDebitNotificationRequest{
		Mandate: gateway.MandateRef{MerchantAutopayID: "ap_1", GatewayMandateID: "token_1"}, MerchantPaymentID: "pay_x_a1",
		PreDebitNotificationID: "pay_x_a1_n1", Amount: 19900, Currency: "INR", EarliestDebitAt: when,
	})
	if err != nil || res.GatewayOrderID != "order_n" || res.State != gateway.NotificationPending {
		t.Fatalf("%+v %v", res, err)
	}
	n := f.orderCreates[0]["notification"].(map[string]any)
	if n["token_id"] != "token_1" || n["payment_after"] != when.Unix() {
		t.Fatalf("notification must carry the token and the earliest debit time: %+v", n)
	}
	// without a token there is nothing to notify
	_, err = newAdapter(t, f).SendPreDebitNotification(context.Background(), gateway.PreDebitNotificationRequest{Mandate: gateway.MandateRef{}})
	if !gateway.IsKind(err, gateway.ErrorKindMandateInvalid) {
		t.Fatalf("want MandateInvalid, got %v", err)
	}
}

func TestSendNotificationTimeoutRecoversOrderByReceiptInsteadOfDuplicating(t *testing.T) {
	f := &fake{
		createOrder: func(map[string]any) (*pkgrazorpay.Order, error) { return nil, context.DeadlineExceeded },
		byReceipt: func(r string) (*pkgrazorpay.Order, error) {
			if len(r) > 40 {
				t.Errorf("receipt %q exceeds Razorpay's 40 characters", r)
			}
			return &pkgrazorpay.Order{ID: "order_found", Notification: &pkgrazorpay.OrderNotification{Status: "delivered", DeliveredAt: time.Unix(1795000000, 0).UTC()}}, nil
		},
	}
	res, err := newAdapter(t, f).SendPreDebitNotification(context.Background(), gateway.PreDebitNotificationRequest{
		Mandate: gateway.MandateRef{GatewayMandateID: "token_1"}, MerchantPaymentID: "pay_" + strings.Repeat("a", 32) + "_a1", PreDebitNotificationID: "pay_" + strings.Repeat("a", 32) + "_a1_n1",
		Amount: 100, Currency: "INR", EarliestDebitAt: time.Now(),
	})
	if err != nil || res.GatewayOrderID != "order_found" || res.State != gateway.NotificationDelivered {
		t.Fatalf("a timed-out create must adopt the order found by receipt: %+v %v", res, err)
	}
	// nothing found: the outcome stays unknown
	f.byReceipt = func(string) (*pkgrazorpay.Order, error) { return nil, nil }
	_, err = newAdapter(t, f).SendPreDebitNotification(context.Background(), gateway.PreDebitNotificationRequest{Mandate: gateway.MandateRef{GatewayMandateID: "t"}, PreDebitNotificationID: "n"})
	if !gateway.IsKind(err, gateway.ErrorKindOutcomeUnknown) {
		t.Fatalf("want OutcomeUnknown, got %v", err)
	}
}

func TestReceiptsNeverCollideForLongIDs(t *testing.T) {
	a := "pay_" + strings.Repeat("a", 32) + "_a1_r1_n1"
	b := "pay_" + strings.Repeat("a", 32) + "_a1_r2_n1"
	if fit40(a) == fit40(b) || len(fit40(a)) != 40 || fit40("short") != "short" {
		t.Fatal("receipts must be unique and at most 40 characters")
	}
}

func TestDebitErrorClassification(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want gateway.ErrorKind
	}{
		{"timeout", context.DeadlineExceeded, gateway.ErrorKindOutcomeUnknown},
		{"server error", &pkgrazorpay.APIError{HTTPStatus: 500, Code: "SERVER_ERROR"}, gateway.ErrorKindTransient},
		{"rate limited", &pkgrazorpay.APIError{HTTPStatus: 429}, gateway.ErrorKindTransient},
		{"cancelled token", &pkgrazorpay.APIError{HTTPStatus: 400, Code: "BAD_REQUEST_ERROR", Reason: "mandate_cancelled"}, gateway.ErrorKindMandateInvalid},
		{"order already paid", &pkgrazorpay.APIError{HTTPStatus: 400, Code: "BAD_REQUEST_ERROR", Description: "Payment already done for this order"}, gateway.ErrorKindAlreadyProcessed},
		{"other 400 on a debit", &pkgrazorpay.APIError{HTTPStatus: 400, Code: "BAD_REQUEST_ERROR", Description: "UPI debit window closed"}, gateway.ErrorKindRejectedTemporarily},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fake{recurring: func(map[string]any) (string, error) { return "", c.err }}
			_, err := newAdapter(t, f).SubmitDebit(context.Background(), gateway.DebitRequest{
				User: user, Amount: 100, Currency: "INR", DebitExecutionID: "e1",
				Attempt: gateway.ChargeAttemptRef{GatewayOrderID: "order_n", MerchantPaymentID: "pay_x", Mandate: gateway.MandateRef{GatewayMandateID: "token_1", GatewayCustomerID: "cust_1"}},
			})
			var ge *gateway.Error
			if !errors.As(err, &ge) || ge.Kind != c.want {
				t.Fatalf("want %s, got %v", c.want, err)
			}
		})
	}
}

func TestSubmitDebitSendsTokenAgainstOrder(t *testing.T) {
	var sent map[string]any
	f := &fake{recurring: func(d map[string]any) (string, error) { sent = d; return "pay_rzp_1", nil }}
	sub, err := newAdapter(t, f).SubmitDebit(context.Background(), gateway.DebitRequest{
		User: user, Amount: 19900, Currency: "INR", DebitExecutionID: "e1",
		Attempt: gateway.ChargeAttemptRef{GatewayOrderID: "order_n", Mandate: gateway.MandateRef{GatewayMandateID: "token_1", GatewayCustomerID: "cust_1"}},
	})
	if err != nil || sub.State != gateway.PaymentPending || sub.GatewayPaymentID != "pay_rzp_1" {
		t.Fatalf("%+v %v", sub, err)
	}
	if sent["order_id"] != "order_n" || sent["token"] != "token_1" || sent["customer_id"] != "cust_1" || sent["contact"] != "+919999999999" || sent["amount"] != int64(19900) {
		t.Fatalf("bad recurring payload %+v", sent)
	}
}

func TestFetchChargeAttemptStatus(t *testing.T) {
	run := func(o *pkgrazorpay.Order, ps []pkgrazorpay.Payment) *gateway.ChargeAttemptStatus {
		t.Helper()
		f := &fake{order: func() (*pkgrazorpay.Order, error) { return o, nil }, payments: func() ([]pkgrazorpay.Payment, error) { return ps, nil }}
		st, err := newAdapter(t, f).FetchChargeAttemptStatus(context.Background(), gateway.ChargeAttemptRef{GatewayOrderID: "order_n"})
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	if st := run(&pkgrazorpay.Order{Status: "created", Notification: &pkgrazorpay.OrderNotification{Status: "delivered", DeliveredAt: time.Unix(1795000000, 0)}}, nil); st.Notification != gateway.NotificationDelivered || st.DebitExists {
		t.Fatalf("delivered, not debited: %+v", st)
	}
	if st := run(&pkgrazorpay.Order{Status: "created", Notification: &pkgrazorpay.OrderNotification{Status: "failed"}}, nil); st.Notification != gateway.NotificationFailed {
		t.Fatalf("failed notification: %+v", st)
	}
	if st := run(&pkgrazorpay.Order{Status: "paid"}, []pkgrazorpay.Payment{{ID: "p1", Status: "captured"}}); st.Debit != gateway.PaymentSucceeded || st.GatewayPaymentID != "p1" || st.Notification != gateway.NotificationDelivered {
		t.Fatalf("paid order: %+v", st)
	}
	st := run(&pkgrazorpay.Order{Status: "attempted", Attempts: 1}, []pkgrazorpay.Payment{{ID: "p1", Status: "failed", ErrorCode: "BAD_REQUEST_ERROR", ErrorDescription: "insufficient"}})
	if !st.DebitExists || st.Debit != gateway.PaymentFailed || st.FailureCode != "BAD_REQUEST_ERROR" {
		t.Fatalf("failed payment: %+v", st)
	}
	// a captured payment wins over an earlier failed one (customer retried)
	st = run(&pkgrazorpay.Order{Status: "paid"}, []pkgrazorpay.Payment{{ID: "p1", Status: "failed"}, {ID: "p2", Status: "captured"}})
	if st.Debit != gateway.PaymentSucceeded || st.GatewayPaymentID != "p2" {
		t.Fatalf("%+v", st)
	}
}

func TestAuthorizationStatus(t *testing.T) {
	status := func(ps []pkgrazorpay.Payment, ref gateway.MandateRef) *gateway.MandateAuthorizationStatus {
		t.Helper()
		f := &fake{payments: func() ([]pkgrazorpay.Payment, error) { return ps, nil },
			token: func() (*pkgrazorpay.Token, error) { return &pkgrazorpay.Token{ID: "token_1", Status: "confirmed"}, nil }}
		st, err := newAdapter(t, f).FetchMandateAuthorizationStatus(context.Background(), ref)
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	order := gateway.MandateRef{GatewayOrderID: "order_a", GatewayCustomerID: "cust_1"}
	if st := status([]pkgrazorpay.Payment{{ID: "p1", Status: "captured", TokenID: "token_1"}}, order); st.State != gateway.AuthorizationSucceeded || st.GatewayMandateID != "token_1" || st.GatewayPaymentID != "p1" {
		t.Fatalf("captured authorization payment: %+v", st)
	}
	if st := status([]pkgrazorpay.Payment{{ID: "p1", Status: "failed", ErrorCode: "X"}}, order); st.State != gateway.AuthorizationFailed || st.FailureCode != "X" {
		t.Fatalf("failed: %+v", st)
	}
	if st := status([]pkgrazorpay.Payment{{ID: "p1", Status: "failed"}, {ID: "p2", Status: "created"}}, order); st.State != gateway.AuthorizationPending {
		t.Fatalf("the customer is still in the flow: %+v", st)
	}
	if st := status(nil, order); st.State != gateway.AuthorizationPending {
		t.Fatalf("no payment yet: %+v", st)
	}
	if st := status(nil, gateway.MandateRef{GatewayMandateID: "token_1", GatewayCustomerID: "cust_1"}); st.State != gateway.AuthorizationSucceeded {
		t.Fatalf("confirmed token: %+v", st)
	}
}

func TestMandateStateAndCancel(t *testing.T) {
	for s, want := range map[string]gateway.MandateState{"confirmed": gateway.MandateActive, "paused": gateway.MandatePaused, "cancelled": gateway.MandateCancelled, "initiated": gateway.MandateAwaitingAuthorization} {
		if got := mandateState(s); got != want {
			t.Fatalf("%s -> %s want %s", s, got, want)
		}
	}
	if got := mandateState("cancellation_initiated"); got != gateway.MandateCancelled {
		t.Fatalf("a token Razorpay is cancelling fails every debit, so it counts as cancelled, got %s", got)
	}
}

func parse(t *testing.T, body string, eventID string) []gateway.Event {
	t.Helper()
	a := newAdapter(t, nil)
	h := http.Header{}
	h.Set("X-Razorpay-Signature", pkgrazorpay.SignWebhook("whsec", []byte(body)))
	if eventID != "" {
		h.Set("x-razorpay-event-id", eventID)
	}
	evs, err := a.VerifyAndParseWebhook(h, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	return evs
}

func TestWebhookSignatureIsVerifiedFirst(t *testing.T) {
	a := newAdapter(t, nil)
	body := []byte(`{"event":"token.paused"}`)
	h := http.Header{}
	h.Set("X-Razorpay-Signature", pkgrazorpay.SignWebhook("wrong", body))
	if _, err := a.VerifyAndParseWebhook(h, body); !errors.Is(err, gateway.ErrInvalidSignature) {
		t.Fatalf("want ErrInvalidSignature, got %v", err)
	}
	if _, err := a.VerifyAndParseWebhook(http.Header{}, body); !errors.Is(err, gateway.ErrInvalidSignature) {
		t.Fatalf("missing signature: want ErrInvalidSignature, got %v", err)
	}
}

func TestWebhookEventMapping(t *testing.T) {
	cases := []struct {
		name string
		body string
		want gateway.EventType
		chk  func(*testing.T, gateway.Event)
	}{
		{"payment captured", `{"event":"payment.captured","created_at":1795000000,"payload":{"payment":{"entity":{"id":"pay_1","order_id":"order_n","amount":19900,"token_id":"token_1","notes":{"merchant_payment_id":"pay_x_a1","purpose":"autopay_charge"}}}}}`,
			gateway.EventAutopayChargeSucceeded, func(t *testing.T, e gateway.Event) {
				if e.Amount != 19900 || e.GatewayOrderID != "order_n" || e.MerchantPaymentID != "pay_x_a1" || e.GatewayMandateID != "token_1" || !e.GatewayEventAt.Equal(time.Unix(1795000000, 0)) {
					t.Fatalf("bad event %+v", e)
				}
			}},
		{"one-time captured", `{"event":"payment.captured","payload":{"payment":{"entity":{"id":"pay_2","order_id":"order_o","amount":4900,"notes":{"purpose":"one_time_payment"}}}}}`, gateway.EventOneTimePaymentSucceeded, nil},
		{"payment failed", `{"event":"payment.failed","payload":{"payment":{"entity":{"id":"pay_1","order_id":"order_n","error_code":"BAD_REQUEST_ERROR","error_description":"insufficient funds","notes":[]}}}}`,
			gateway.EventAutopayChargeFailed, func(t *testing.T, e gateway.Event) {
				if e.FailureCode != "BAD_REQUEST_ERROR" || e.FailureDescription != "insufficient funds" {
					t.Fatalf("bad failure %+v", e)
				}
			}},
		{"notification delivered", `{"event":"order.notification.delivered","created_at":1794990000,"payload":{"order":{"entity":{"id":"order_n","notes":{"merchant_payment_id":"pay_x_a1"}}}}}`,
			gateway.EventPreDebitNotificationDelivered, func(t *testing.T, e gateway.Event) {
				if !e.DeliveredAt.Equal(time.Unix(1794990000, 0)) || e.DeliveredAt.Location() != time.UTC {
					t.Fatalf("delivered_at %v", e.DeliveredAt)
				}
			}},
		{"notification failed", `{"event":"order.notification.failed","payload":{"order":{"entity":{"id":"order_n"}}}}`, gateway.EventPreDebitNotificationFailed, nil},
		{"token confirmed", `{"event":"token.confirmed","payload":{"token":{"entity":{"id":"token_1"}}}}`, gateway.EventMandateAuthorizationSucceeded, nil},
		{"token paused", `{"event":"token.paused","payload":{"token":{"entity":{"id":"token_1"}}}}`, gateway.EventMandateStatusChanged, func(t *testing.T, e gateway.Event) {
			if e.MandateState != gateway.MandatePaused || e.GatewayMandateID != "token_1" {
				t.Fatalf("%+v", e)
			}
		}},
		{"token cancelled", `{"event":"token.cancelled","payload":{"token":{"entity":{"id":"token_1"}}}}`, gateway.EventMandateStatusChanged, func(t *testing.T, e gateway.Event) {
			if e.MandateState != gateway.MandateCancelled {
				t.Fatalf("%+v", e)
			}
		}},
		{"link paid", `{"event":"payment_link.paid","payload":{"payment_link":{"entity":{"id":"plink_1","amount_paid":4900,"notes":{"merchant_payment_id":"pay_ot"}}},"payment":{"entity":{"id":"pay_3"}}}}`,
			gateway.EventOneTimePaymentSucceeded, func(t *testing.T, e gateway.Event) {
				if e.GatewayPaymentLinkID != "plink_1" || e.Amount != 4900 || e.GatewayPaymentID != "pay_3" {
					t.Fatalf("%+v", e)
				}
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			evs := parse(t, c.body, "evt_1")
			if len(evs) != 1 || evs[0].Type != c.want {
				t.Fatalf("want one %s, got %+v", c.want, evs)
			}
			if evs[0].DedupeKey != "evt_1" {
				t.Fatalf("the x-razorpay-event-id header must be the dedupe key, got %q", evs[0].DedupeKey)
			}
			if c.chk != nil {
				c.chk(t, evs[0])
			}
		})
	}
}

func TestWebhookIgnoredAndFallbackDedupe(t *testing.T) {
	for _, body := range []string{
		`{"event":"payment.authorized","payload":{"payment":{"entity":{"id":"p"}}}}`, // orders capture automatically
		`{"event":"refund.processed","payload":{}}`,
	} {
		if evs := parse(t, body, ""); len(evs) != 0 {
			t.Fatalf("expected no events for %s, got %+v", body, evs)
		}
	}
	ok := `{"event":"payment.captured","created_at":1,"payload":{"payment":{"entity":{"id":"pay_1","order_id":"o"}}}}`
	bad := `{"event":"payment.failed","created_at":1,"payload":{"payment":{"entity":{"id":"pay_1","order_id":"o"}}}}`
	first, again := parse(t, ok, "")[0].DedupeKey, parse(t, ok, "")[0].DedupeKey
	if first != again || first == parse(t, bad, "")[0].DedupeKey {
		t.Fatal("without an event id the key must be stable per delivery and differ per outcome")
	}
}

func TestCancelMandate(t *testing.T) {
	ref := gateway.MandateRef{GatewayMandateID: "token_1", GatewayCustomerID: "cust_1"}
	cancel := func(err error) error {
		return newAdapter(t, &fake{cancelToken: func() (string, error) { return "cancellation_initiated", err }}).CancelMandate(context.Background(), ref)
	}
	if err := cancel(nil); err != nil {
		t.Fatalf("an acknowledged cancel is success, got %v", err)
	}
	if err := cancel(&pkgrazorpay.APIError{HTTPStatus: 400, Code: "BAD_REQUEST_ERROR", Reason: "invalid_mandate_state"}); !gateway.IsKind(err, gateway.ErrorKindAlreadyProcessed) {
		t.Fatalf("nothing left to cancel must be treated as done, got %v", err)
	}
	if err := cancel(&pkgrazorpay.APIError{HTTPStatus: 400, Reason: "concurrent_request_in_progress"}); !gateway.IsKind(err, gateway.ErrorKindTransient) {
		t.Fatalf("a concurrent cancel is retried later, got %v", err)
	}
	if err := cancel(&pkgrazorpay.APIError{HTTPStatus: 400, Reason: "token_not_recurring"}); !gateway.IsKind(err, gateway.ErrorKindRejectedPermanently) {
		t.Fatalf("a non-recurring token cannot be cancelled, got %v", err)
	}
	if err := newAdapter(t, &fake{}).CancelMandate(context.Background(), gateway.MandateRef{}); !gateway.IsKind(err, gateway.ErrorKindMandateInvalid) {
		t.Fatalf("no token yet: nothing to cancel at Razorpay, got %v", err)
	}
	if !newAdapter(t, &fake{}).Capabilities().SupportsMandateCancellation {
		t.Fatal("Razorpay can cancel a token")
	}
}

func TestRateLimitIsReportedWithRetryAfter(t *testing.T) {
	f := &fake{recurring: func(map[string]any) (string, error) {
		return "", &pkgrazorpay.APIError{HTTPStatus: 429, Code: "BAD_REQUEST_ERROR", Description: "too many requests", RetryAfter: 12 * time.Second}
	}}
	_, err := newAdapter(t, f).SubmitDebit(context.Background(), gateway.DebitRequest{User: user, Attempt: gateway.ChargeAttemptRef{GatewayOrderID: "o"}})
	if wait, ok := gateway.RateLimitOf(err); !ok || wait != 12*time.Second || !gateway.IsKind(err, gateway.ErrorKindTransient) {
		t.Fatalf("a 429 must be a transient rate-limit error carrying Retry-After: %v", err)
	}
}
