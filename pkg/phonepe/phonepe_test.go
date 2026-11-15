package phonepe

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type recordedRequest struct {
	Method string
	URL    string
	Header http.Header
	Body   string
}

// fakeDoer replays canned responses by "METHOD path-prefix" and records every request.
type fakeDoer struct {
	mu        sync.Mutex
	requests  []recordedRequest
	responses map[string][]fakeResponse
}

type fakeResponse struct {
	status int
	body   string
}

func newFakeDoer() *fakeDoer { return &fakeDoer{responses: map[string][]fakeResponse{}} }

func (f *fakeDoer) on(key string, status int, body string) {
	f.responses[key] = append(f.responses[key], fakeResponse{status, body})
}

func (f *fakeDoer) Do(req *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var body string
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		body = string(b)
	}
	f.requests = append(f.requests, recordedRequest{req.Method, req.URL.String(), req.Header.Clone(), body})

	key := req.Method + " " + req.URL.Path
	if strings.Contains(req.URL.Path, "oauth/token") {
		key = "TOKEN"
	}
	queue := f.responses[key]
	if len(queue) == 0 {
		return &http.Response{StatusCode: 500, Body: io.NopCloser(strings.NewReader(`{"code":"NO_FAKE","message":"no response for ` + key + `"}`))}, nil
	}
	resp := queue[0]
	if len(queue) > 1 {
		f.responses[key] = queue[1:]
	}
	return &http.Response{StatusCode: resp.status, Body: io.NopCloser(strings.NewReader(resp.body))}, nil
}

func (f *fakeDoer) count(prefix string) int {
	n := 0
	for _, r := range f.requests {
		if strings.HasPrefix(r.Method+" "+r.URL, prefix) {
			n++
		}
	}
	return n
}

func tokenBody(token string, expiresAt time.Time) string {
	return `{"access_token":"` + token + `","expires_at":` + itoa64(expiresAt.Unix()) + `,"token_type":"O-Bearer"}`
}

func itoa64(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func testCreds(sandbox bool) Credentials {
	return Credentials{ClientID: "cid", ClientSecret: "sec", ClientVersion: 1, Sandbox: sandbox}
}

func TestEnvironmentURLs(t *testing.T) {
	sb, pr := testCreds(true), testCreds(false)
	if sb.BaseURL() != SandboxBaseURL || pr.BaseURL() != ProductionBaseURL {
		t.Fatalf("base urls: %s / %s", sb.BaseURL(), pr.BaseURL())
	}
	// Production auth lives on identity-manager, not under /apis/pg.
	if !strings.Contains(pr.TokenURL(), "identity-manager") || strings.Contains(sb.TokenURL(), "identity-manager") {
		t.Fatalf("token urls: %s / %s", sb.TokenURL(), pr.TokenURL())
	}
}

func TestTokenIsCachedAndRefreshedBeforeExpiry(t *testing.T) {
	now := time.Date(2026, 11, 7, 0, 0, 0, 0, time.UTC)
	f := newFakeDoer()
	f.on("TOKEN", 200, tokenBody("tok1", now.Add(2*time.Hour)))
	f.on("TOKEN", 200, tokenBody("tok2", now.Add(10*time.Hour)))
	f.on("GET /apis/pg-sandbox/checkout/v2/subscriptions/s1/status", 200, `{"state":"ACTIVE"}`)

	clock := now
	c := newWithHTTP(testCreds(true), f, func() time.Time { return clock })

	for i := 0; i < 3; i++ {
		if _, err := c.FetchSubscription(context.Background(), "s1"); err != nil {
			t.Fatal(err)
		}
	}
	if got := f.count("POST https://api-preprod.phonepe.com/apis/pg-sandbox/v1/oauth/token"); got != 1 {
		t.Fatalf("token fetched %d times, want 1 (cached)", got)
	}
	if auth := f.requests[1].Header.Get("Authorization"); auth != "O-Bearer tok1" {
		t.Fatalf("Authorization = %q", auth)
	}

	// 30s before expiry is inside the 60s skew, so the token must be refreshed.
	clock = now.Add(2*time.Hour - 30*time.Second)
	if _, err := c.FetchSubscription(context.Background(), "s1"); err != nil {
		t.Fatal(err)
	}
	if got := f.count("POST https://api-preprod.phonepe.com/apis/pg-sandbox/v1/oauth/token"); got != 2 {
		t.Fatalf("token fetched %d times, want 2 after refresh window", got)
	}
}

func TestTokenRequestIsFormEncodedWithVersion(t *testing.T) {
	now := time.Now()
	f := newFakeDoer()
	f.on("TOKEN", 200, tokenBody("t", now.Add(time.Hour)))
	f.on("GET /apis/pg/checkout/v2/subscriptions/s/status", 200, `{}`)
	c := newWithHTTP(testCreds(false), f, nil)
	if _, err := c.FetchSubscription(context.Background(), "s"); err != nil {
		t.Fatal(err)
	}
	tr := f.requests[0]
	if !strings.Contains(tr.URL, "identity-manager") {
		t.Fatalf("prod token url = %s", tr.URL)
	}
	if tr.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
		t.Fatalf("content type = %s", tr.Header.Get("Content-Type"))
	}
	for _, want := range []string{"client_id=cid", "client_secret=sec", "client_version=1", "grant_type=client_credentials"} {
		if !strings.Contains(tr.Body, want) {
			t.Errorf("token body %q missing %q", tr.Body, want)
		}
	}
}

