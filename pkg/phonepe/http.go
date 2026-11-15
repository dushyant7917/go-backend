package phonepe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go-backend/pkg/retryafter"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// httpDoer is the subset of *http.Client we need; it lets tests inject a transport.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

type client struct {
	creds   Credentials
	baseURL string
	http    httpDoer
	tokens  *tokenSource
}

// New builds a production Client for the credentials. Access tokens are shared across clients that use the
// same credentials.
func New(creds Credentials) Client {
	doer := &http.Client{Timeout: 30 * time.Second}
	return &client{creds: creds, baseURL: creds.BaseURL(), http: doer, tokens: sharedTokenSource(creds, doer)}
}

// NewFactory returns the production ClientFactory.
func NewFactory() ClientFactory { return New }

// newWithHTTP is the test constructor: a custom transport and a private (unshared) token source.
func newWithHTTP(creds Credentials, doer httpDoer, now func() time.Time) *client {
	ts := newTokenSource(creds, doer)
	if now != nil {
		ts.now = now
	}
	return &client{creds: creds, baseURL: creds.BaseURL(), http: doer, tokens: ts}
}

// flexInt64 decodes a JSON number or a quoted number: PhonePe's samples show fields like notifiedAt both ways.
type flexInt64 int64

func (f *flexInt64) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		*f = 0
		return nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		fl, ferr := strconv.ParseFloat(s, 64)
		if ferr != nil {
			return err
		}
		n = int64(fl)
	}
	*f = flexInt64(n)
	return nil
}

func msToTime(ms flexInt64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(int64(ms)).UTC()
}

func timeToMs(t time.Time) int64 { return t.UnixMilli() }

// do sends an authenticated JSON request and decodes a 2xx JSON response into out (may be nil). A 401 drops
// the cached token and retries once with a fresh one.
func (c *client) do(ctx context.Context, method, path string, body, out interface{}) error {
	var payload []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		payload = b
	}

	for attempt := 0; ; attempt++ {
		token, err := c.tokens.Token(ctx)
		if err != nil {
			return err
		}

		var reader io.Reader
		if payload != nil {
			reader = bytes.NewReader(payload)
		}
		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "O-Bearer "+token)
		req.Header.Set("Accept", "application/json")
		if payload != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := c.http.Do(req)
		if err != nil {
			return err
		}
		raw, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return err
		}

		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			c.tokens.Invalidate(token)
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return newAPIError(resp.StatusCode, resp.Header, raw)
		}
		if out == nil || len(raw) == 0 {
			return nil
		}
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
		return nil
	}
}

// newAPIError builds an *APIError from a PhonePe error body. PhonePe error bodies vary between
// {"code","message"} and {"errorCode","message"} shapes, so both are read.
func newAPIError(status int, header http.Header, raw []byte) *APIError {
	apiErr := &APIError{HTTPStatus: status, Body: string(raw), RetryAfter: retryafter.FromHeader(header)}
	var parsed struct {
		Code      string `json:"code"`
		ErrorCode string `json:"errorCode"`
		Message   string `json:"message"`
		Error     string `json:"error_description"`
	}
	if err := json.Unmarshal(raw, &parsed); err == nil {
		apiErr.Code = parsed.Code
		if apiErr.Code == "" {
			apiErr.Code = parsed.ErrorCode
		}
		apiErr.Message = parsed.Message
		if apiErr.Message == "" {
			apiErr.Message = parsed.Error
		}
	}
	if apiErr.Message == "" {
		apiErr.Message = http.StatusText(status)
	}
	return apiErr
}

func metaInfoOrNil(m map[string]string) interface{} {
	if len(m) == 0 {
		return nil
	}
	return m
}

func (c *client) CreateSubscriptionOrder(ctx context.Context, p CreateSubscriptionOrderParams) (*SDKOrder, error) {
	expireAfter := int(p.OrderExpireAfter / time.Second)
	if expireAfter <= 0 {
		expireAfter = 1800
	}
	body := map[string]interface{}{
		"merchantOrderId": p.MerchantOrderID,
		"amount":          p.AmountPaise,
		"expireAfter":     expireAfter,
		"paymentFlow": map[string]interface{}{
			"type": "SUBSCRIPTION_CHECKOUT_SETUP",
			"subscriptionDetails": map[string]interface{}{
				"subscriptionType":       "RECURRING",
				"merchantSubscriptionId": p.MerchantSubscriptionID,
				"authWorkflowType":       "TRANSACTION",
				// VARIABLE because our retries debit a reduced amount below maxAmount.
				"amountType":  "VARIABLE",
				"maxAmount":   p.MaxAmountPaise,
				"frequency":   "ON_DEMAND",
				"productType": "UPI_MANDATE",
				"expireAt":    timeToMs(p.SubscriptionExpiry),
			},
		},
	}
	if mi := metaInfoOrNil(p.MetaInfo); mi != nil {
		body["metaInfo"] = mi
	}
	return c.createSDKOrder(ctx, body)
}

