// Package razorpay is a thin HTTP client for the Razorpay APIs the autopay Razorpay adapter needs: customers,
// orders (incl. the pre-debit notification carried by an order), recurring payments, tokens and payment links.
//
// It is plain net/http rather than razorpay-go because the SDK drops the HTTP status and the error code of a failed
// call, and the adapter needs both to tell a transient failure from a rejection. It is exposed through the Client
// interface so the adapter can be tested with a fake.
//
// Amounts are paise (int64) and timestamps are unix seconds on the wire; this package returns time.Time in UTC.
package razorpay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go-backend/pkg/retryafter"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// BaseURL is the Razorpay API root. Test and live mode share it; the key id decides the mode.
const BaseURL = "https://api.razorpay.com/v1"

// Credentials identify a Razorpay merchant account (test or live keys).
type Credentials struct {
	KeyID     string
	KeySecret string
}

// ClientFactory builds a Client for a set of credentials (one per app config).
type ClientFactory func(creds Credentials) Client

// Client is the Razorpay surface the adapter needs.
type Client interface {
	// CreateCustomer returns the customer id for the email+contact (an existing customer is reused, fail_existing=0).
	CreateCustomer(ctx context.Context, email, contact string) (string, error)
	CreateOrder(ctx context.Context, data map[string]any) (*Order, error)
	FetchOrder(ctx context.Context, orderID string) (*Order, error)
	// FindOrderByReceipt returns the order created with the receipt, or nil when there is none.
	FindOrderByReceipt(ctx context.Context, receipt string) (*Order, error)
	FetchOrderPayments(ctx context.Context, orderID string) ([]Payment, error)
	// CreateRecurringPayment debits a token against an order and returns the razorpay payment id.
	CreateRecurringPayment(ctx context.Context, data map[string]any) (string, error)
	FetchToken(ctx context.Context, customerID, tokenID string) (*Token, error)
	// CancelToken asks Razorpay to cancel a UPI AutoPay mandate (PUT /customers/:id/tokens/:id/cancel). It only
	// acknowledges: the answer is "cancellation_initiated" and the final "cancelled" arrives with the token.cancelled
	// webhook. Allowed for tokens that are initiated, confirmed or paused (or cancellation_initiated, to retry).
	CancelToken(ctx context.Context, customerID, tokenID string) (status string, err error)
	CreatePaymentLink(ctx context.Context, data map[string]any) (*PaymentLink, error)
	FetchPaymentLink(ctx context.Context, linkID string) (*PaymentLink, error)
}

// Notes tolerates Razorpay returning an empty notes object as [] and non-string values.
type Notes map[string]string

func (n *Notes) UnmarshalJSON(b []byte) error {
	*n = Notes{}
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil // [] or null: no notes
	}
	for k, v := range m {
		if s, ok := v.(string); ok {
			(*n)[k] = s
		} else if v != nil {
			(*n)[k] = fmt.Sprint(v)
		}
	}
	return nil
}

// Order is the subset of a Razorpay order we use.
type Order struct {
	ID         string
	Status     string // created | attempted | paid
	Amount     int64
	AmountPaid int64
	Attempts   int64
	Receipt    string
	Notes      Notes
	// Notification is the pre-debit notification carried by an order created with notification{token_id,payment_after}.
	Notification *OrderNotification
}

// OrderNotification is the state of the pre-debit notification of an order.
type OrderNotification struct {
	TokenID       string
	Status        string
	PaymentAfter  time.Time
	DeliveredAt   time.Time
	FailureReason string
}

// Payment is the subset of a Razorpay payment we use.
type Payment struct {
	ID               string
	OrderID          string
	Status           string // created | authorized | captured | refunded | failed
	Amount           int64
	TokenID          string
	CustomerID       string
	ErrorCode        string
	ErrorDescription string
	ErrorReason      string
	CreatedAt        time.Time
}

// Token is a recurring-payment token (the UPI mandate).
type Token struct {
	ID            string
	Status        string // recurring_details.status: initiated | confirmed | rejected | cancelled | paused | ...
	FailureReason string
}

// PaymentLink is a hosted one-time payment page.
type PaymentLink struct {
	ID         string
	Status     string // created | partially_paid | paid | expired | cancelled
	ShortURL   string
	AmountPaid int64
	PaymentIDs []string
}

// APIError is an error response from Razorpay.
type APIError struct {
	HTTPStatus  int
	Code        string // BAD_REQUEST_ERROR | GATEWAY_ERROR | SERVER_ERROR
	Description string
	Reason      string
	Source      string
	Step        string
	Body        string
	RetryAfter  time.Duration
}