func TestUnauthorizedRefreshesTokenOnce(t *testing.T) {
	now := time.Now()
	f := newFakeDoer()
	f.on("TOKEN", 200, tokenBody("old", now.Add(time.Hour)))
	f.on("TOKEN", 200, tokenBody("new", now.Add(time.Hour)))
	f.on("GET /apis/pg-sandbox/checkout/v2/subscriptions/s/status", 401, `{"code":"AUTHORIZATION_FAILED","message":"expired"}`)
	f.on("GET /apis/pg-sandbox/checkout/v2/subscriptions/s/status", 200, `{"state":"ACTIVE"}`)
	c := newWithHTTP(testCreds(true), f, nil)

	sub, err := c.FetchSubscription(context.Background(), "s")
	if err != nil {
		t.Fatal(err)
	}
	if sub.State != SubscriptionActive {
		t.Fatalf("state = %s", sub.State)
	}
	last := f.requests[len(f.requests)-1]
	if last.Header.Get("Authorization") != "O-Bearer new" {
		t.Fatalf("retry used %q", last.Header.Get("Authorization"))
	}
}

func TestCreateSubscriptionOrderBody(t *testing.T) {
	f := newFakeDoer()
	f.on("TOKEN", 200, tokenBody("t", time.Now().Add(time.Hour)))
	f.on("POST /apis/pg-sandbox/checkout/v2/sdk/order", 200, `{"orderId":"OMO1","state":"PENDING","token":"sdk-token","expireAt":1703756259307}`)
	c := newWithHTTP(testCreds(true), f, nil)

	exp := time.Date(2027, 11, 7, 0, 0, 0, 0, time.UTC)
	order, err := c.CreateSubscriptionOrder(context.Background(), CreateSubscriptionOrderParams{
		MerchantOrderID: "auth_1", MerchantSubscriptionID: "pesub_1",
		AmountPaise: 100, MaxAmountPaise: 49900, SubscriptionExpiry: exp, OrderExpireAfter: 30 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if order.Token != "sdk-token" || order.OrderID != "OMO1" {
		t.Fatalf("order = %+v", order)
	}

	var body map[string]interface{}
	if err := json.Unmarshal([]byte(f.requests[1].Body), &body); err != nil {
		t.Fatal(err)
	}
	if body["amount"].(float64) != 100 || body["expireAfter"].(float64) != 1800 {
		t.Fatalf("body = %v", body)
	}
	flow := body["paymentFlow"].(map[string]interface{})
	if flow["type"] != "SUBSCRIPTION_CHECKOUT_SETUP" {
		t.Fatalf("flow = %v", flow)
	}
	sd := flow["subscriptionDetails"].(map[string]interface{})
	want := map[string]interface{}{
		"subscriptionType": "RECURRING", "merchantSubscriptionId": "pesub_1", "authWorkflowType": "TRANSACTION",
		"amountType": "VARIABLE", "frequency": "ON_DEMAND", "productType": "UPI_MANDATE",
	}
	for k, v := range want {
		if sd[k] != v {
			t.Errorf("subscriptionDetails[%s] = %v, want %v", k, sd[k], v)
		}
	}
	if sd["maxAmount"].(float64) != 49900 || int64(sd["expireAt"].(float64)) != exp.UnixMilli() {
		t.Errorf("subscriptionDetails = %v", sd)
	}
}

func TestSDKOrderWithoutTokenFails(t *testing.T) {
	f := newFakeDoer()
	f.on("TOKEN", 200, tokenBody("t", time.Now().Add(time.Hour)))
	f.on("POST /apis/pg-sandbox/checkout/v2/sdk/order", 200, `{"orderId":"OMO1","state":"PENDING"}`)
	c := newWithHTTP(testCreds(true), f, nil)
	if _, err := c.CreateOneTimeOrder(context.Background(), CreateOneTimeOrderParams{MerchantOrderID: "o", AmountPaise: 100}); err == nil {
		t.Fatal("want error for missing token")
	}
}

func TestNotifyBodyUsesCustomRetryAndNoAutoDebit(t *testing.T) {
	f := newFakeDoer()
	f.on("TOKEN", 200, tokenBody("t", time.Now().Add(time.Hour)))
	f.on("POST /apis/pg-sandbox/checkout/v2/subscriptions/notify", 200, `{"orderId":"OMO2","state":"NOTIFICATION_IN_PROGRESS","expireAt":1773309503656}`)
	c := newWithHTTP(testCreds(true), f, nil)

	exp := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	res, err := c.NotifyRedemption(context.Background(), NotifyParams{
		MerchantOrderID: "pay_1_a1", MerchantSubscriptionID: "pesub_1", AmountPaise: 49900, ExpireAt: exp,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.OrderID != "OMO2" || res.State != OrderNotificationInProgress {
		t.Fatalf("res = %+v", res)
	}
	var body map[string]interface{}
	_ = json.Unmarshal([]byte(f.requests[1].Body), &body)
	flow := body["paymentFlow"].(map[string]interface{})
	if flow["type"] != "SUBSCRIPTION_CHECKOUT_REDEMPTION" || flow["redemptionRetryStrategy"] != "CUSTOM" || flow["autoDebit"] != false {
		t.Fatalf("flow = %v", flow)
	}
	if int64(body["expireAt"].(float64)) != exp.UnixMilli() {
		t.Fatalf("expireAt = %v", body["expireAt"])
	}
}

func TestRedeemAndCancel(t *testing.T) {
	f := newFakeDoer()
	f.on("TOKEN", 200, tokenBody("t", time.Now().Add(time.Hour)))
	f.on("POST /apis/pg-sandbox/checkout/v2/subscriptions/redeem", 200, `{"transactionId":"OM1","state":"PENDING"}`)
	f.on("POST /apis/pg-sandbox/checkout/v2/subscriptions/pesub_1/cancel", 204, ``)
	c := newWithHTTP(testCreds(true), f, nil)

	r, err := c.Redeem(context.Background(), "pay_1_a1")
	if err != nil || r.TransactionID != "OM1" || r.State != OrderPending {
		t.Fatalf("redeem = %+v, %v", r, err)
	}
	if err := c.CancelSubscription(context.Background(), "pesub_1"); err != nil {
		t.Fatalf("cancel (204) should succeed: %v", err)
	}
}

func TestFetchOrderParsesRedemptionAndFailure(t *testing.T) {
	f := newFakeDoer()
	f.on("TOKEN", 200, tokenBody("t", time.Now().Add(time.Hour)))
	f.on("GET /apis/pg-sandbox/checkout/v2/order/pay_1_a1/status", 200, `{
		"orderId":"OMO3","merchantOrderId":"pay_1_a1","state":"COMPLETED","amount":49900,"expireAt":1772537868652,
		"paymentFlow":{"type":"SUBSCRIPTION_CHECKOUT_REDEMPTION","merchantSubscriptionId":"pesub_1",
			"validAfter":1772278669000,"validUpto":1772537868000,"notifiedAt":"1772192269095"},
		"paymentDetails":[
			{"transactionId":"OM-old","paymentMode":"UPI_AUTO_PAY","timestamp":1772427000000,"amount":49900,"state":"FAILED","errorCode":"INSUFFICIENT_BALANCE","detailedErrorCode":"Z9"},
			{"transactionId":"OM-new","paymentMode":"UPI_AUTO_PAY","timestamp":1772427051478,"amount":49900,"state":"COMPLETED","rail":{"type":"UPI","utr":"606132842672","umn":"abc@axl"}}
		]}`)
	c := newWithHTTP(testCreds(true), f, nil)

	o, err := c.FetchOrder(context.Background(), "pay_1_a1")
	if err != nil {
		t.Fatal(err)
	}
	if o.State != OrderCompleted || !o.IsTerminal() || o.AmountPaise != 49900 {
		t.Fatalf("order = %+v", o)
	}
	if o.ValidAfter.IsZero() || o.ValidUpto.IsZero() || o.NotifiedAt.UnixMilli() != 1772192269095 {
		t.Fatalf("window = %v %v %v", o.ValidAfter, o.ValidUpto, o.NotifiedAt)
	}
	latest := o.LatestAttempt()
	if latest == nil || latest.TransactionID != "OM-new" || latest.UTR != "606132842672" || latest.UMN != "abc@axl" {
		t.Fatalf("latest = %+v", latest)
	}
}

func TestFailureCodesFallBackToLatestAttempt(t *testing.T) {
	o := &Order{State: OrderFailed, Attempts: []PaymentAttempt{{ErrorCode: "INSUFFICIENT_BALANCE", DetailedErrorCode: "Z9"}}}
	if code, detail := o.FailureCodes(); code != "INSUFFICIENT_BALANCE" || detail != "Z9" {
		t.Fatalf("codes = %s %s", code, detail)
	}
	o.ErrorCode, o.DetailedErrorCode = "OTHERS", "INTENT_EXPIRED"
	if code, _ := o.FailureCodes(); code != "OTHERS" {
		t.Fatalf("order-level code should win, got %s", code)
	}
}

func TestOrderIsTerminal(t *testing.T) {
	for state, want := range map[string]bool{
		OrderPending: false, OrderNotificationInProgress: false, OrderNotified: false,
		OrderCompleted: true, OrderFailed: true, OrderExpired: true,
	} {
		if got := (&Order{State: state}).IsTerminal(); got != want {
			t.Errorf("%s terminal = %v, want %v", state, got, want)
		}
	}
}

func TestAPIErrorParsing(t *testing.T) {
	f := newFakeDoer()
	f.on("TOKEN", 200, tokenBody("t", time.Now().Add(time.Hour)))
	f.on("POST /apis/pg-sandbox/checkout/v2/subscriptions/notify", 417, `{"code":"INVALID_TRANSACTION_ID","message":"duplicate"}`)
	f.on("POST /apis/pg-sandbox/checkout/v2/subscriptions/redeem", 500, `{"errorCode":"INTERNAL_SERVER_ERROR","message":"boom"}`)
	c := newWithHTTP(testCreds(true), f, nil)

	_, err := c.NotifyRedemption(context.Background(), NotifyParams{MerchantOrderID: "o", MerchantSubscriptionID: "s", AmountPaise: 100})
	apiErr, ok := AsAPIError(err)
	if !ok || !apiErr.IsDuplicate() || apiErr.IsRetryable() || apiErr.HTTPStatus != 417 {
		t.Fatalf("notify err = %#v", err)
	}

	_, err = c.Redeem(context.Background(), "o")
	apiErr, ok = AsAPIError(err)
	if !ok || !apiErr.IsRetryable() || apiErr.Code != "INTERNAL_SERVER_ERROR" || apiErr.IsDuplicate() {
		t.Fatalf("redeem err = %#v", err)
	}
}

func TestWebhookSignature(t *testing.T) {
	body := []byte(`{"event":"checkout.order.completed","payload":{"merchantOrderId":"o1"}}`)
	hexSig := SignWebhook("secret", body)
	if len(hexSig) != 64 {
		t.Fatalf("signature = %q", hexSig)
	}
	mac := hmac.New(sha256.New, []byte("secret"))
	mac.Write(body)
	raw := mac.Sum(nil)

	accepted := map[string]string{
		"hex":             hexSig,
		"upper hex":       strings.ToUpper(hexSig),
		"prefixed":        "sha256=" + hexSig,
		"prefixed upper":  "SHA256=" + hexSig,
		"padded":          "  " + hexSig + " ",
		"base64":          base64.StdEncoding.EncodeToString(raw),
		"base64 url":      base64.URLEncoding.EncodeToString(raw),
		"base64 raw":      base64.RawStdEncoding.EncodeToString(raw),
		"base64 prefixed": "sha256=" + base64.StdEncoding.EncodeToString(raw),
	}
	for name, sig := range accepted {
		if !VerifyWebhookSignature("secret", body, sig) {
			t.Errorf("%s signature rejected", name)
		}
	}

	otherBody := []byte(`{"event":"checkout.order.completed","payload":{"merchantOrderId":"o2"}}`)
	rejected := map[string]struct {
		secret string
		body   []byte
		sig    string
	}{
		"tampered body":  {"secret", otherBody, hexSig},
		"wrong secret":   {"other", body, hexSig},
		"empty":          {"secret", body, ""},
		"only prefix":    {"secret", body, "sha256="},
		"garbage":        {"secret", body, "nope"},
		"truncated":      {"secret", body, hexSig[:40]},
		"no secret":      {"", body, SignWebhook("", body)},
		"signature only": {"secret", body, base64.StdEncoding.EncodeToString([]byte("short"))},
	}
	for name, c := range rejected {
		if VerifyWebhookSignature(c.secret, c.body, c.sig) {
			t.Errorf("%s accepted", name)
		}
	}
}
