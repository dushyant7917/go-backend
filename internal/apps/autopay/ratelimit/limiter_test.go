package ratelimit

import (
	"context"
	"math"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestParamsFor(t *testing.T) {
	cases := []struct {
		rpm        int
		wantBurst  float64
		wantRefill float64 // per second
	}{
		{60, 5, 50.0 / 60}, // budget 55 = 60*r + b
		{100, 5, 90.0 / 60},
		{12, 3, 4.0 / 60}, // budget 7 -> burst 3
		{8, 1, 2.0 / 60},  // budget 3 -> burst 1
		{0, 1, 1.0 / 60},  // floor: budget 2
	}
	for _, c := range cases {
		p := ParamsFor(c.rpm)
		if p.Burst != c.wantBurst || math.Abs(p.RefillPerSecond-c.wantRefill) > 1e-9 {
			t.Errorf("ParamsFor(%d) = %+v, want burst %v refill %v", c.rpm, p, c.wantBurst, c.wantRefill)
		}
		// worst case in any 60 s window must stay within the budget
		budget := float64(c.rpm - BufferRequestsPerMinute)
		if budget < 2 {
			budget = 2
		}
		if worst := 60*p.RefillPerSecond + p.Burst; worst > budget+1e-9 {
			t.Errorf("ParamsFor(%d): worst case %v exceeds budget %v", c.rpm, worst, budget)
		}
	}
}

func TestReserveNeverStarvesSmallBuckets(t *testing.T) {
	if r := ParamsFor(60).reserve(Background); r != BackgroundReserve {
		t.Errorf("reserve at burst 5 = %v", r)
	}
	if r := ParamsFor(60).reserve(Interactive); r != 0 {
		t.Errorf("interactive reserve = %v", r)
	}
	small := Params{Burst: 1, RefillPerSecond: 1}
	if r := small.reserve(Background); r != 0 {
		t.Errorf("burst 1 must not reserve anything, got %v", r)
	}
}

func TestUntilAvailableBounds(t *testing.T) {
	if d := untilAvailable(1, 0); d < maxPoll {
		t.Errorf("no refill must poll slowly, got %v", d)
	}
	if d := untilAvailable(0.0001, 1000); d < minPoll {
		t.Errorf("must not poll faster than %v, got %v", minPoll, d)
	}
	if d := untilAvailable(1000, 0.001); d > maxPoll+maxPoll/4 {
		t.Errorf("must cap the sleep, got %v", d)
	}
}

// ---- integration tests (real Postgres with the autopay migration applied) ----
//
//	AUTOPAY_IT_DSN="host=localhost port=5432 user=postgres password=postgres dbname=autopay_it sslmode=disable" \
//	  go test ./internal/apps/autopay/ratelimit/
//
// They are skipped when AUTOPAY_IT_DSN is unset. They only create and delete their own payment_gateway_configs rows.

func integrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("AUTOPAY_IT_DSN")
	if dsn == "" {
		t.Skip("AUTOPAY_IT_DSN not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	return db
}

// newConfig inserts a config row (the bucket's foreign key target) and removes it, and its bucket, on cleanup.
func newConfig(t *testing.T, db *gorm.DB) uuid.UUID {
	t.Helper()
	id := uuid.New()
	err := db.Exec(`INSERT INTO payment_gateway_configs (id, gateway, app_name, environment) VALUES (?, 'test', ?, 'test')`, id, "ratelimit-"+id.String()).Error
	if err != nil {
		t.Fatalf("insert config: %v", err)
	}
	t.Cleanup(func() { db.Exec(`DELETE FROM payment_gateway_configs WHERE id = ?`, id) })
	return id
}

func countGrants(t *testing.T, l *Limiter, id uuid.UUID, p Params, class Class, attempts int) int {
	t.Helper()
	granted := 0
	for i := 0; i < attempts; i++ {
		ok, _, err := l.tryTake(context.Background(), id, p, p.reserve(class))
		if err != nil {
			t.Fatalf("tryTake: %v", err)
		}
		if ok {
			granted++
		}
	}
	return granted
}

func TestBurstThenDenied(t *testing.T) {
	db := integrationDB(t)
	l := New(db)
	id := newConfig(t, db)
	p := Params{Burst: 3, RefillPerSecond: 0.0001}
	if err := l.ensureBucket(context.Background(), id, p.Burst); err != nil {
		t.Fatal(err)
	}
	if got := countGrants(t, l, id, p, Interactive, 10); got != 3 {
		t.Fatalf("granted %d tokens, want exactly the burst of 3", got)
	}
}

func TestRefillOverTime(t *testing.T) {
	db := integrationDB(t)
	l := New(db)
	id := newConfig(t, db)
	p := Params{Burst: 5, RefillPerSecond: 50}
	if err := l.ensureBucket(context.Background(), id, p.Burst); err != nil {
		t.Fatal(err)
	}
	if got := countGrants(t, l, id, Params{Burst: 5, RefillPerSecond: 0.0001}, Interactive, 5); got != 5 {
		t.Fatalf("drain granted %d", got)
	}
	drained := time.Now()
	time.Sleep(120 * time.Millisecond) // ~6 tokens at 50/s, capped at the burst of 5
	got := countGrants(t, l, id, p, Interactive, 10)
	// Tokens keep refilling while the grants run (slow on a loaded machine), so the ceiling is what the elapsed time
	// can have produced: never more than refill*elapsed + 1 since the drain.
	ceiling := int(time.Since(drained).Seconds()*p.RefillPerSecond) + 1
	if got < 3 || got > ceiling {
		t.Fatalf("after refill granted %d tokens, want between 3 and %d", got, ceiling)
	}
}

func TestBackgroundLeavesReserveForInteractive(t *testing.T) {
	db := integrationDB(t)
	l := New(db)
	id := newConfig(t, db)
	p := Params{Burst: 5, RefillPerSecond: 0.0001}
	if err := l.ensureBucket(context.Background(), id, p.Burst); err != nil {
		t.Fatal(err)
	}
	// Background needs tokens >= 1 + reserve(2): it may take at 5, 4, 3 but not at 2.
	if got := countGrants(t, l, id, p, Background, 10); got != 3 {
		t.Fatalf("background granted %d, want 3", got)
	}
	// The reserved 2 tokens are still there for the web server.
	if got := countGrants(t, l, id, p, Interactive, 10); got != 2 {
		t.Fatalf("interactive granted %d, want the 2 reserved tokens", got)
	}
}

func TestConcurrentTakersNeverExceedTheBucket(t *testing.T) {
	db := integrationDB(t)
	l := New(db)
	id := newConfig(t, db)
	p := Params{Burst: 5, RefillPerSecond: 0.0001}
	if err := l.ensureBucket(context.Background(), id, p.Burst); err != nil {
		t.Fatal(err)
	}
	var granted int64
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, _, err := l.tryTake(context.Background(), id, p, 0)
			if err != nil {
				t.Errorf("tryTake: %v", err)
				return
			}
			if ok {
				atomic.AddInt64(&granted, 1)
			}
		}()
	}
	wg.Wait()
	if granted != 5 {
		t.Fatalf("20 concurrent takers were granted %d tokens, want exactly 5", granted)
	}
}