func (e *APIError) Error() string {
	return fmt.Sprintf("razorpay: http %d %s (%s): %s", e.HTTPStatus, e.Code, e.Reason, e.Description)
}

// IsRetryable reports whether repeating the same request may succeed (rate limit, server or gateway error).
func (e *APIError) IsRetryable() bool {
	return e.HTTPStatus == 429 || e.HTTPStatus >= 500 || strings.EqualFold(e.Code, "SERVER_ERROR") || strings.EqualFold(e.Code, "GATEWAY_ERROR")
}

// AsAPIError extracts an *APIError from err's chain.
func AsAPIError(err error) (*APIError, bool) {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr, true
	}
	return nil, false
}

type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

type client struct {
	creds   Credentials
	baseURL string
	http    httpDoer
}

// New builds a production Client.
func New(creds Credentials) Client {
	return &client{creds: creds, baseURL: BaseURL, http: &http.Client{Timeout: 30 * time.Second}}
}

// NewFactory returns the production ClientFactory.
func NewFactory() ClientFactory { return New }

func newWithHTTP(creds Credentials, baseURL string, doer httpDoer) *client {
	return &client{creds: creds, baseURL: baseURL, http: doer}
}

func (c *client) do(ctx context.Context, method, path string, query url.Values, body, out interface{}) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("razorpay: marshal request: %w", err)
		}
		rdr = bytes.NewReader(b)
	}
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.creds.KeyID, c.creds.KeySecret)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return newAPIError(resp.StatusCode, resp.Header, raw)
	}
	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("razorpay: decode response: %w", err)
	}
	return nil
}

