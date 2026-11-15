package cashfree

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"
)

// TestSandboxSmoke talks to the real Cashfree sandbox. It is opt-in:
//
//	CASHFREE_SANDBOX_SMOKE=1 CASHFREE_APP_ID=... CASHFREE_SECRET_KEY=... go test ./pkg/cashfree/ -run SandboxSmoke -v
//
// It creates (and cancels) one throwaway ON_DEMAND UPI subscription. The mandate cannot be authorized without a
// customer, so notify/execute are only probed for their error shape.
func TestSandboxSmoke(t *testing.T) {
	if os.Getenv("CASHFREE_SANDBOX_SMOKE") != "1" {
		t.Skip("set CASHFREE_SANDBOX_SMOKE=1 (plus CASHFREE_APP_ID / CASHFREE_SECRET_KEY) to run against the sandbox")
	}
	id, secret := os.Getenv("CASHFREE_APP_ID"), os.Getenv("CASHFREE_SECRET_KEY")
	if id == "" || secret == "" {
		t.Fatal("CASHFREE_APP_ID and CASHFREE_SECRET_KEY are required")
	}

	c := New(Credentials{ClientID: id, ClientSecret: secret, Sandbox: true})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	subID := fmt.Sprintf("smoke_%d", time.Now().UnixNano())
	sub, err := c.CreateSubscription(ctx, CreateSubscriptionParams{
		SubscriptionID:           subID,
		PlanName:                 "smoke mandate",
		MaxAmountPaise:           9900,
		AuthorizationAmountPaise: 100,
		CustomerName:             "Smoke Test",
		CustomerEmail:            "smoke@example.com",
		CustomerPhone:            "9999999999",
		ExpiryTime:               time.Now().AddDate(1, 0, 0),
		IdempotencyKey:           subID,
	})
	if err != nil {
		t.Fatalf("CreateSubscription: %v", err)
	}
	t.Logf("created: status=%s cf_id=%s session_id_len=%d", sub.Status, sub.CFSubscriptionID, len(sub.SessionID))
	if sub.SessionID == "" || sub.CFSubscriptionID == "" {
		t.Errorf("expected session id and cf id, got %+v", sub)
	}

	got, err := c.FetchSubscription(ctx, subID)
	if err != nil {
		t.Fatalf("FetchSubscription: %v", err)
	}
	t.Logf("fetched: status=%s auth_status=%s", got.Status, got.AuthStatus)

	// Probes: record exactly how Cashfree reports errors so the error handling matches reality.
	payID := "smokepay_" + subID
	_, err = c.NotifyMandate(ctx, NotifyParams{
		SubscriptionID: subID, PaymentID: payID, NotificationID: payID + "_n1",
		AmountPaise: 9900, ScheduleDate: time.Now().AddDate(0, 0, 2), Remarks: "smoke",
	})
	logAPIError(t, "NotifyMandate (unauthorized mandate)", err)

	_, err = c.ExecuteMandate(ctx, ExecuteParams{PaymentID: payID, ExecutionID: payID + "_e1"})
	logAPIError(t, "ExecuteMandate (no notification)", err)

	_, err = c.FetchNotification(ctx, payID, payID+"_n1")
	logAPIError(t, "FetchNotification (unknown)", err)

	_, err = c.FetchExecution(ctx, payID, payID+"_e1")
	logAPIError(t, "FetchExecution (unknown)", err)

	_, err = c.FetchPayment(ctx, subID, payID)
	logAPIError(t, "FetchPayment (unknown)", err)

	if _, err := c.ManageSubscription(ctx, subID, "CANCEL"); err != nil {
		t.Fatalf("ManageSubscription CANCEL: %v", err)
	}
	after, err := c.FetchSubscription(ctx, subID)
	if err != nil {
		t.Fatalf("FetchSubscription after cancel: %v", err)
	}
	t.Logf("after cancel: status=%s", after.Status)
}

// TestSandboxOneTimeSmoke checks one-time orders and payment links against the real sandbox (opt-in, see above).
func TestSandboxOneTimeSmoke(t *testing.T) {
	if os.Getenv("CASHFREE_SANDBOX_SMOKE") != "1" {
		t.Skip("set CASHFREE_SANDBOX_SMOKE=1 (plus CASHFREE_APP_ID / CASHFREE_SECRET_KEY) to run against the sandbox")
	}
	c := New(Credentials{ClientID: os.Getenv("CASHFREE_APP_ID"), ClientSecret: os.Getenv("CASHFREE_SECRET_KEY"), Sandbox: true})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	orderID := "smoke_order_" + suffix
	order, err := c.CreateOrder(ctx, CreateOrderParams{
		OrderID: orderID, AmountPaise: 9950, CustomerID: "smoke_customer", CustomerName: "Smoke Test",
		CustomerEmail: "smoke@example.com", CustomerPhone: "9999999999",
		ExpiryTime: time.Now().Add(time.Hour), Note: "smoke", IdempotencyKey: orderID,
	})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	t.Logf("order: id=%s status=%s amount_paise=%d session_len=%d", order.OrderID, order.Status, order.AmountPaise, len(order.SessionID))
	if order.SessionID == "" || order.OrderID != orderID || order.AmountPaise != 9950 || order.Status != "ACTIVE" {
		t.Errorf("unexpected order: %+v", order)
	}
	fetched, err := c.FetchOrder(ctx, orderID)
	if err != nil {
		t.Fatalf("FetchOrder: %v", err)
	}
	t.Logf("fetched order: status=%s amount_paise=%d", fetched.Status, fetched.AmountPaise)
	_, err = c.FetchOrder(ctx, "smoke_missing_"+suffix)
	logAPIError(t, "FetchOrder (unknown)", err)

	linkID := "smoke_link_" + suffix
	link, err := c.CreatePaymentLink(ctx, CreatePaymentLinkParams{
		LinkID: linkID, AmountPaise: 9950, Purpose: "smoke", CustomerName: "Smoke Test",
		CustomerEmail: "smoke@example.com", CustomerPhone: "9999999999",
		ExpiryTime: time.Now().Add(time.Hour), IdempotencyKey: linkID,
	})
	if err != nil {
		t.Fatalf("CreatePaymentLink: %v", err)
	}
	t.Logf("link: id=%s status=%s amount_paise=%d url_len=%d", link.LinkID, link.Status, link.AmountPaise, len(link.URL))
	if link.URL == "" || link.LinkID != linkID || link.AmountPaise != 9950 || link.Status != "ACTIVE" {
		t.Errorf("unexpected link: %+v", link)
	}
	fl, err := c.FetchPaymentLink(ctx, linkID)
	if err != nil {
		t.Fatalf("FetchPaymentLink: %v", err)
	}
	t.Logf("fetched link: status=%s paid_paise=%d", fl.Status, fl.AmountPaidPaise)
	_, err = c.FetchPaymentLink(ctx, "smoke_missing_"+suffix)
	logAPIError(t, "FetchPaymentLink (unknown)", err)
}

func logAPIError(t *testing.T, what string, err error) {
	t.Helper()
	if err == nil {
		t.Logf("%s: no error", what)
		return
	}
	if apiErr, ok := AsAPIError(err); ok {
		t.Logf("%s: http=%d code=%q type=%q message=%q retryable=%v in_progress=%v",
			what, apiErr.HTTPStatus, apiErr.Code, apiErr.Type, apiErr.Message, apiErr.IsRetryable(), apiErr.IsInProgress())
		return
	}
	t.Logf("%s: non-API error: %v", what, err)
}
