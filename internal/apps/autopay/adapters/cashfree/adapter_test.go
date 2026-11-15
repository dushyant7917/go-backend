package cashfree

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"go-backend/internal/apps/autopay/gateway"
	pkgcashfree "go-backend/pkg/cashfree"
)

// stub embeds the Client interface so a test overrides only the calls it exercises.
type stub struct {
	pkgcashfree.Client
	execute func(pkgcashfree.ExecuteParams) (*pkgcashfree.ExecuteResult, error)
	fetchEx func() (*pkgcashfree.Execution, error)
	manage  func() (*pkgcashfree.Subscription, error)
	fetchSb func() (*pkgcashfree.Subscription, error)
}

func (s *stub) ManageSubscription(context.Context, string, string) (*pkgcashfree.Subscription, error) {
	return s.manage()
}
func (s *stub) FetchSubscription(context.Context, string) (*pkgcashfree.Subscription, error) {
	return s.fetchSb()
}

func (s *stub) ExecuteMandate(_ context.Context, p pkgcashfree.ExecuteParams) (*pkgcashfree.ExecuteResult, error) {
	return s.execute(p)
}
func (s *stub) FetchExecution(context.Context, string, string) (*pkgcashfree.Execution, error) {
	return s.fetchEx()
}

func newAdapter(t *testing.T, c pkgcashfree.Client) *Adapter {
	t.Helper()
	g, err := NewFactory(func(pkgcashfree.Credentials) pkgcashfree.Client { return c })(gateway.Config{
		Gateway: gateway.NameCashfree, Environment: "local", Credentials: map[string]string{"client_id": "id", "client_secret": "secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return g.(*Adapter)
}

func TestFactoryRequiresCredentials(t *testing.T) {
	_, err := NewFactory(pkgcashfree.NewFactory())(gateway.Config{Credentials: map[string]string{"client_id": "x"}})
	if err == nil {
		t.Fatal("expected an error without client_secret")
	}
}

func signed(t *testing.T, a *Adapter, body string, ts string) (http.Header, []byte) {
	t.Helper()
	h := http.Header{}
	h.Set("x-webhook-timestamp", ts)
	h.Set("x-webhook-signature", pkgcashfree.SignWebhook("secret", ts, []byte(body)))
	return h, []byte(body)
}

func TestWebhookSignatureIsVerifiedFirst(t *testing.T) {
	a := newAdapter(t, nil)
	h, body := signed(t, a, `{"type":"SUBSCRIPTION_AUTH_STATUS","data":{}}`, "1700000000000")
	if _, err := a.VerifyAndParseWebhook(h, body); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	h.Set("x-webhook-signature", "bogus")
	if _, err := a.VerifyAndParseWebhook(h, body); !errors.Is(err, gateway.ErrInvalidSignature) {
		t.Fatalf("want ErrInvalidSignature, got %v", err)
	}
	if _, err := a.VerifyAndParseWebhook(h, []byte(`{"type":`)); !errors.Is(err, gateway.ErrInvalidSignature) {
		t.Fatalf("a tampered body must fail the signature before parsing, got %v", err)
	}
}

func parse(t *testing.T, body string) []gateway.Event {
	t.Helper()
	a := newAdapter(t, nil)
	h, b := signed(t, a, body, "1700000000000")
	evs, err := a.VerifyAndParseWebhook(h, b)
	if err != nil {
		t.Fatal(err)
	}
	return evs
}

func TestWebhookEventMapping(t *testing.T) {
	cases := []struct {
		name string
		body string
		want gateway.EventType
		chk  func(*testing.T, gateway.Event)
	}{
		{"charge success", `{"type":"SUBSCRIPTION_PAYMENT_SUCCESS","event_time":"2026-11-12T03:40:00+05:30","data":{"subscription_id":"sub1","payment_id":"pay1","cf_payment_id":"77","payment_amount":199.5,"payment_type":"CHARGE"}}`,
			gateway.EventAutopayChargeSucceeded, func(t *testing.T, e gateway.Event) {
				if e.MerchantPaymentID != "pay1" || e.Amount != 19950 || e.GatewayPaymentID != "77" {
					t.Fatalf("bad event %+v", e)
				}
				// event_time is +05:30: must be stored as the identical UTC instant
				if !e.GatewayEventAt.Equal(time.Date(2026, 11, 11, 22, 10, 0, 0, time.UTC)) || e.GatewayEventAt.Location() != time.UTC {
					t.Fatalf("event time not converted to UTC: %v", e.GatewayEventAt)
				}
			}},
		{"auth payment success", `{"type":"SUBSCRIPTION_PAYMENT_SUCCESS","data":{"subscription_id":"sub1","payment_id":"cfauth","payment_type":"AUTH","payment_amount":1}}`, gateway.EventMandateAuthorizationSucceeded, nil},
		{"charge failed", `{"type":"SUBSCRIPTION_PAYMENT_FAILED","data":{"subscription_id":"sub1","payment_id":"pay1","failure_details":{"failure_reason":"insufficient"}}}`, gateway.EventAutopayChargeFailed,
			func(t *testing.T, e gateway.Event) {
				if e.FailureDescription != "insufficient" {
					t.Fatalf("bad failure %+v", e)
				}
			}},
		{"notification delivered", `{"type":"SUBSCRIPTION_CONTROLLED_NOTIFICATION_STATUS","data":{"subscription_id":"sub1","payment_id":"pay1","notification_status":"SUCCESS","notification_completed_time":"2026-11-12T01:20:00+05:30"}}`,
			gateway.EventPreDebitNotificationDelivered, func(t *testing.T, e gateway.Event) {
				if !e.DeliveredAt.Equal(time.Date(2026, 11, 11, 19, 50, 0, 0, time.UTC)) {
					t.Fatalf("delivered_at %v", e.DeliveredAt)
				}
			}},
		{"mandate cancelled", `{"type":"SUBSCRIPTION_STATUS_CHANGED","data":{"subscription_details":{"subscription_id":"sub1","subscription_status":"CUSTOMER_CANCELLED"}}}`, gateway.EventMandateStatusChanged,
			func(t *testing.T, e gateway.Event) {
				if e.MandateState != gateway.MandateCancelled || e.MerchantAutopayID != "sub1" {
					t.Fatalf("bad event %+v", e)
				}
			}},
		{"one-time paid", `{"type":"PAYMENT_SUCCESS_WEBHOOK","data":{"order":{"order_id":"ord1","order_amount":50},"payment":{"cf_payment_id":9,"payment_status":"SUCCESS"}}}`, gateway.EventOneTimePaymentSucceeded,
			func(t *testing.T, e gateway.Event) {
				if e.MerchantPaymentID != "ord1" || e.Amount != 5000 {
					t.Fatalf("bad event %+v", e)
				}
			}},
		{"link paid", `{"type":"PAYMENT_LINK_EVENT","data":{"link_id":"lnk1","link_status":"PAID","link_amount_paid":10}}`, gateway.EventOneTimePaymentSucceeded, nil},
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

func TestWebhookIgnoredAndDedupe(t *testing.T) {
	for _, body := range []string{
		`{"type":"SUBSCRIPTION_PAYMENT_NOTIFICATION_INITIATED","data":{"subscription_id":"s","payment_id":"p"}}`, // "sent" is not "delivered"
		`{"type":"REFUND_STATUS_WEBHOOK","data":{}}`,
		`{"type":"SUBSCRIPTION_STATUS_CHANGED","data":{"subscription_details":{"subscription_status":"INITIALIZED"}}}`,
	} {
		if evs := parse(t, body); len(evs) != 0 {
			t.Fatalf("expected no events for %s, got %+v", body, evs)
		}
	}
	body := `{"type":"SUBSCRIPTION_PAYMENT_SUCCESS","event_time":"2026-11-12T03:40:00+05:30","data":{"subscription_id":"s","payment_id":"p","cf_payment_id":"1"}}`
	a, b := parse(t, body)[0], parse(t, body)[0]
	if a.DedupeKey != b.DedupeKey {
		t.Fatal("the same delivery must produce the same dedupe key")
	}
	other := parse(t, `{"type":"SUBSCRIPTION_PAYMENT_FAILED","event_time":"2026-11-12T03:40:00+05:30","data":{"subscription_id":"s","payment_id":"p","cf_payment_id":"1"}}`)[0]
	if other.DedupeKey == a.DedupeKey {
		t.Fatal("different outcomes must not share a dedupe key")
	}
}

func TestDebitErrorClassification(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want gateway.ErrorKind
	}{
		{"timeout is unknown", context.DeadlineExceeded, gateway.ErrorKindOutcomeUnknown},
		{"4xx on debit is temporary", &pkgcashfree.APIError{HTTPStatus: 400, Code: "request_failed"}, gateway.ErrorKindRejectedTemporarily},
		{"5xx is transient", &pkgcashfree.APIError{HTTPStatus: 503}, gateway.ErrorKindTransient},
		{"429 is transient", &pkgcashfree.APIError{HTTPStatus: 429}, gateway.ErrorKindTransient},
		{"inactive mandate", &pkgcashfree.APIError{HTTPStatus: 400, Code: "subscription_not_active"}, gateway.ErrorKindMandateInvalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := newAdapter(t, &stub{execute: func(pkgcashfree.ExecuteParams) (*pkgcashfree.ExecuteResult, error) { return nil, c.err }})
			_, err := a.SubmitDebit(context.Background(), gateway.DebitRequest{Attempt: gateway.ChargeAttemptRef{MerchantPaymentID: "p"}, DebitExecutionID: "e"})
			var ge *gateway.Error
			if !errors.As(err, &ge) || ge.Kind != c.want {
				t.Fatalf("want kind %s, got %v", c.want, err)
			}
		})
	}
}

func TestFetchChargeAttemptStatusUnknownExecutionMeansNoDebit(t *testing.T) {
	a := newAdapter(t, &stub{fetchEx: func() (*pkgcashfree.Execution, error) {
		return nil, &pkgcashfree.APIError{HTTPStatus: 400, Code: "execution_not_found"}
	}})
	st, err := a.FetchChargeAttemptStatus(context.Background(), gateway.ChargeAttemptRef{MerchantPaymentID: "p", DebitExecutionID: "e"})
	if err != nil || st.DebitExists {
		t.Fatalf("an unknown execution id must mean no debit exists: %+v %v", st, err)
	}
	a = newAdapter(t, &stub{fetchEx: func() (*pkgcashfree.Execution, error) { return nil, &pkgcashfree.APIError{HTTPStatus: 503} }})
	if _, err := a.FetchChargeAttemptStatus(context.Background(), gateway.ChargeAttemptRef{MerchantPaymentID: "p", DebitExecutionID: "e"}); err == nil {
		t.Fatal("a gateway outage must surface as an error, not as 'no debit'")
	}
}

func TestScheduleDateClampedToT1ToT7InIST(t *testing.T) {
	now := time.Date(2026, 11, 12, 20, 0, 0, 0, time.UTC) // 01:30 IST on the 13th
	day := func(d int) time.Time { return time.Date(2026, 11, d, 0, 0, 0, 0, pkgcashfree.IST) }
	for _, c := range []struct{ debit, want time.Time }{
		{now, day(14)},                   // today -> T+1
		{now.AddDate(0, 0, 2), day(15)},  // within range, converted to IST date
		{now.AddDate(0, 0, 30), day(20)}, // far future -> T+7
	} {
		if got := scheduleDateFor(c.debit, now); !got.Equal(c.want) {
			t.Fatalf("scheduleDateFor(%v) = %v, want %v", c.debit, got, c.want)
		}
	}
}

func TestRateLimitIsReportedWithRetryAfter(t *testing.T) {
	a := newAdapter(t, &stub{execute: func(pkgcashfree.ExecuteParams) (*pkgcashfree.ExecuteResult, error) {
		return nil, &pkgcashfree.APIError{HTTPStatus: 429, Code: "rate_limit_error", RetryAfter: 20 * time.Second}
	}})
	_, err := a.SubmitDebit(context.Background(), gateway.DebitRequest{Attempt: gateway.ChargeAttemptRef{MerchantPaymentID: "p"}, DebitExecutionID: "e"})
	if wait, ok := gateway.RateLimitOf(err); !ok || wait != 20*time.Second || !gateway.IsKind(err, gateway.ErrorKindTransient) {
		t.Fatalf("a 429 on a debit is a rate limit (not a temporary rejection): %v", err)
	}
}

func TestCancelMandateTreatsAnAlreadyCancelledSubscriptionAsDone(t *testing.T) {
	rejection := &pkgcashfree.APIError{HTTPStatus: 400, Code: "subscription_cancel_failed", Message: "cannot cancel"}
	cancel := func(manageErr error, fetched *pkgcashfree.Subscription, fetchErr error) error {
		a := newAdapter(t, &stub{
			manage:  func() (*pkgcashfree.Subscription, error) { return nil, manageErr },
			fetchSb: func() (*pkgcashfree.Subscription, error) { return fetched, fetchErr },
		})
		return a.CancelMandate(context.Background(), gateway.MandateRef{MerchantAutopayID: "sub1"})
	}
	if err := cancel(nil, nil, nil); err != nil {
		t.Fatalf("a successful cancel is success: %v", err)
	}
	for _, status := range []string{"CANCELLED", "CUSTOMER_CANCELLED", "EXPIRED", "COMPLETED"} {
		if err := cancel(rejection, &pkgcashfree.Subscription{Status: status}, nil); !gateway.IsKind(err, gateway.ErrorKindAlreadyProcessed) {
			t.Fatalf("%s: a rejected cancel of an already ended subscription is already-processed, got %v", status, err)
		}
	}
	if err := cancel(rejection, &pkgcashfree.Subscription{Status: "ACTIVE"}, nil); !gateway.IsKind(err, gateway.ErrorKindRejectedPermanently) {
		t.Fatalf("an active subscription that cannot be cancelled is a real rejection, got %v", err)
	}
	if err := cancel(rejection, nil, errors.New("boom")); !gateway.IsKind(err, gateway.ErrorKindRejectedPermanently) {
		t.Fatalf("when the state cannot be checked the original rejection is reported, got %v", err)
	}
	// a gateway outage is not a rejection: no extra lookup, still transient
	if err := cancel(&pkgcashfree.APIError{HTTPStatus: 503}, nil, nil); !gateway.IsKind(err, gateway.ErrorKindTransient) {
		t.Fatalf("a 5xx stays transient, got %v", err)
	}
}
