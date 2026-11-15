package gateway

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestErrorKindHelpers(t *testing.T) {
	base := NewError(ErrorKindTransient, "503", "service unavailable", errors.New("boom"))
	wrapped := fmt.Errorf("charge: %w", base)

	if k, ok := KindOf(wrapped); !ok || k != ErrorKindTransient {
		t.Fatalf("KindOf(wrapped) = %v, %v", k, ok)
	}
	if !IsKind(wrapped, ErrorKindTransient) || IsKind(wrapped, ErrorKindRejectedPermanently) {
		t.Fatal("IsKind mismatch")
	}
	if _, ok := KindOf(errors.New("plain")); ok {
		t.Fatal("plain error must have no kind")
	}
	if !errors.Is(wrapped, base.Cause) {
		t.Fatal("Unwrap must expose the cause")
	}
}

func TestIsRetryableOnlyForTransientAndUnknown(t *testing.T) {
	cases := map[ErrorKind]bool{
		ErrorKindTransient:           true,
		ErrorKindOutcomeUnknown:      true,
		ErrorKindAlreadyProcessed:    false,
		ErrorKindRejectedPermanently: false,
		ErrorKindRejectedTemporarily: false,
		ErrorKindMandateInvalid:      false,
	}
	for kind, want := range cases {
		if got := IsRetryable(NewError(kind, "", "", nil)); got != want {
			t.Errorf("IsRetryable(%s) = %v, want %v", kind, got, want)
		}
	}
	if IsRetryable(errors.New("plain")) {
		t.Error("a non-gateway error must not be retryable")
	}
}

func TestConfigEnvironmentAndLimit(t *testing.T) {
	if (Config{Environment: "prod"}).IsSandbox() {
		t.Error("prod must not be sandbox")
	}
	if !(Config{Environment: "local"}).IsSandbox() || !(Config{Environment: ""}).IsSandbox() {
		t.Error("anything but prod is sandbox")
	}
	if got := (Config{}).EffectiveRequestsPerMinuteLimit(60); got != 60 {
		t.Errorf("fallback = %d", got)
	}
	if got := (Config{RequestsPerMinuteLimit: 100}).EffectiveRequestsPerMinuteLimit(60); got != 100 {
		t.Errorf("override = %d", got)
	}
}

func TestCapabilitiesOneTimeKinds(t *testing.T) {
	c := Capabilities{OneTimePaymentKinds: []string{OneTimePaymentKindOrder}}
	if !c.SupportsOneTimePaymentKind(OneTimePaymentKindOrder) || c.SupportsOneTimePaymentKind(OneTimePaymentKindPaymentLink) {
		t.Fatal("kind lookup wrong")
	}
}

func TestRegistry(t *testing.T) {
	r := NewRegistry()
	called := 0
	r.Register(NameCashfree, func(cfg Config) (Gateway, error) { called++; return nil, nil })
	r.Register(NameRazorpay, func(cfg Config) (Gateway, error) { return nil, errors.New("bad credentials") })

	if !r.Has(NameCashfree) || r.Has(NamePhonePe) {
		t.Fatal("Has mismatch")
	}
	if names := r.Names(); len(names) != 2 || names[0] != NameCashfree || names[1] != NameRazorpay {
		t.Fatalf("Names = %v", names)
	}
	if _, err := r.New(Config{Gateway: NameCashfree}); err != nil || called != 1 {
		t.Fatalf("New(cashfree): err=%v called=%d", err, called)
	}
	if _, err := r.New(Config{Gateway: NameRazorpay}); err == nil {
		t.Fatal("factory error must propagate")
	}
	if _, err := r.New(Config{Gateway: NamePhonePe}); !errors.Is(err, ErrUnknownGateway) {
		t.Fatalf("unknown gateway err = %v", err)
	}

	defer func() {
		if recover() == nil {
			t.Fatal("registering twice must panic")
		}
	}()
	r.Register(NameCashfree, func(cfg Config) (Gateway, error) { return nil, nil })
}

// Compile-time guard that http.Header is what VerifyAndParseWebhook takes.
var _ = func(g Gateway) ([]Event, error) { return g.VerifyAndParseWebhook(http.Header{}, nil) }
