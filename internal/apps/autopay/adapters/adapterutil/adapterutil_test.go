package adapterutil

import (
	"context"
	"errors"
	"testing"

	"go-backend/internal/apps/autopay/gateway"
)

func TestTransportErrorKinds(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		mutating bool
		want     gateway.ErrorKind
	}{
		{"timeout on a write: the request may have been processed", context.DeadlineExceeded, true, gateway.ErrorKindOutcomeUnknown},
		{"connection reset on a write", errors.New("read: connection reset by peer"), true, gateway.ErrorKindOutcomeUnknown},
		{"refused connection on a write: never reached the gateway", errors.New("dial tcp: connection refused"), true, gateway.ErrorKindTransient},
		{"timeout on a read is just transient", context.DeadlineExceeded, false, gateway.ErrorKindTransient},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if kind, _ := gateway.KindOf(TransportError(c.err, c.mutating)); kind != c.want {
				t.Fatalf("got %s want %s", kind, c.want)
			}
		})
	}
}

func TestFailureCodeAndRejected(t *testing.T) {
	if got := FailureCode("", "reason", "fallback"); got != "reason" {
		t.Fatalf("got %q", got)
	}
	if got := FailureCode("", "", "fallback"); got != "fallback" {
		t.Fatalf("got %q", got)
	}
	if got := FailureCode(string(make([]byte, 300)), "", ""); len(got) != 255 {
		t.Fatalf("must fit failure_code: %d", len(got))
	}
	if !gateway.IsKind(Rejected("c", "m"), gateway.ErrorKindRejectedPermanently) {
		t.Fatal("Rejected is a permanent rejection")
	}
}

func TestCancelAlreadyDone(t *testing.T) {
	rejection := gateway.NewError(gateway.ErrorKindRejectedPermanently, "x", "no", nil)
	state := func(s gateway.MandateState, err error) func() (gateway.MandateState, error) {
		return func() (gateway.MandateState, error) { return s, err }
	}
	if err := CancelAlreadyDone(rejection, errors.New("raw"), state(gateway.MandateCancelled, nil)); !gateway.IsKind(err, gateway.ErrorKindAlreadyProcessed) {
		t.Fatalf("cancelled: %v", err)
	}
	if err := CancelAlreadyDone(rejection, nil, state(gateway.MandateExpired, nil)); !gateway.IsKind(err, gateway.ErrorKindAlreadyProcessed) {
		t.Fatalf("expired: %v", err)
	}
	if err := CancelAlreadyDone(rejection, nil, state(gateway.MandateAwaitingAuthorization, nil)); !gateway.IsKind(err, gateway.ErrorKindAlreadyProcessed) {
		t.Fatalf("never authorized: nothing live to cancel, got %v", err)
	}
	if err := CancelAlreadyDone(rejection, nil, state(gateway.MandateActive, nil)); err != rejection {
		t.Fatalf("active: the rejection stands, got %v", err)
	}
	if err := CancelAlreadyDone(rejection, nil, state("", errors.New("down"))); err != rejection {
		t.Fatalf("lookup failed: the rejection stands, got %v", err)
	}
	outage := gateway.NewError(gateway.ErrorKindTransient, "503", "down", nil)
	called := false
	if err := CancelAlreadyDone(outage, nil, func() (gateway.MandateState, error) { called = true; return gateway.MandateCancelled, nil }); err != outage || called {
		t.Fatalf("an outage must not trigger a lookup: err=%v called=%v", err, called)
	}
}
