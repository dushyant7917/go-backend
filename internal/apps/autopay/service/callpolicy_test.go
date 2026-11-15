package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"go-backend/internal/apps/autopay/gateway"
	"go-backend/internal/apps/autopay/ratelimit"

	"github.com/google/uuid"
)

type fakeLimiter struct {
	takes   int
	classes []ratelimit.Class
	err     error
}

func (f *fakeLimiter) Wait(_ context.Context, _ uuid.UUID, _ int, class ratelimit.Class) error {
	f.takes++
	f.classes = append(f.classes, class)
	return f.err
}

type sleepRecorder struct{ waits []time.Duration }

func (s *sleepRecorder) sleep(_ context.Context, d time.Duration) error {
	s.waits = append(s.waits, d)
	return nil
}

func policy(l *fakeLimiter, s *sleepRecorder, class ratelimit.Class) CallPolicy {
	return CallPolicy{Limiter: l, ConfigID: uuid.New(), RequestsPerMinute: 60, Class: class, sleep: s.sleep}
}

func TestCallSucceedsFirstTryWithOneToken(t *testing.T) {
	l, s := &fakeLimiter{}, &sleepRecorder{}
	calls := 0
	got, err := Call(context.Background(), policy(l, s, ratelimit.Background), func(ctx context.Context) (string, error) {
		calls++
		return "ok", nil
	})
	if err != nil || got != "ok" || calls != 1 || l.takes != 1 || len(s.waits) != 0 {
		t.Fatalf("got=%q err=%v calls=%d tokens=%d waits=%v", got, err, calls, l.takes, s.waits)
	}
	if l.classes[0] != ratelimit.Background {
		t.Fatal("the caller's priority class must reach the limiter")
	}
}

func TestEveryTryTakesAToken(t *testing.T) {
	l, s := &fakeLimiter{}, &sleepRecorder{}
	calls := 0
	got, err := Call(context.Background(), policy(l, s, ratelimit.Interactive), func(ctx context.Context) (int, error) {
		calls++
		if calls < 3 {
			return 0, gateway.NewError(gateway.ErrorKindTransient, "503", "unavailable", nil)
		}
		return 42, nil
	})
	if err != nil || got != 42 {
		t.Fatalf("got=%d err=%v", got, err)
	}
	if calls != 3 || l.takes != 3 {
		t.Fatalf("3 tries must take 3 tokens, got calls=%d tokens=%d", calls, l.takes)
	}
	if len(s.waits) != 2 || s.waits[0] < time.Second || s.waits[0] > 1250*time.Millisecond || s.waits[1] < 2*time.Second || s.waits[1] > 2500*time.Millisecond {
		t.Fatalf("backoff must be ~1s then ~2s with jitter, got %v", s.waits)
	}
}

func TestExhaustedTriesReturnTheLastErrorWithItsKind(t *testing.T) {
	l, s := &fakeLimiter{}, &sleepRecorder{}
	calls := 0
	p := policy(l, s, ratelimit.Background)
	p.RetryUnknownOutcome = true // a gateway with idempotency keys
	_, err := Call(context.Background(), p, func(ctx context.Context) (string, error) {
		calls++
		return "", gateway.NewError(gateway.ErrorKindOutcomeUnknown, "timeout", "no response", errors.New("i/o timeout"))
	})
	if calls != maxCallTries {
		t.Fatalf("calls = %d, want %d", calls, maxCallTries)
	}
	if !gateway.IsKind(err, gateway.ErrorKindOutcomeUnknown) {
		t.Fatalf("the caller needs the kind to decide: %v", err)
	}
}