func (c *client) CreateOneTimeOrder(ctx context.Context, p CreateOneTimeOrderParams) (*SDKOrder, error) {
	expireAfter := int(p.OrderExpireAfter / time.Second)
	if expireAfter <= 0 {
		expireAfter = 1800
	}
	flow := map[string]interface{}{"type": "PG_CHECKOUT"}
	if p.RedirectURL != "" {
		flow["merchantUrls"] = map[string]interface{}{"redirectUrl": p.RedirectURL}
	}
	body := map[string]interface{}{
		"merchantOrderId": p.MerchantOrderID,
		"amount":          p.AmountPaise,
		"expireAfter":     expireAfter,
		"paymentFlow":     flow,
	}
	if mi := metaInfoOrNil(p.MetaInfo); mi != nil {
		body["metaInfo"] = mi
	}
	return c.createSDKOrder(ctx, body)
}

func (c *client) createSDKOrder(ctx context.Context, body map[string]interface{}) (*SDKOrder, error) {
	var out struct {
		OrderID  string    `json:"orderId"`
		State    string    `json:"state"`
		Token    string    `json:"token"`
		ExpireAt flexInt64 `json:"expireAt"`
	}
	if err := c.do(ctx, http.MethodPost, "/checkout/v2/sdk/order", body, &out); err != nil {
		return nil, err
	}
	if out.Token == "" {
		return nil, fmt.Errorf("phonepe: sdk/order response had no token")
	}
	return &SDKOrder{OrderID: out.OrderID, State: out.State, Token: out.Token, ExpireAt: msToTime(out.ExpireAt)}, nil
}

type paymentDetailJSON struct {
	TransactionID     string    `json:"transactionId"`
	PaymentMode       string    `json:"paymentMode"`
	Timestamp         flexInt64 `json:"timestamp"`
	Amount            int64     `json:"amount"`
	State             string    `json:"state"`
	ErrorCode         string    `json:"errorCode"`
	DetailedErrorCode string    `json:"detailedErrorCode"`
	Rail              struct {
		UTR string `json:"utr"`
		UMN string `json:"umn"`
	} `json:"rail"`
}

func (c *client) FetchOrder(ctx context.Context, merchantOrderID string) (*Order, error) {
	path := "/checkout/v2/order/" + url.PathEscape(merchantOrderID) + "/status?details=false&errorContext=true"
	var out struct {
		OrderID           string    `json:"orderId"`
		MerchantOrderID   string    `json:"merchantOrderId"`
		State             string    `json:"state"`
		Amount            int64     `json:"amount"`
		ExpireAt          flexInt64 `json:"expireAt"`
		ErrorCode         string    `json:"errorCode"`
		DetailedErrorCode string    `json:"detailedErrorCode"`
		PaymentFlow       struct {
			Type                   string    `json:"type"`
			MerchantSubscriptionID string    `json:"merchantSubscriptionId"`
			SubscriptionID         string    `json:"subscriptionId"`
			ValidAfter             flexInt64 `json:"validAfter"`
			ValidUpto              flexInt64 `json:"validUpto"`
			NotifiedAt             flexInt64 `json:"notifiedAt"`
		} `json:"paymentFlow"`
		PaymentDetails []paymentDetailJSON `json:"paymentDetails"`
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}

	order := &Order{
		OrderID:                out.OrderID,
		MerchantOrderID:        out.MerchantOrderID,
		State:                  out.State,
		AmountPaise:            out.Amount,
		ExpireAt:               msToTime(out.ExpireAt),
		FlowType:               out.PaymentFlow.Type,
		MerchantSubscriptionID: out.PaymentFlow.MerchantSubscriptionID,
		SubscriptionID:         out.PaymentFlow.SubscriptionID,
		ValidAfter:             msToTime(out.PaymentFlow.ValidAfter),
		ValidUpto:              msToTime(out.PaymentFlow.ValidUpto),
		NotifiedAt:             msToTime(out.PaymentFlow.NotifiedAt),
		ErrorCode:              out.ErrorCode,
		DetailedErrorCode:      out.DetailedErrorCode,
	}
	if order.MerchantOrderID == "" {
		order.MerchantOrderID = merchantOrderID
	}
	for _, d := range out.PaymentDetails {
		order.Attempts = append(order.Attempts, PaymentAttempt{
			TransactionID:     d.TransactionID,
			PaymentMode:       d.PaymentMode,
			State:             d.State,
			AmountPaise:       d.Amount,
			Timestamp:         msToTime(d.Timestamp),
			ErrorCode:         d.ErrorCode,
			DetailedErrorCode: d.DetailedErrorCode,
			UTR:               d.Rail.UTR,
			UMN:               d.Rail.UMN,
		})
	}
	return order, nil
}

