package cashfree

import (
	"context"
	"net/http"
	"time"

	cfpg "github.com/cashfree/cashfree-pg/v6"
)

// client is the production Client: SDK for create/fetch/manage, raw HTTP for the controlled flow.
type client struct {
	*rawClient
	sdk        *cfpg.Cashfree
	httpClient *http.Client
}

// New builds a Client for the given credentials.
func New(creds Credentials) Client {
	env := cfpg.PRODUCTION
	if creds.Sandbox {
		env = cfpg.SANDBOX
	}
	analytics := false // do not send SDK error analytics to Cashfree
	id, secret := creds.ClientID, creds.ClientSecret
	return &client{
		rawClient: newRawClient(creds),
		sdk: &cfpg.Cashfree{
			XClientId:             &id,
			XClientSecret:         &secret,
			XEnvironment:          &env,
			XEnableErrorAnalytics: &analytics,
		},
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// NewFactory is the production ClientFactory.
func NewFactory() ClientFactory {
	return New
}

// sdkError converts an SDK failure (GenericOpenAPIError + *http.Response) into an *APIError.
// Transport errors (no response) are returned unchanged.
func sdkError(resp *http.Response, err error) error {
	if err == nil {
		return nil
	}
	if resp == nil {
		return err
	}
	var body []byte
	if gerr, ok := err.(*cfpg.GenericOpenAPIError); ok {
		body = gerr.Body()
	}
	if resp.StatusCode < 300 {
		// Decode failure on a 2xx response: surface the original error.
		return err
	}
	apiErr := newAPIError(resp.StatusCode, resp.Header, body)
	if apiErr.Message == http.StatusText(resp.StatusCode) && err.Error() != "" {
		apiErr.Message = err.Error()
	}
	return apiErr
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func toSubscription(e *cfpg.SubscriptionEntity) *Subscription {
	if e == nil {
		return nil
	}
	s := &Subscription{
		SubscriptionID:   str(e.SubscriptionId),
		CFSubscriptionID: str(e.CfSubscriptionId),
		SessionID:        str(e.SubscriptionSessionId),
		Status:           str(e.SubscriptionStatus),
	}
	if a := e.AuthorisationDetails; a != nil {
		s.AuthStatus = str(a.AuthorizationStatus)
		s.AuthPaymentID = str(a.PaymentId)
		s.AuthReference = str(a.AuthorizationReference)
	}
	return s
}

func (c *client) CreateSubscription(ctx context.Context, p CreateSubscriptionParams) (*Subscription, error) {
	maxAmount := float32(PaiseToRupees(p.MaxAmountPaise))
	authAmount := float32(PaiseToRupees(p.AuthorizationAmountPaise))
	planType := "ON_DEMAND"
	currency := "INR"
	planName := p.PlanName
	refund := p.RefundAuthorizationAmount
	expiry := p.ExpiryTime.In(IST).Format("2006-01-02T15:04:05-07:00")
	name := p.CustomerName

	req := &cfpg.CreateSubscriptionRequest{
		SubscriptionId: p.SubscriptionID,
		CustomerDetails: cfpg.SubscriptionCustomerDetails{
			CustomerName:  &name,
			CustomerEmail: p.CustomerEmail,
			CustomerPhone: p.CustomerPhone,
		},
		PlanDetails: cfpg.CreateSubscriptionRequestPlanDetails{
			PlanName:      &planName,
			PlanType:      &planType,
			PlanCurrency:  &currency,
			PlanMaxAmount: &maxAmount,
		},
		AuthorizationDetails: &cfpg.CreateSubscriptionRequestAuthorizationDetails{
			AuthorizationAmount:       &authAmount,
			AuthorizationAmountRefund: &refund,
			PaymentMethods:            []string{"upi"},
		},
		SubscriptionExpiryTime: &expiry,
	}
	if p.ReturnURL != "" {
		returnURL := p.ReturnURL
		req.SubscriptionMeta = &cfpg.CreateSubscriptionRequestSubscriptionMeta{ReturnUrl: &returnURL}
	}

	var idem *string
	if key := idempotencyHeader(p.IdempotencyKey); key != "" {
		idem = &key
	}
	entity, resp, err := c.sdk.SubsCreateSubscriptionWithContext(ctx, req, nil, idem, c.httpClient)
	if err := sdkError(resp, err); err != nil {
		return nil, err
	}
	return toSubscription(entity), nil
}

func (c *client) FetchSubscription(ctx context.Context, subscriptionID string) (*Subscription, error) {
	entity, resp, err := c.sdk.SubsFetchSubscriptionWithContext(ctx, subscriptionID, nil, nil, c.httpClient)
	if err := sdkError(resp, err); err != nil {
		return nil, err
	}
	return toSubscription(entity), nil
}

func (c *client) ManageSubscription(ctx context.Context, subscriptionID, action string) (*Subscription, error) {
	req := &cfpg.ManageSubscriptionRequest{SubscriptionId: subscriptionID, Action: action}
	entity, resp, err := c.sdk.SubsManageSubscriptionWithContext(ctx, subscriptionID, req, nil, nil, c.httpClient)
	if err := sdkError(resp, err); err != nil {
		return nil, err
	}
	return toSubscription(entity), nil
}

func (c *client) FetchPayment(ctx context.Context, subscriptionID, paymentID string) (*Payment, error) {
	entity, resp, err := c.sdk.SubsFetchSubscriptionPaymentWithContext(ctx, subscriptionID, paymentID, nil, nil, c.httpClient)
	if err := sdkError(resp, err); err != nil {
		return nil, err
	}
	p := &Payment{
		PaymentID:   str(entity.PaymentId),
		CFPaymentID: str(entity.CfPaymentId),
		Type:        str(entity.PaymentType),
		Status:      str(entity.PaymentStatus),
	}
	if entity.FailureDetails != nil {
		p.FailureReason = str(entity.FailureDetails.FailureReason)
	}
	if entity.RetryAttempts != nil {
		p.RetryAttempts = int(*entity.RetryAttempts)
	}
	return p, nil
}

// compile-time check
var _ Client = (*client)(nil)