func TestNonRetryableErrorsAreReturnedImmediately(t *testing.T) {
	for _, kind := range []gateway.ErrorKind{
		gateway.ErrorKindRejectedPermanently, gateway.ErrorKindRejectedTemporarily,
		gateway.ErrorKindAlreadyProcessed, gateway.ErrorKindMandateInvalid,
	} {
		l, s := &fakeLimiter{}, &sleepRecorder{}
		calls := 0
		_, err := Call(context.Background(), policy(l, s, ratelimit.Background), func(ctx context.Context) (string, error) {
			calls++
			return "", gateway.NewError(kind, "x", "", nil)
		})
		if calls != 1 || l.takes != 1 || len(s.waits) != 0 || !gateway.IsKind(err, kind) {
			t.Errorf("%s: calls=%d tokens=%d waits=%v err=%v", kind, calls, l.takes, s.waits, err)
		}
	}
	// a plain (non-gateway) error is not retried either
	l, s := &fakeLimiter{}, &sleepRecorder{}
	calls := 0
	_, err := Call(context.Background(), policy(l, s, ratelimit.Background), func(ctx context.Context) (string, error) {
		calls++
		return "", errors.New("bug")
	})
	if calls != 1 || err == nil {
		t.Fatalf("plain error: calls=%d err=%v", calls, err)
	}
}

func TestLimiterFailureFailsClosedWithoutCallingTheGateway(t *testing.T) {
	l, s := &fakeLimiter{err: errors.New("db down")}, &sleepRecorder{}
	calls := 0
	_, err := Call(context.Background(), policy(l, s, ratelimit.Background), func(ctx context.Context) (string, error) {
		calls++
		return "", nil
	})
	if calls != 0 {
		t.Fatal("the gateway must not be called without a token")
	}
	if !gateway.IsKind(err, gateway.ErrorKindTransient) {
		t.Fatalf("a limiter failure must surface as transient (the request was not sent), got %v", err)
	}
}

