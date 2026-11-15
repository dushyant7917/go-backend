package cashfree

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go-backend/pkg/retryafter"
	"io"
	"net/http"
	"net/url"
	"time"
)

// httpDoer is the subset of *http.Client we need; it lets tests inject a transport.
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// rawClient implements the controlled-flow endpoints the SDK doesn't cover.
type rawClient struct {
	creds   Credentials
	baseURL string
	http    httpDoer
}

func newRawClient(creds Credentials) *rawClient {
	return &rawClient{
		creds:   creds,
		baseURL: creds.BaseURL(),
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// do sends a JSON request and decodes a 2xx JSON response into out (may be nil).
func (r *rawClient) do(ctx context.Context, method, path, idempotencyKey string, body, out interface{}) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		reader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, r.baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("x-api-version", APIVersion)
	req.Header.Set("x-client-id", r.creds.ClientID)
	req.Header.Set("x-client-secret", r.creds.ClientSecret)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key := idempotencyHeader(idempotencyKey); key != "" {
		req.Header.Set("x-idempotency-key", key)
	}

	resp, err := r.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
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

// newAPIError builds an *APIError from a Cashfree error body: {"message","code","type","help"}.
func newAPIError(status int, header http.Header, raw []byte) *APIError {
	apiErr := &APIError{HTTPStatus: status, Body: string(raw), RetryAfter: retryafter.FromHeader(header, "x-ratelimit-retry", "x-ratelimit-reset")}
	var parsed struct {
		Message string `json:"message"`
		Code    string `json:"code"`
		Type    string `json:"type"`
	}
	if err := json.Unmarshal(raw, &parsed); err == nil {
		apiErr.Message, apiErr.Code, apiErr.Type = parsed.Message, parsed.Code, parsed.Type
	}
	if apiErr.Message == "" {
		apiErr.Message = http.StatusText(status)
	}
	return apiErr
}

type failureDetailsJSON struct {
	FailureReason       string `json:"failure_reason"`
	FailureErrorCode    string `json:"failure_error_code"`
	FailureSubErrorCode string `json:"failure_sub_error_code"`
}

func (f failureDetailsJSON) toFailure() FailureDetails {
	return FailureDetails{Reason: f.FailureReason, ErrorCode: f.FailureErrorCode, SubErrorCode: f.FailureSubErrorCode}
}

func (r *rawClient) NotifyMandate(ctx context.Context, p NotifyParams) (*NotifyResult, error) {
	body := map[string]interface{}{
		"subscription_id": p.SubscriptionID,
		"payment_id":      p.PaymentID,
		"notification_id": p.NotificationID,
		"payment_amount":  PaiseToRupees(p.AmountPaise),
	}
	if !p.ScheduleDate.IsZero() {
		body["payment_schedule_date"] = p.ScheduleDate.In(IST).Format("2006-01-02T15:04:05-07:00")
	}
	if p.Remarks != "" {
		body["payment_remarks"] = p.Remarks
	}

	var out struct {
		CFNotificationID   string `json:"cf_notification_id"`
		CFPaymentID        string `json:"cf_payment_id"`
		NotificationID     string `json:"notification_id"`
		NotificationStatus string `json:"notification_status"`
		PaymentStatus      string `json:"payment_status"`
	}
	if err := r.do(ctx, http.MethodPost, "/subscriptions/pay/controlled/notify-mandate", p.IdempotencyKey, body, &out); err != nil {
		return nil, err
	}
	return &NotifyResult{
		CFNotificationID: out.CFNotificationID,
		CFPaymentID:      out.CFPaymentID,
		NotificationID:   out.NotificationID,
		Status:           out.NotificationStatus,
		PaymentStatus:    out.PaymentStatus,
	}, nil
}

func (r *rawClient) ExecuteMandate(ctx context.Context, p ExecuteParams) (*ExecuteResult, error) {
	body := map[string]interface{}{
		"execution_id": p.ExecutionID,
		"payment_id":   p.PaymentID,
	}
	var out struct {
		CFExecutionID   string `json:"cf_execution_id"`
		ExecutionID     string `json:"execution_id"`
		ExecutionStatus string `json:"execution_status"`
		PaymentStatus   string `json:"payment_status"`
	}
	if err := r.do(ctx, http.MethodPost, "/subscriptions/pay/controlled/execute-mandate", p.IdempotencyKey, body, &out); err != nil {
		return nil, err
	}
	return &ExecuteResult{
		CFExecutionID: out.CFExecutionID,
		ExecutionID:   out.ExecutionID,
		Status:        out.ExecutionStatus,
		PaymentStatus: out.PaymentStatus,
	}, nil
}

func (r *rawClient) FetchNotification(ctx context.Context, paymentID, notificationID string) (*Notification, error) {
	path := fmt.Sprintf("/subscriptions/payments/%s/controlled/notifications/%s", url.PathEscape(paymentID), url.PathEscape(notificationID))
	var out struct {
		NotificationID        string             `json:"notification_id"`
		CFNotificationID      string             `json:"cf_notification_id"`
		CFPaymentID           string             `json:"cf_payment_id"`
		NotificationStatus    string             `json:"notification_status"`
		PaymentStatus         string             `json:"payment_status"`
		NotificationCompleted string             `json:"notification_completed_time"`
		Failure               failureDetailsJSON `json:"notification_failure_details"`
	}
	if err := r.do(ctx, http.MethodGet, path, "", nil, &out); err != nil {
		return nil, err
	}
	return &Notification{
		NotificationID:   out.NotificationID,
		CFNotificationID: out.CFNotificationID,
		CFPaymentID:      out.CFPaymentID,
		Status:           out.NotificationStatus,
		PaymentStatus:    out.PaymentStatus,
		CompletedTime:    ParseTime(out.NotificationCompleted),
		Failure:          out.Failure.toFailure(),
	}, nil
}

func (r *rawClient) FetchExecution(ctx context.Context, paymentID, executionID string) (*Execution, error) {
	path := fmt.Sprintf("/subscriptions/payments/%s/controlled/executions/%s", url.PathEscape(paymentID), url.PathEscape(executionID))
	var out struct {
		ExecutionID     string             `json:"execution_id"`
		CFExecutionID   string             `json:"cf_execution_id"`
		CFPaymentID     string             `json:"cf_payment_id"`
		ExecutionStatus string             `json:"execution_status"`
		PaymentStatus   string             `json:"payment_status"`
		Completed       string             `json:"execution_completed_time"`
		Failure         failureDetailsJSON `json:"failure_details"`
	}
	if err := r.do(ctx, http.MethodGet, path, "", nil, &out); err != nil {
		return nil, err
	}
	return &Execution{
		ExecutionID:   out.ExecutionID,
		CFExecutionID: out.CFExecutionID,
		CFPaymentID:   out.CFPaymentID,
		Status:        out.ExecutionStatus,
		PaymentStatus: out.PaymentStatus,
		CompletedTime: ParseTime(out.Completed),
		Failure:       out.Failure.toFailure(),
	}, nil
}

// Payment links go through plain HTTP rather than the SDK: the SDK's link response model cannot decode
// Cashfree's actual payload (it expects subscription.plan_details to be an object, the API sends an array),
// so every create/fetch would "fail" after succeeding.
type paymentLinkJSON struct {
	CFLinkID       interface{} `json:"cf_link_id"`
	LinkID         string      `json:"link_id"`
	LinkURL        string      `json:"link_url"`
	LinkStatus     string      `json:"link_status"`
	LinkAmount     float64     `json:"link_amount"`
	LinkAmountPaid float64     `json:"link_amount_paid"`
}

func (l paymentLinkJSON) toLink() *PaymentLink {
	cfID := ""
	switch v := l.CFLinkID.(type) {
	case string:
		cfID = v
	case float64:
		cfID = fmt.Sprintf("%.0f", v)
	}
	return &PaymentLink{
		LinkID:          l.LinkID,
		CFLinkID:        cfID,
		URL:             l.LinkURL,
		Status:          l.LinkStatus,
		AmountPaise:     RupeesToPaise(l.LinkAmount),
		AmountPaidPaise: RupeesToPaise(l.LinkAmountPaid),
	}
}

func (r *rawClient) CreatePaymentLink(ctx context.Context, p CreatePaymentLinkParams) (*PaymentLink, error) {
	customer := map[string]interface{}{"customer_phone": p.CustomerPhone}
	if p.CustomerEmail != "" {
		customer["customer_email"] = p.CustomerEmail
	}
	if p.CustomerName != "" {
		customer["customer_name"] = p.CustomerName
	}
	body := map[string]interface{}{
		"link_id":          p.LinkID,
		"link_amount":      PaiseToRupees(p.AmountPaise),
		"link_currency":    "INR",
		"customer_details": customer,
		// We share the link ourselves; Cashfree must not send its own SMS/email.
		"link_notify": map[string]interface{}{"send_sms": false, "send_email": false},
	}
	if p.Purpose != "" {
		body["link_purpose"] = p.Purpose
	}
	if !p.ExpiryTime.IsZero() {
		body["link_expiry_time"] = p.ExpiryTime.In(IST).Format("2006-01-02T15:04:05-07:00")
	}
	if p.ReturnURL != "" {
		body["link_meta"] = map[string]interface{}{"return_url": p.ReturnURL}
	}

	var out paymentLinkJSON
	if err := r.do(ctx, http.MethodPost, "/links", p.IdempotencyKey, body, &out); err != nil {
		return nil, err
	}
	return out.toLink(), nil
}

func (r *rawClient) FetchPaymentLink(ctx context.Context, linkID string) (*PaymentLink, error) {
	var out paymentLinkJSON
	if err := r.do(ctx, http.MethodGet, "/links/"+url.PathEscape(linkID), "", nil, &out); err != nil {
		return nil, err
	}
	return out.toLink(), nil
}