func (c *client) FetchSubscription(ctx context.Context, merchantSubscriptionID string) (*Subscription, error) {
	path := "/checkout/v2/subscriptions/" + url.PathEscape(merchantSubscriptionID) + "/status"
	var out struct {
		MerchantSubscriptionID string    `json:"merchantSubscriptionId"`
		SubscriptionID         string    `json:"subscriptionId"`
		State                  string    `json:"state"`
		AuthWorkflowType       string    `json:"authWorkflowType"`
		AmountType             string    `json:"amountType"`
		MaxAmount              int64     `json:"maxAmount"`
		Frequency              string    `json:"frequency"`
		ExpireAt               flexInt64 `json:"expireAt"`
		PauseStartDate         flexInt64 `json:"pauseStartDate"`
		PauseEndDate           flexInt64 `json:"pauseEndDate"`
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	sub := &Subscription{
		MerchantSubscriptionID: out.MerchantSubscriptionID,
		SubscriptionID:         out.SubscriptionID,
		State:                  out.State,
		AuthWorkflowType:       out.AuthWorkflowType,
		AmountType:             out.AmountType,
		MaxAmountPaise:         out.MaxAmount,
		Frequency:              out.Frequency,
		ExpireAt:               msToTime(out.ExpireAt),
		PauseStartDate:         msToTime(out.PauseStartDate),
		PauseEndDate:           msToTime(out.PauseEndDate),
	}
	if sub.MerchantSubscriptionID == "" {
		sub.MerchantSubscriptionID = merchantSubscriptionID
	}
	return sub, nil
}

func (c *client) NotifyRedemption(ctx context.Context, p NotifyParams) (*NotifyResult, error) {
	strategy := p.RetryStrategy
	if strategy == "" {
		strategy = RetryStrategyCustom
	}
	body := map[string]interface{}{
		"merchantOrderId": p.MerchantOrderID,
		"amount":          p.AmountPaise,
		"paymentFlow": map[string]interface{}{
			"type":                    "SUBSCRIPTION_CHECKOUT_REDEMPTION",
			"merchantSubscriptionId":  p.MerchantSubscriptionID,
			"redemptionRetryStrategy": strategy,
			// We debit ourselves after the notice period, so PhonePe must not auto-debit.
			"autoDebit": false,
		},
	}
	if !p.ExpireAt.IsZero() {
		body["expireAt"] = timeToMs(p.ExpireAt)
	}
	var out struct {
		OrderID  string    `json:"orderId"`
		State    string    `json:"state"`
		ExpireAt flexInt64 `json:"expireAt"`
	}
	if err := c.do(ctx, http.MethodPost, "/checkout/v2/subscriptions/notify", body, &out); err != nil {
		return nil, err
	}
	return &NotifyResult{OrderID: out.OrderID, State: out.State, ExpireAt: msToTime(out.ExpireAt)}, nil
}

func (c *client) Redeem(ctx context.Context, merchantOrderID string) (*RedeemResult, error) {
	body := map[string]interface{}{"merchantOrderId": merchantOrderID}
	var out struct {
		TransactionID string `json:"transactionId"`
		State         string `json:"state"`
	}
	if err := c.do(ctx, http.MethodPost, "/checkout/v2/subscriptions/redeem", body, &out); err != nil {
		return nil, err
	}
	return &RedeemResult{TransactionID: out.TransactionID, State: out.State}, nil
}

func (c *client) CancelSubscription(ctx context.Context, merchantSubscriptionID string) error {
	path := "/checkout/v2/subscriptions/" + url.PathEscape(merchantSubscriptionID) + "/cancel"
	return c.do(ctx, http.MethodPost, path, nil, nil)
}