func TestCancelledContextStopsRetrying(t *testing.T) {
	l := &fakeLimiter{}
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	p := CallPolicy{Limiter: l, ConfigID: uuid.New(), RequestsPerMinute: 60, Class: ratelimit.Background,
		sleep: func(ctx context.Context, d time.Duration) error { cancel(); return ctx.Err() }}
	_, err := Call(ctx, p, func(ctx context.Context) (string, error) {
		calls++
		return "", gateway.NewError(gateway.ErrorKindTransient, "503", "", nil)
	})
	if calls != 1 || !errors.Is(err, context.Canceled) {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

// penalizingLimiter is a fakeLimiter that also records the pauses the gateway asked for.
type penalizingLimiter struct {
	fakeLimiter
	pauses []time.Duration
}

func (p *penalizingLimiter) Penalize(_ context.Context, _ uuid.UUID, d time.Duration) error {
	p.pauses = append(p.pauses, d)
	return nil
}

func TestRateLimitedAnswerPausesTheWholeAccountAndIsRetried(t *testing.T) {
	l, s := &penalizingLimiter{}, &sleepRecorder{}
	calls := 0
	got, err := Call(context.Background(), CallPolicy{Limiter: l, ConfigID: uuid.New(), RequestsPerMinute: 60, Class: ratelimit.Background, sleep: s.sleep},
		func(ctx context.Context) (string, error) {
			calls++
			if calls == 1 {
				return "", gateway.NewRateLimitedError("429", "too many requests", 7*time.Second, nil)
			}
			return "ok", nil
		})
	if err != nil || got != "ok" || calls != 2 {
		t.Fatalf("a 429 is transient and must be retried: got=%q err=%v calls=%d", got, err, calls)
	}
	if len(l.pauses) != 1 || l.pauses[0] != 7*time.Second {
		t.Fatalf("the pause the gateway asked for must reach the shared limiter, got %v", l.pauses)
	}
	if l.takes != 2 {
		t.Fatalf("both tries still take a token, got %d", l.takes)
	}
}

func TestRateLimitedWithoutRetryAfterUsesTheDefaultPause(t *testing.T) {
	l, s := &penalizingLimiter{}, &sleepRecorder{}
	_, err := Call(context.Background(), CallPolicy{Limiter: l, ConfigID: uuid.New(), RequestsPerMinute: 60, Class: ratelimit.Interactive, sleep: s.sleep},
		func(ctx context.Context) (string, error) {
			return "", gateway.NewRateLimitedError("429", "", 0, nil)
		})
	if !gateway.IsKind(err, gateway.ErrorKindTransient) {
		t.Fatalf("an exhausted 429 stays a transient error (the attempt is released, not failed), got %v", err)
	}
	if len(l.pauses) != maxCallTries || l.pauses[0] != defaultRateLimitPause {
		t.Fatalf("every 429 pauses the account, by the default when the gateway says nothing: %v", l.pauses)
	}
}

func TestOtherTransientErrorsDoNotPauseTheAccount(t *testing.T) {
	l, s := &penalizingLimiter{}, &sleepRecorder{}
	_, _ = Call(context.Background(), CallPolicy{Limiter: l, ConfigID: uuid.New(), RequestsPerMinute: 60, sleep: s.sleep},
		func(ctx context.Context) (string, error) {
			return "", gateway.NewError(gateway.ErrorKindTransient, "503", "down", nil)
		})
	if len(l.pauses) != 0 {
		t.Fatalf("a 503 is not a rate limit: %v", l.pauses)
	}
}

func TestUnknownOutcomeIsNotRetriedWithoutIdempotencyKeys(t *testing.T) {
	l, s := &fakeLimiter{}, &sleepRecorder{}
	calls := 0
	_, err := Call(context.Background(), policy(l, s, ratelimit.Background), func(ctx context.Context) (string, error) {
		calls++
		return "", gateway.NewError(gateway.ErrorKindOutcomeUnknown, "timeout", "no response", errors.New("i/o timeout"))
	})
	if calls != 1 || l.takes != 1 || len(s.waits) != 0 {
		t.Fatalf("a request that may have taken effect must not be repeated blindly: calls=%d tokens=%d waits=%v", calls, l.takes, s.waits)
	}
	if !gateway.IsKind(err, gateway.ErrorKindOutcomeUnknown) {
		t.Fatalf("the caller verifies with the gateway, so it needs the kind: %v", err)
	}
	// transient errors (nothing was processed) are still retried
	calls = 0
	_, _ = Call(context.Background(), policy(&fakeLimiter{}, &sleepRecorder{}, ratelimit.Background), func(ctx context.Context) (string, error) {
		calls++
		return "", gateway.NewError(gateway.ErrorKindTransient, "503", "down", nil)
	})
	if calls != maxCallTries {
		t.Fatalf("transient errors are retried regardless: %d", calls)
	}
}

// blockingLimiter never grants a token: it waits until its context is done, like a bucket under a 429 pause.
type blockingLimiter struct{}

func (blockingLimiter) Wait(ctx context.Context, _ uuid.UUID, _ int, _ ratelimit.Class) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestInteractiveCallFailsFastWhenNoTokenComesInTime(t *testing.T) {
	old := interactiveMaxTokenWait
	interactiveMaxTokenWait = 50 * time.Millisecond
	defer func() { interactiveMaxTokenWait = old }()

	calls := 0
	started := time.Now()
	_, err := Call(context.Background(), CallPolicy{Limiter: blockingLimiter{}, ConfigID: uuid.New(), RequestsPerMinute: 60, Class: ratelimit.Interactive},
		func(ctx context.Context) (string, error) { calls++; return "ok", nil })
	if calls != 0 || !gateway.IsKind(err, gateway.ErrorKindTransient) {
		t.Fatalf("no token in time: the gateway must not be called and the error is transient, calls=%d err=%v", calls, err)
	}
	if time.Since(started) > 2*time.Second {
		t.Fatalf("a web request must not hang while the account is paused: %v", time.Since(started))
	}
}

func TestBackgroundCallWaitsForTheCallersOwnContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	_, err := Call(ctx, CallPolicy{Limiter: blockingLimiter{}, ConfigID: uuid.New(), RequestsPerMinute: 60, Class: ratelimit.Background},
		func(ctx context.Context) (string, error) { return "ok", nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a cron/webhook call is bounded by its own context, got %v", err)
	}
}