func newAPIError(status int, header http.Header, raw []byte) *APIError {
	e := &APIError{HTTPStatus: status, Body: truncate(string(raw), 2000), RetryAfter: retryafter.FromHeader(header)}
	var env struct {
		Error struct {
			Code        string `json:"code"`
			Description string `json:"description"`
			Reason      string `json:"reason"`
			Source      string `json:"source"`
			Step        string `json:"step"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &env) == nil {
		e.Code, e.Description, e.Reason, e.Source, e.Step = env.Error.Code, env.Error.Description, env.Error.Reason, env.Error.Source, env.Error.Step
	}
	if e.Description == "" {
		e.Description = truncate(string(raw), 300)
	}
	return e
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func unix(sec int64) time.Time {
	if sec <= 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0).UTC()
}

// ---- wire shapes ----

type orderJSON struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	Amount       int64  `json:"amount"`
	AmountPaid   int64  `json:"amount_paid"`
	Attempts     int64  `json:"attempts"`
	Receipt      string `json:"receipt"`
	Notes        Notes  `json:"notes"`
	Notification *struct {
		TokenID       string `json:"token_id"`
		Status        string `json:"status"`
		PaymentAfter  int64  `json:"payment_after"`
		DeliveredAt   int64  `json:"delivered_at"`
		FailureReason string `json:"failure_reason"`
	} `json:"notification"`
}

func (o orderJSON) order() *Order {
	out := &Order{ID: o.ID, Status: o.Status, Amount: o.Amount, AmountPaid: o.AmountPaid, Attempts: o.Attempts, Receipt: o.Receipt, Notes: o.Notes}
	if n := o.Notification; n != nil {
		out.Notification = &OrderNotification{TokenID: n.TokenID, Status: n.Status, PaymentAfter: unix(n.PaymentAfter), DeliveredAt: unix(n.DeliveredAt), FailureReason: n.FailureReason}
	}
	return out
}

type paymentJSON struct {
	ID               string `json:"id"`
	OrderID          string `json:"order_id"`
	Status           string `json:"status"`
	Amount           int64  `json:"amount"`
	TokenID          string `json:"token_id"`
	CustomerID       string `json:"customer_id"`
	ErrorCode        string `json:"error_code"`
	ErrorDescription string `json:"error_description"`
	ErrorReason      string `json:"error_reason"`
	CreatedAt        int64  `json:"created_at"`
}

func (p paymentJSON) payment() Payment {
	return Payment{ID: p.ID, OrderID: p.OrderID, Status: p.Status, Amount: p.Amount, TokenID: p.TokenID, CustomerID: p.CustomerID,
		ErrorCode: p.ErrorCode, ErrorDescription: p.ErrorDescription, ErrorReason: p.ErrorReason, CreatedAt: unix(p.CreatedAt)}
}

// ---- calls ----

func (c *client) CreateCustomer(ctx context.Context, email, contact string) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	if err := c.do(ctx, http.MethodPost, "/customers", nil, map[string]any{"email": email, "contact": contact, "fail_existing": "0"}, &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", errors.New("razorpay: customer response has no id")
	}
	return out.ID, nil
}

func (c *client) CreateOrder(ctx context.Context, data map[string]any) (*Order, error) {
	var out orderJSON
	if err := c.do(ctx, http.MethodPost, "/orders", nil, data, &out); err != nil {
		return nil, err
	}
	if out.ID == "" {
		return nil, errors.New("razorpay: order response has no id")
	}
	return out.order(), nil
}

func (c *client) FetchOrder(ctx context.Context, orderID string) (*Order, error) {
	var out orderJSON
	if err := c.do(ctx, http.MethodGet, "/orders/"+url.PathEscape(orderID), nil, nil, &out); err != nil {
		return nil, err
	}
	return out.order(), nil
}

func (c *client) FindOrderByReceipt(ctx context.Context, receipt string) (*Order, error) {
	var out struct {
		Items []orderJSON `json:"items"`
	}
	if err := c.do(ctx, http.MethodGet, "/orders", url.Values{"receipt": {receipt}, "count": {"1"}}, nil, &out); err != nil {
		return nil, err
	}
	if len(out.Items) == 0 {
		return nil, nil
	}
	return out.Items[0].order(), nil
}

func (c *client) FetchOrderPayments(ctx context.Context, orderID string) ([]Payment, error) {
	var out struct {
		Items []paymentJSON `json:"items"`
	}
	if err := c.do(ctx, http.MethodGet, "/orders/"+url.PathEscape(orderID)+"/payments", nil, nil, &out); err != nil {
		return nil, err
	}
	payments := make([]Payment, 0, len(out.Items))
	for _, p := range out.Items {
		payments = append(payments, p.payment())
	}
	return payments, nil
}

func (c *client) CreateRecurringPayment(ctx context.Context, data map[string]any) (string, error) {
	var out struct {
		PaymentID string `json:"razorpay_payment_id"`
		ID        string `json:"id"`
	}
	if err := c.do(ctx, http.MethodPost, "/payments/create/recurring", nil, data, &out); err != nil {
		return "", err
	}
	if out.PaymentID != "" {
		return out.PaymentID, nil
	}
	return out.ID, nil
}

func (c *client) FetchToken(ctx context.Context, customerID, tokenID string) (*Token, error) {
	var out struct {
		ID               string `json:"id"`
		RecurringDetails struct {
			Status        string `json:"status"`
			FailureReason string `json:"failure_reason"`
		} `json:"recurring_details"`
	}
	if err := c.do(ctx, http.MethodGet, "/customers/"+url.PathEscape(customerID)+"/tokens/"+url.PathEscape(tokenID), nil, nil, &out); err != nil {
		return nil, err
	}
	return &Token{ID: out.ID, Status: out.RecurringDetails.Status, FailureReason: out.RecurringDetails.FailureReason}, nil
}

func (c *client) CancelToken(ctx context.Context, customerID, tokenID string) (string, error) {
	var out struct {
		Status string `json:"status"`
	}
	path := "/customers/" + url.PathEscape(customerID) + "/tokens/" + url.PathEscape(tokenID) + "/cancel"
	if err := c.do(ctx, http.MethodPut, path, nil, nil, &out); err != nil {
		return "", err
	}
	return out.Status, nil
}

type linkJSON struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	ShortURL   string `json:"short_url"`
	AmountPaid int64  `json:"amount_paid"`
	Payments   []struct {
		PaymentID string `json:"payment_id"`
	} `json:"payments"`
}

func (l linkJSON) link() *PaymentLink {
	out := &PaymentLink{ID: l.ID, Status: l.Status, ShortURL: l.ShortURL, AmountPaid: l.AmountPaid}
	for _, p := range l.Payments {
		out.PaymentIDs = append(out.PaymentIDs, p.PaymentID)
	}
	return out
}

func (c *client) CreatePaymentLink(ctx context.Context, data map[string]any) (*PaymentLink, error) {
	var out linkJSON
	if err := c.do(ctx, http.MethodPost, "/payment_links", nil, data, &out); err != nil {
		return nil, err
	}
	return out.link(), nil
}

func (c *client) FetchPaymentLink(ctx context.Context, linkID string) (*PaymentLink, error) {
	var out linkJSON
	if err := c.do(ctx, http.MethodGet, "/payment_links/"+url.PathEscape(linkID), nil, nil, &out); err != nil {
		return nil, err
	}
	return out.link(), nil
}
