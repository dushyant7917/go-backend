package cashfree

import (
	"context"

	cfpg "github.com/cashfree/cashfree-pg/v6"
)

func f32Paise(v *float32) int64 {
	if v == nil {
		return 0
	}
	return RupeesToPaise(float64(*v))
}

func optStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func toOrder(e *cfpg.OrderEntity) *Order {
	if e == nil {
		return nil
	}
	return &Order{
		OrderID:     str(e.OrderId),
		CFOrderID:   str(e.CfOrderId),
		SessionID:   str(e.PaymentSessionId),
		Status:      str(e.OrderStatus),
		AmountPaise: f32Paise(e.OrderAmount),
	}
}

func (c *client) CreateOrder(ctx context.Context, p CreateOrderParams) (*Order, error) {
	orderID := p.OrderID
	req := &cfpg.CreateOrderRequest{
		OrderId:       &orderID,
		OrderAmount:   PaiseToRupees(p.AmountPaise),
		OrderCurrency: "INR",
		CustomerDetails: &cfpg.CustomerDetails{
			CustomerId:    p.CustomerID,
			CustomerPhone: p.CustomerPhone,
			CustomerEmail: optStr(p.CustomerEmail),
			CustomerName:  optStr(p.CustomerName),
		},
		OrderNote: optStr(p.Note),
	}
	if !p.ExpiryTime.IsZero() {
		expiry := p.ExpiryTime.In(IST).Format("2006-01-02T15:04:05-07:00")
		req.OrderExpiryTime = &expiry
	}
	if p.ReturnURL != "" {
		req.OrderMeta = &cfpg.OrderMeta{ReturnUrl: &p.ReturnURL}
	}

	var idem *string
	if key := idempotencyHeader(p.IdempotencyKey); key != "" {
		idem = &key
	}
	entity, resp, err := c.sdk.PGCreateOrder(req, nil, idem, c.httpClient)
	if err := sdkError(resp, err); err != nil {
		return nil, err
	}
	return toOrder(entity), nil
}

func (c *client) FetchOrder(ctx context.Context, orderID string) (*Order, error) {
	entity, resp, err := c.sdk.PGFetchOrder(orderID, nil, nil, c.httpClient)
	if err := sdkError(resp, err); err != nil {
		return nil, err
	}
	return toOrder(entity), nil
}