func TestWaitBlocksUntilRefilled(t *testing.T) {
	db := integrationDB(t)
	l := New(db)
	id := newConfig(t, db)
	p := Params{Burst: 1, RefillPerSecond: 25} // one token every 40 ms
	start := time.Now()
	for i := 0; i < 4; i++ {
		if err := l.wait(context.Background(), id, p, Interactive); err != nil {
			t.Fatalf("wait %d: %v", i, err)
		}
	}
	// first token is free (full bucket); the next three need ~3 refills = ~120 ms
	if elapsed := time.Since(start); elapsed < 90*time.Millisecond || elapsed > 3*time.Second {
		t.Fatalf("4 waits took %v, want roughly 120 ms", elapsed)
	}
}

func TestWaitHonoursContext(t *testing.T) {
	db := integrationDB(t)
	l := New(db)
	id := newConfig(t, db)
	p := Params{Burst: 1, RefillPerSecond: 0.0001}
	if err := l.wait(context.Background(), id, p, Interactive); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if err := l.wait(ctx, id, p, Interactive); err == nil {
		t.Fatal("an empty bucket must block until the context ends")
	}
}

func TestMissingConfigFailsClosed(t *testing.T) {
	db := integrationDB(t)
	l := New(db)
	if err := l.Wait(context.Background(), uuid.New(), 60, Interactive); err == nil {
		t.Fatal("a bucket for a config that does not exist must be an error (fail closed)")
	}
}

func TestPenalizeBlocksEveryCallerUntilItPasses(t *testing.T) {
	db := integrationDB(t)
	l := New(db)
	id := newConfig(t, db)
	p := Params{Burst: 5, RefillPerSecond: 50}
	if err := l.ensureBucket(context.Background(), id, p.Burst); err != nil {
		t.Fatal(err)
	}
	if got := countGrants(t, l, id, p, Interactive, 1); got != 1 {
		t.Fatalf("a full bucket grants, got %d", got)
	}
	if err := l.Penalize(context.Background(), id, 400*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if got := countGrants(t, l, id, p, Interactive, 5); got != 0 {
		t.Fatalf("during the penalty nobody gets a token (the gateway said stop), got %d", got)
	}
	started := time.Now()
	if err := l.wait(context.Background(), id, p, Background); err != nil {
		t.Fatal(err)
	}
	if waited := time.Since(started); waited < 300*time.Millisecond {
		t.Fatalf("Wait must block until the penalty has passed, waited only %v", waited)
	}
}

func TestPenalizeNeverShortensAndIsCapped(t *testing.T) {
	db := integrationDB(t)
	l := New(db)
	id := newConfig(t, db)
	if err := l.Penalize(context.Background(), id, time.Hour); err != nil { // capped at MaxPenalty
		t.Fatal(err)
	}
	var secs float64
	read := func() float64 {
		if err := db.Raw(`SELECT EXTRACT(EPOCH FROM (refilled_at - clock_timestamp())) FROM payment_gateway_rate_limits WHERE gateway_config_id = ?`, id).Scan(&secs).Error; err != nil {
			t.Fatal(err)
		}
		return secs
	}
	if got := read(); got < MaxPenalty.Seconds()-5 || got > MaxPenalty.Seconds()+1 {
		t.Fatalf("a huge Retry-After is capped at %v, got a %vs penalty", MaxPenalty, got)
	}
	if err := l.Penalize(context.Background(), id, time.Second); err != nil { // a shorter one must not shorten the longer
		t.Fatal(err)
	}
	if got := read(); got < MaxPenalty.Seconds()-5 {
		t.Fatalf("a shorter penalty shortened the one in force: %vs", got)
	}
	if err := l.Penalize(context.Background(), id, 0); err != nil { // nothing to do
		t.Fatal(err)
	}
}
