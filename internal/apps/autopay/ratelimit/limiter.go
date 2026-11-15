// Package ratelimit is the shared, DB-backed token bucket that keeps every call to a gateway under that merchant
// account's requests-per-minute limit. The web server and every cron take tokens from the same bucket (one row of
// payment_gateway_rate_limits per payment_gateway_configs row), so nothing has to be split statically between
// processes. Every gateway call takes a token first: creates, notifications, debits, status fetches and every
// retry try alike.
//
// Algorithm: token bucket with burst b and refill r tokens/second never lets more than 60*r + b calls through in
// any 60-second window, so it is safe whether the gateway enforces fixed or sliding windows. ParamsFor chooses
// r = (RPM - buffer - b) / 60 so that worst case never exceeds RPM - buffer.
package ratelimit

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	// BufferRequestsPerMinute is the safety margin kept below the gateway's documented limit.
	BufferRequestsPerMinute = 5
	// DefaultBurst is the bucket size: how many calls may go out back to back.
	DefaultBurst = 5
	// BackgroundReserve is how many tokens background work (crons, webhook processing, fetches) leaves untouched, so
	// an interactive request from the web server never queues behind a saturating cron.
	BackgroundReserve = 2

	minPoll = 20 * time.Millisecond
	maxPoll = 5 * time.Second
	// MaxPenalty caps a gateway-requested pause, so one odd Retry-After cannot stall payments for an hour.
	MaxPenalty = 2 * time.Minute
	// logWaitAfter: a call that has waited this long for a token is logged once.
	logWaitAfter = 30 * time.Second
)

// Class is the priority of a caller.
type Class int

const (
	// Interactive is the web server's request path (create, verify, cancel). It needs a single token.
	Interactive Class = iota
	// Background is crons, webhook processing and fetches. It leaves BackgroundReserve tokens for Interactive.
	Background
)

// Params are the bucket parameters derived from a gateway's requests-per-minute limit.
type Params struct {
	RefillPerSecond float64
	Burst           float64
}

// ParamsFor returns the bucket parameters for a merchant account's requests-per-minute limit.
func ParamsFor(requestsPerMinute int) Params {
	budget := requestsPerMinute - BufferRequestsPerMinute
	if budget < 2 {
		budget = 2
	}
	burst := DefaultBurst
	if half := budget / 2; half < burst {
		burst = half
	}
	return Params{
		RefillPerSecond: float64(budget-burst) / 60,
		Burst:           float64(burst),
	}
}

// reserve returns how many tokens a caller of the class must leave behind. It never exceeds burst-1, otherwise a
// small bucket would starve background callers forever.
func (p Params) reserve(class Class) float64 {
	if class != Background {
		return 0
	}
	return math.Min(BackgroundReserve, p.Burst-1)
}

// Limiter hands out tokens from the shared per-config buckets.
type Limiter struct {
	db      *gorm.DB
	ensured sync.Map // uuid.UUID -> struct{}: buckets known to exist
	sleep   func(ctx context.Context, d time.Duration) error
}

// New returns a Limiter on db.
func New(db *gorm.DB) *Limiter {
	return &Limiter{db: db, sleep: sleepContext}
}

// Wait blocks until a token is granted for the config (one merchant account) or ctx is done. It fails closed: if the
// limiter's own database statement fails, the error is returned and the gateway call must not be made.
func (l *Limiter) Wait(ctx context.Context, configID uuid.UUID, requestsPerMinute int, class Class) error {
	return l.wait(ctx, configID, ParamsFor(requestsPerMinute), class)
}

func (l *Limiter) wait(ctx context.Context, configID uuid.UUID, p Params, class Class) error {
	if err := l.ensureBucket(ctx, configID, p.Burst); err != nil {
		return err
	}
	reserve := p.reserve(class)
	started := time.Now()
	logged := false
	for {
		granted, available, err := l.tryTake(ctx, configID, p, reserve)
		if err != nil {
			return err
		}
		if granted {
			return nil
		}
		wait := untilAvailable(1+reserve-available, p.RefillPerSecond)
		if !logged && time.Since(started) > logWaitAfter {
			logged = true
			log.Printf("[autopay ratelimit] config %s has waited %s for a gateway token", configID, time.Since(started).Round(time.Second))
		}
		if err := l.sleep(ctx, wait); err != nil {
			return err
		}
	}
}

