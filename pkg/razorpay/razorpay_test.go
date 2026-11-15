package razorpay

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func serve(t *testing.T, h http.HandlerFunc) *client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return newWithHTTP(Credentials{KeyID: "k", KeySecret: "s"}, srv.URL, srv.Client())
}

func TestAPIErrorKeepsStatusCodeAndRetryAfter(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":"BAD_REQUEST_ERROR","description":"slow down","reason":"rate_limit"}}`))
	})
	_, err := c.FetchOrder(context.Background(), "order_1")
	apiErr, ok := AsAPIError(err)
	if !ok || apiErr.HTTPStatus != 429 || apiErr.Code != "BAD_REQUEST_ERROR" || apiErr.RetryAfter != 7*time.Second || !apiErr.IsRetryable() {
		t.Fatalf("bad error %+v (%v)", apiErr, err)
	}
	c = serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":"BAD_REQUEST_ERROR","description":"nope","reason":"mandate_cancelled"}}`))
	})
	_, err = c.FetchOrder(context.Background(), "order_1")
	if apiErr, _ := AsAPIError(err); apiErr == nil || apiErr.IsRetryable() || apiErr.Reason != "mandate_cancelled" {
		t.Fatalf("a 400 business rejection must not be retryable: %+v", apiErr)
	}
}

func TestOrderDecodingTolerantNotesAndNotification(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "k" || p != "s" {
			t.Errorf("missing basic auth")
		}
		_, _ = w.Write([]byte(`{"id":"order_1","status":"attempted","amount":9900,"amount_paid":0,"attempts":1,"notes":[],
			"notification":{"token_id":"token_1","status":"delivered","payment_after":1795000000,"delivered_at":1794990000}}`))
	})
	o, err := c.FetchOrder(context.Background(), "order_1")
	if err != nil {
		t.Fatal(err)
	}
	if o.ID != "order_1" || o.Attempts != 1 || len(o.Notes) != 0 || o.Notification == nil ||
		!o.Notification.DeliveredAt.Equal(time.Unix(1794990000, 0)) || o.Notification.DeliveredAt.Location() != time.UTC {
		t.Fatalf("bad order %+v %+v", o, o.Notification)
	}
}

func TestFindOrderByReceiptReturnsNilWhenNone(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("receipt") != "rcpt" {
			t.Errorf("receipt not sent: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"entity":"collection","count":0,"items":[]}`))
	})
	o, err := c.FindOrderByReceipt(context.Background(), "rcpt")
	if err != nil || o != nil {
		t.Fatalf("want nil,nil got %v %v", o, err)
	}
}

func TestWebhookSignature(t *testing.T) {
	body := []byte(`{"event":"payment.captured"}`)
	sig := SignWebhook("sec", body)
	if !VerifyWebhookSignature("sec", body, sig) {
		t.Fatal("valid signature rejected")
	}
	if VerifyWebhookSignature("other", body, sig) || VerifyWebhookSignature("", body, sig) || VerifyWebhookSignature("sec", body, "zz") || VerifyWebhookSignature("sec", []byte("x"), sig) {
		t.Fatal("invalid signature accepted")
	}
}

func TestCancelTokenUsesPutAndReturnsTheAcknowledgement(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/customers/cust_1/tokens/token_1/cancel" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"status":"cancellation_initiated"}`))
	})
	status, err := c.CancelToken(context.Background(), "cust_1", "token_1")
	if err != nil || status != "cancellation_initiated" {
		t.Fatalf("status=%q err=%v", status, err)
	}
}

func TestRetryAfterIsReadFromTheHeader(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "9")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	_, err := c.FetchOrder(context.Background(), "order_1")
	if apiErr, ok := AsAPIError(err); !ok || apiErr.HTTPStatus != 429 || apiErr.RetryAfter != 9*time.Second {
		t.Fatalf("%+v %v", apiErr, err)
	}
}
