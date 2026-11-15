package cashfree

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

func newTestRaw(t *testing.T, h http.HandlerFunc) *rawClient {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &rawClient{
		creds:   Credentials{ClientID: "id", ClientSecret: "secret", Sandbox: true},
		baseURL: srv.URL,
		http:    srv.Client(),
	}
}

func TestNotifyMandate_RequestAndResponse(t *testing.T) {
	var gotBody map[string]interface{}
	var gotHeaders http.Header
	r := newTestRaw(t, func(w http.ResponseWriter, req *http.Request) {
		gotHeaders = req.Header.Clone()
		if req.Method != http.MethodPost || req.URL.Path != "/subscriptions/pay/controlled/notify-mandate" {
			t.Errorf("unexpected %s %s", req.Method, req.URL.Path)
		}
		b, _ := io.ReadAll(req.Body)
		_ = json.Unmarshal(b, &gotBody)
		_, _ = w.Write([]byte(`{"cf_notification_id":"cfn1","cf_payment_id":"cfp1","notification_id":"n1","notification_status":"INITIALIZED","payment_status":"INITIALIZED"}`))
	})

	res, err := r.NotifyMandate(context.Background(), NotifyParams{
		SubscriptionID: "sub1", PaymentID: "pay1", NotificationID: "n1", AmountPaise: 69900,
		ScheduleDate: time.Date(2026, 11, 9, 2, 0, 0, 0, IST), Remarks: "cycle 1", IdempotencyKey: "idem-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.CFNotificationID != "cfn1" || res.Status != "INITIALIZED" {
		t.Errorf("bad result: %+v", res)
	}
	if gotBody["payment_amount"].(float64) != 699 {
		t.Errorf("amount must be rupees, got %v", gotBody["payment_amount"])
	}
	if gotBody["payment_schedule_date"] != "2026-11-09T02:00:00+05:30" {
		t.Errorf("schedule date: %v", gotBody["payment_schedule_date"])
	}
	for k, want := range map[string]string{"X-Api-Version": APIVersion, "X-Client-Id": "id", "X-Client-Secret": "secret", "X-Idempotency-Key": idempotencyHeader("idem-1")} {
		if gotHeaders.Get(k) != want {
			t.Errorf("header %s = %q, want %q", k, gotHeaders.Get(k), want)
		}
	}
}

func TestExecuteMandate(t *testing.T) {
	r := newTestRaw(t, func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/subscriptions/pay/controlled/execute-mandate" {
			t.Errorf("path %s", req.URL.Path)
		}
		b, _ := io.ReadAll(req.Body)
		var body map[string]string
		_ = json.Unmarshal(b, &body)
		if body["execution_id"] != "e1" || body["payment_id"] != "pay1" {
			t.Errorf("body %v", body)
		}
		_, _ = w.Write([]byte(`{"cf_execution_id":"cfe1","execution_id":"e1","execution_status":"INITIALIZED","payment_status":"PENDING"}`))
	})
	res, err := r.ExecuteMandate(context.Background(), ExecuteParams{PaymentID: "pay1", ExecutionID: "e1"})
	if err != nil || res.CFExecutionID != "cfe1" || res.PaymentStatus != "PENDING" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestAPIErrorParsing(t *testing.T) {
	r := newTestRaw(t, func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"Previous PDN is in progress","code":"Prev_PDN_In_Progress","type":"invalid_request_error"}`))
	})
	_, err := r.NotifyMandate(context.Background(), NotifyParams{SubscriptionID: "s", PaymentID: "p", NotificationID: "n", AmountPaise: 100})
	apiErr, ok := AsAPIError(err)
	if !ok {
		t.Fatalf("expected APIError, got %v", err)
	}
	if apiErr.HTTPStatus != 400 || apiErr.Code != "Prev_PDN_In_Progress" || !apiErr.IsInProgress() || apiErr.IsRetryable() {
		t.Errorf("unexpected: %+v", apiErr)
	}
}

func TestAPIError_IsInProgressUsesCodesOnly(t *testing.T) {
	if !(&APIError{Code: "Prev_PDN_In_Progress"}).IsInProgress() || !(&APIError{Code: "prev_execution_in_progress"}).IsInProgress() {
		t.Error("documented codes must match")
	}
	if (&APIError{Code: "something_else", Message: "request is in progress somewhere"}).IsInProgress() {
		t.Error("message text must not drive control flow")
	}
}

func TestAPIError_Retryable(t *testing.T) {
	for status, want := range map[int]bool{400: false, 401: false, 404: false, 422: false, 429: true, 500: true, 502: true} {
		if got := (&APIError{HTTPStatus: status}).IsRetryable(); got != want {
			t.Errorf("status %d retryable=%v want %v", status, got, want)
		}
	}
}

func TestFetchNotificationAndExecution(t *testing.T) {
	r := newTestRaw(t, func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/subscriptions/payments/pay1/controlled/notifications/n1":
			_, _ = w.Write([]byte(`{"notification_id":"n1","notification_status":"FAILED","notification_completed_time":"2026-11-07T10:00:00+05:30","notification_failure_details":{"failure_reason":"Unable to notify","failure_error_code":"E1","failure_sub_error_code":"S1"}}`))
		case "/subscriptions/payments/pay1/controlled/executions/e1":
			_, _ = w.Write([]byte(`{"execution_id":"e1","execution_status":"SUCCESS","payment_status":"SUCCESS","execution_completed_time":"2026-11-08T03:00:00"}`))
		default:
			t.Errorf("unexpected path %s", req.URL.Path)
		}
	})
	n, err := r.FetchNotification(context.Background(), "pay1", "n1")
	if err != nil || n.Status != "FAILED" || n.Failure.ErrorCode != "E1" || n.CompletedTime == nil {
		t.Fatalf("n=%+v err=%v", n, err)
	}
	e, err := r.FetchExecution(context.Background(), "pay1", "e1")
	if err != nil || e.Status != "SUCCESS" || e.CompletedTime == nil {
		t.Fatalf("e=%+v err=%v", e, err)
	}
	if e.CompletedTime.Location().String() != "IST" {
		t.Errorf("offset-less time should parse as IST, got %v", e.CompletedTime.Location())
	}
}

func TestAmountConversion(t *testing.T) {
	for paise, rupees := range map[int64]float64{100: 1, 9900: 99, 69900: 699, 9950: 99.5, 1: 0.01} {
		if got := PaiseToRupees(paise); got != rupees {
			t.Errorf("PaiseToRupees(%d)=%v want %v", paise, got, rupees)
		}
		if got := RupeesToPaise(rupees); got != paise {
			t.Errorf("RupeesToPaise(%v)=%d want %d", rupees, got, paise)
		}
	}
	// float noise must not lose a paisa
	if got := RupeesToPaise(0.1 + 0.2); got != 30 {
		t.Errorf("RupeesToPaise(0.30000000000000004)=%d", got)
	}
}

func TestVerifyWebhookSignature(t *testing.T) {
	body := []byte(`{"type":"SUBSCRIPTION_STATUS_CHANGED","data":{"x":170.00}}`)
	ts := "1699999999999"
	sig := SignWebhook("topsecret", ts, body)

	if err := VerifyWebhookSignature("topsecret", ts, body, sig); err != nil {
		t.Errorf("valid signature rejected: %v", err)
	}
	if err := VerifyWebhookSignature("wrong", ts, body, sig); err == nil {
		t.Error("wrong secret accepted")
	}
	if err := VerifyWebhookSignature("topsecret", ts+"1", body, sig); err == nil {
		t.Error("wrong timestamp accepted")
	}
	if err := VerifyWebhookSignature("topsecret", ts, append(body, ' '), sig); err == nil {
		t.Error("tampered body accepted")
	}
	if err := VerifyWebhookSignature("topsecret", ts, body, ""); err == nil {
		t.Error("empty signature accepted")
	}
}

func TestParseTime(t *testing.T) {
	if ParseTime("") != nil || ParseTime("garbage") != nil {
		t.Error("expected nil")
	}
	tm := ParseTime("2026-11-06T10:00:00+05:30")
	if tm == nil || tm.UTC().Hour() != 4 || tm.UTC().Minute() != 30 {
		t.Errorf("got %v", tm)
	}
}

func TestIdempotencyHeaderIsAlwaysAUUID(t *testing.T) {
	a := idempotencyHeader("pay_abc_n1")
	if _, err := uuid.Parse(a); err != nil {
		t.Fatalf("not a UUID: %q", a)
	}
	if a != idempotencyHeader("pay_abc_n1") {
		t.Error("must be deterministic so retries dedupe")
	}
	if a == idempotencyHeader("pay_abc_n2") {
		t.Error("different keys must differ")
	}
	given := "3f2b8c1e-9a4d-4e7b-8c55-0a1b2c3d4e5f"
	if idempotencyHeader(given) != given {
		t.Error("a real UUID must pass through unchanged")
	}
	if idempotencyHeader("") != "" {
		t.Error("empty stays empty")
	}
}

func TestPaymentLinkRawCalls(t *testing.T) {
	var created map[string]interface{}
	r := newTestRaw(t, func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.Method == http.MethodPost && req.URL.Path == "/links":
			b, _ := io.ReadAll(req.Body)
			_ = json.Unmarshal(b, &created)
			// the real API also returns a subscription block whose plan_details is an array
			_, _ = w.Write([]byte(`{"cf_link_id":55566677,"link_id":"l1","link_url":"https://payments.cashfree.com/links/x","link_status":"ACTIVE","link_amount":99.5,"link_amount_paid":0,"subscription":{"plan_details":[]}}`))
		case req.Method == http.MethodGet && req.URL.Path == "/links/l1":
			_, _ = w.Write([]byte(`{"cf_link_id":"55566677","link_id":"l1","link_status":"PAID","link_amount":99.5,"link_amount_paid":99.5}`))
		default:
			t.Errorf("unexpected %s %s", req.Method, req.URL.Path)
		}
	})

	link, err := r.CreatePaymentLink(context.Background(), CreatePaymentLinkParams{
		LinkID: "l1", AmountPaise: 9950, Purpose: "p", CustomerPhone: "9999999999", CustomerEmail: "a@b.c",
		ExpiryTime: time.Date(2026, 11, 7, 10, 0, 0, 0, IST), ReturnURL: "https://x.example/return", IdempotencyKey: "l1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if link.URL == "" || link.Status != "ACTIVE" || link.AmountPaise != 9950 || link.CFLinkID != "55566677" {
		t.Errorf("link = %+v", link)
	}
	if created["link_amount"].(float64) != 99.5 || created["link_currency"] != "INR" || created["link_expiry_time"] != "2026-11-07T10:00:00+05:30" {
		t.Errorf("request body = %v", created)
	}
	if n := created["link_notify"].(map[string]interface{}); n["send_sms"] != false || n["send_email"] != false {
		t.Errorf("Cashfree must not notify the customer itself: %v", n)
	}

	got, err := r.FetchPaymentLink(context.Background(), "l1")
	if err != nil || got.Status != "PAID" || got.AmountPaidPaise != 9950 {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestAPIErrorCarriesTheRateLimitWait(t *testing.T) {
	h := http.Header{}
	h.Set("x-ratelimit-retry", "15")
	if e := newAPIError(429, h, []byte(`{"message":"slow down","code":"rate_limit_error"}`)); e.RetryAfter != 15*time.Second || e.HTTPStatus != 429 {
		t.Fatalf("%+v", e)
	}
	if e := newAPIError(200, http.Header{"X-Ratelimit-Retry": {"0"}}, nil); e.RetryAfter != 0 {
		t.Fatalf("0 means no wait: %+v", e)
	}
}
