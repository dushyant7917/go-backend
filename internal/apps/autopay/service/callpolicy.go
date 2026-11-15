package service

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"go-backend/internal/apps/autopay/gateway"
	"go-backend/internal/apps/autopay/ratelimit"

	"github.com/google/uuid"
)

// maxCallTries is the number of tries per gateway call: the first plus 2 retries. These absorb transient network and
// gateway-side errors only; they are unrelated to the per-attempt limits in schedule.go.
const maxCallTries = 3

// callBackoffs are the waits before the 2nd and 3rd try (plus jitter).
var callBackoffs = []time.Duration{time.Second, 2 * time.Second}

// TokenTaker hands out rate-limit tokens. *ratelimit.Limiter implements it.
type TokenTaker interface {
	Wait(ctx context.Context, configID uuid.UUID, requestsPerMinute int, class ratelimit.Class) error
}

// Penalizer is an optional TokenTaker capability: pause every caller of a config for a while after the gateway
// answered HTTP 429. *ratelimit.Limiter implements it.
type Penalizer interface {
	Penalize(ctx context.Context, configID uuid.UUID, d time.Duration) error
}

// defaultRateLimitPause is the pause when a gateway answers 429 without saying how long to wait: long enough for a
// burst to drain, short enough not to hold up the night's run.
const defaultRateLimitPause = 10 * time.Second

// CallPolicy wraps every call to a gateway: it takes a rate-limit token before EVERY try (every call counts toward
// the merchant account's RPM, retries and fetches included) and retries transient and outcome-unknown errors.
type CallPolicy struct {
	Limiter           TokenTaker
	ConfigID          uuid.UUID
	RequestsPerMinute int
	Class             ratelimit.Class
	// RetryUnknownOutcome allows repeating a call whose outcome is unknown (timeout or reset after the request was
	// sent). Only safe when the gateway deduplicates by idempotency key (gateway.Capabilities.SupportsIdempotencyKeys);
	// otherwise the caller must verify with the gateway instead of repeating a call that may have taken effect.
	RetryUnknownOutcome bool

	sleep func(ctx context.Context, d time.Duration) error // replaced in tests
}

// Call runs call up to maxCallTries times under the policy and returns the last result/error.
//
// Only errors where repeating the SAME operation is safe and useful are retried (gateway.IsRetryable: transient,
// and outcome unknown when the gateway deduplicates by idempotency key, see RetryUnknownOutcome). The closure must
// therefore reuse the same ids and idempotency keys on every try (Razorpay: the same order id; Cashfree: the same
// execution id; PhonePe: the same order), which is why it takes no per-try arguments. Business rejections are returned immediately. The caller decides what an exhausted error means (see the
// debit outcome table): outcome unknown is verified with the gateway, transient releases the attempt without counting it.
//
// The limiter fails closed: if it cannot grant a token (database problem) the call is NOT made and a transient error
// is returned, which the normal policy handles. A cancelled context is returned as is.
func Call[T any](ctx context.Context, p CallPolicy, call func(ctx context.Context) (T, error)) (T, error) {
	var zero T
	sleep := p.sleep
	if sleep == nil {
		sleep = sleepWithContext
	}

	var lastErr error
	for try := 0; try < maxCallTries; try++ {
		if err := p.takeToken(ctx); err != nil {
			if ctx.Err() != nil {
				return zero, ctx.Err()
			}
			return zero, gateway.NewError(gateway.ErrorKindTransient, "rate_limiter_unavailable",
				"could not take a rate-limit token; the gateway was not called", err)
		}

		result, err := call(ctx)
		if err == nil {
			return result, nil
		}
		lastErr = err
		if wait, limited := gateway.RateLimitOf(err); limited {
			p.pauseAccount(ctx, wait)
		}
		if errors.Is(err, context.Canceled) || (errors.Is(err, context.DeadlineExceeded) && ctx.Err() != nil) {
			return zero, err
		}
		if !gateway.IsRetryable(err) || try == maxCallTries-1 || (gateway.IsKind(err, gateway.ErrorKindOutcomeUnknown) && !p.RetryUnknownOutcome) {
			return zero, err
		}
		if err := sleep(ctx, withJitter(callBackoffs[try])); err != nil {
			return zero, err
		}
	}
	return zero, lastErr
}

// interactiveMaxTokenWait bounds how long a web request waits for a rate-limit token (a gateway 429 pauses the whole
// account for up to ratelimit.MaxPenalty). A request that cannot get one in time fails fast as transient, so the
// client can retry, instead of hanging; crons and webhook processing wait as long as their own context allows.
var interactiveMaxTokenWait = 15 * time.Second

// takeToken waits for a rate-limit token under the policy's class.
func (p CallPolicy) takeToken(ctx context.Context) error {
	if p.Class == ratelimit.Interactive {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, interactiveMaxTokenWait)
		defer cancel()
	}
	return p.Limiter.Wait(ctx, p.ConfigID, p.RequestsPerMinute, p.Class)
}

// pauseAccount applies a gateway's HTTP 429 to the shared limiter, so the retry (and every other caller of the
// merchant account, in this process or another) waits it out instead of hammering a gateway that said stop.
func (p CallPolicy) pauseAccount(ctx context.Context, wait time.Duration) {
	pen, ok := p.Limiter.(Penalizer)
	if !ok {
		return
	}
	if wait <= 0 {
		wait = defaultRateLimitPause
	}
	if err := pen.Penalize(ctx, p.ConfigID, wait); err != nil {
		fmt.Printf("[autopay] cannot record the gateway's rate limit for config %s: %v\n", p.ConfigID, err)
	}
}

// withJitter adds up to 25% so that several callers do not retry in lockstep.
func withJitter(d time.Duration) time.Duration {
	return d + time.Duration(rand.Int63n(int64(d)/4+1))
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