// untilAvailable is how long to sleep before re-checking when `missing` more tokens are needed, with jitter so
// that several waiters do not wake up in lockstep.
func untilAvailable(missing, refillPerSecond float64) time.Duration {
	d := maxPoll
	if refillPerSecond > 0 && missing > 0 {
		d = time.Duration(missing / refillPerSecond * float64(time.Second))
	}
	if d < minPoll {
		d = minPoll
	}
	if d > maxPoll {
		d = maxPoll
	}
	return d + time.Duration(rand.Int63n(int64(d)/4+1))
}

func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Penalize makes every caller of the config wait at least d: the gateway answered HTTP 429, so the merchant account's
// real limit is lower than our budget (another consumer of the same account, a limit lowered by the gateway). It empties
// the bucket and pushes its refill clock d into the future, on the DB clock, so the server and every cron sharing the
// bucket stop together; a longer penalty already in force is never shortened. The next Wait blocks until it has passed.
func (l *Limiter) Penalize(ctx context.Context, configID uuid.UUID, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	if d > MaxPenalty {
		d = MaxPenalty
	}
	if err := l.ensureBucket(ctx, configID, 0); err != nil {
		return err
	}
	err := l.db.WithContext(ctx).Exec(
		`UPDATE payment_gateway_rate_limits
		    SET tokens = 0,
		        refilled_at = GREATEST(refilled_at, clock_timestamp() + CAST(@seconds AS float8) * interval '1 second')
		  WHERE gateway_config_id = @id`,
		sql.Named("id", configID), sql.Named("seconds", d.Seconds()),
	).Error
	if err != nil {
		return fmt.Errorf("ratelimit: penalize config %s: %w", configID, err)
	}
	log.Printf("[autopay ratelimit] config %s is rate limited by its gateway: all calls paused for %s", configID, d.Round(time.Second))
	return nil
}

// ensureBucket creates the config's bucket row (full) the first time this process sees the config.
func (l *Limiter) ensureBucket(ctx context.Context, configID uuid.UUID, burst float64) error {
	if _, ok := l.ensured.Load(configID); ok {
		return nil
	}
	err := l.db.WithContext(ctx).Exec(
		`INSERT INTO payment_gateway_rate_limits (gateway_config_id, tokens) VALUES (@id, @burst)
		 ON CONFLICT (gateway_config_id) DO NOTHING`,
		sql.Named("id", configID), sql.Named("burst", burst),
	).Error
	if err != nil {
		return fmt.Errorf("ratelimit: ensure bucket for config %s: %w", configID, err)
	}
	l.ensured.Store(configID, struct{}{})
	return nil
}

// takeSQL refills the bucket on the DB clock and takes one token in a single atomic statement: the bucket row is
// locked by the first SELECT, so concurrent callers (any process) are serialized. refilled_at only moves when a
// token is taken, so a denied caller does not lose the refill that has accrued.
const takeSQL = `
WITH cur AS (
    SELECT gateway_config_id,
           clock_timestamp() AS now_at,
           LEAST(CAST(@burst AS float8), tokens + EXTRACT(EPOCH FROM (clock_timestamp() - refilled_at)) * CAST(@refill AS float8)) AS available
      FROM payment_gateway_rate_limits
     WHERE gateway_config_id = @id
       FOR UPDATE
), taken AS (
    UPDATE payment_gateway_rate_limits r
       SET tokens = cur.available - 1, refilled_at = cur.now_at
      FROM cur
     WHERE r.gateway_config_id = cur.gateway_config_id AND cur.available >= 1 + CAST(@reserve AS float8)
 RETURNING 1
)
SELECT (SELECT count(*) FROM taken) AS granted, (SELECT available FROM cur) AS available`

type takeResult struct {
	Granted   int
	Available float64
}

// tryTake attempts to take one token. granted=false means the caller must wait; available is the token count seen.
func (l *Limiter) tryTake(ctx context.Context, configID uuid.UUID, p Params, reserve float64) (granted bool, available float64, err error) {
	var res takeResult
	err = l.db.WithContext(ctx).Raw(takeSQL,
		sql.Named("id", configID), sql.Named("burst", p.Burst),
		sql.Named("refill", p.RefillPerSecond), sql.Named("reserve", reserve),
	).Scan(&res).Error
	if err != nil {
		return false, 0, fmt.Errorf("ratelimit: take token for config %s: %w", configID, err)
	}
	return res.Granted == 1, res.Available, nil
}
