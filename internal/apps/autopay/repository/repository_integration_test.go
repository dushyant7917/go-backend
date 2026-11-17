package repository

import (
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go-backend/internal/apps/autopay/models"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Run with AUTOPAY_IT_DSN pointing at a database with the autopay migration applied (see ratelimit/limiter_test.go).
// Skipped when unset. Every test creates its own config and removes everything it created.

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

// newUser inserts a real users row (autopay_mandates.user_id is a foreign key to users) and removes it afterwards.
func newUser(t *testing.T, db *gorm.DB) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if err := db.Exec(`INSERT INTO users (id, app_name, email) VALUES (?, 'autopay-it', ?)`, id, id.String()+"@example.com").Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { db.Exec(`DELETE FROM users WHERE id = ?`, id) })
	return id
}

type fixture struct {
	repo    Repository
	db      *gorm.DB
	cfg     models.PaymentGatewayConfig
	mandate models.AutopayMandate
	cycle   models.AutopayBillingCycle
	now     time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db := integrationDB(t)
	f := &fixture{repo: New(db), db: db, now: time.Now().UTC().Truncate(time.Microsecond)}
	f.cfg = models.PaymentGatewayConfig{Gateway: "test", AppName: "app-" + uuid.NewString(), Environment: "test", IsActive: true, AcceptsNewAutopay: true}
	must(t, db.Create(&f.cfg).Error)
	t.Cleanup(func() {
		db.Exec(`DELETE FROM autopay_webhook_events WHERE gateway_config_id = ?`, f.cfg.ID)
		db.Exec(`DELETE FROM autopay_mandates WHERE gateway_config_id = ?`, f.cfg.ID)
		db.Unscoped().Delete(&models.PaymentGatewayConfig{}, "id = ?", f.cfg.ID)
	})

	due := f.now.Add(-time.Hour)
	end := f.now.Add(30 * 24 * time.Hour)
	f.mandate = models.AutopayMandate{
		UserID: newUser(t, db), AppName: f.cfg.AppName, GatewayConfigID: f.cfg.ID, PaymentType: models.PaymentTypeRecurring,
		MerchantAutopayID: "m-" + uuid.NewString(), Status: models.MandateStatusActive, MaxDebitAmount: 29900,
		BillingFrequency: "monthly", StartAt: &f.now, EndAt: &end, NextChargeDueAt: &due,
	}
	must(t, f.repo.CreateMandate(nil, &f.mandate))
	f.cycle = f.newCycle(t, 1, models.BillingCycleStatusAwaitingPayment, &due)
	return f
}

func (f *fixture) newCycle(t *testing.T, number int, status models.BillingCycleStatus, nextAttempt *time.Time) models.AutopayBillingCycle {
	t.Helper()
	end := f.now.Add(30 * 24 * time.Hour)
	c := models.AutopayBillingCycle{
		AutopayMandateID: f.mandate.ID, CycleNumber: number, StartAt: f.now.Add(time.Duration(number) * time.Minute),
		EndAt: &end, Amount: 29900, Status: status, NextChargeAttemptAt: nextAttempt,
	}
	must(t, f.repo.CreateBillingCycle(nil, &c))
	return c
}

func (f *fixture) newAttempt(t *testing.T, number int, status models.ChargeAttemptStatus, delivered *time.Time) models.AutopayChargeAttempt {
	t.Helper()
	a := models.AutopayChargeAttempt{
		BillingCycleID: f.cycle.ID, GatewayConfigID: f.cfg.ID, AttemptNumber: number, CountsTowardAttemptLimit: true,
		MerchantPaymentID: "p-" + uuid.NewString(), Status: status, Amount: 29900, PreDebitNotificationDeliveredAt: delivered,
	}
	must(t, f.repo.CreateChargeAttempt(nil, &a))
	return a
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func ids(as []models.AutopayChargeAttempt) map[uuid.UUID]bool {
	m := map[uuid.UUID]bool{}
	for _, a := range as {
		m[a.ID] = true
	}
	return m
}

func TestDebitGateRequiresDeliveredAgedNotificationDueCycleActiveMandate(t *testing.T) {
	f := newFixture(t)
	const gap = 26 * time.Hour
	old := f.now.Add(-27 * time.Hour)
	young := f.now.Add(-2 * time.Hour)

	ready := f.newAttempt(t, 1, models.ChargeAttemptStatusNotificationDelivered, &old)
	// each of these violates exactly one condition of the gate
	tooYoung := f.newAttempt(t, 2, models.ChargeAttemptStatusNotificationDelivered, &young)
	noDeliveredAt := f.newAttempt(t, 3, models.ChargeAttemptStatusNotificationDelivered, nil)
	notDelivered := f.newAttempt(t, 4, models.ChargeAttemptStatusAwaitingNotification, &old)
	alreadySubmitted := f.newAttempt(t, 5, models.ChargeAttemptStatusDebitSubmitted, &old)

	got, err := f.repo.FindChargeAttemptsReadyForDebit(f.cfg.ID, f.now, gap, 0)
	must(t, err)
	g := ids(got)
	if !g[ready.ID] || len(g) != 1 {
		t.Fatalf("gate returned %v; want only the ready attempt (excluded: young=%v noDelivered=%v notDelivered=%v submitted=%v)",
			g, g[tooYoung.ID], g[noDeliveredAt.ID], g[notDelivered.ID], g[alreadySubmitted.ID])
	}

	// the cycle is not due yet -> nothing is ready
	future := f.now.Add(time.Hour)
	must(t, f.db.Model(&models.AutopayBillingCycle{}).Where("id = ?", f.cycle.ID).Update("next_charge_attempt_at", future).Error)
	if got, _ = f.repo.FindChargeAttemptsReadyForDebit(f.cfg.ID, f.now, gap, 0); len(got) != 0 {
		t.Fatalf("a cycle that is not due must not be debited, got %d", len(got))
	}
	must(t, f.db.Model(&models.AutopayBillingCycle{}).Where("id = ?", f.cycle.ID).Update("next_charge_attempt_at", f.now.Add(-time.Hour)).Error)

	// a paused mandate is never debited
	must(t, f.db.Model(&models.AutopayMandate{}).Where("id = ?", f.mandate.ID).Update("status", models.MandateStatusPaused).Error)
	if got, _ = f.repo.FindChargeAttemptsReadyForDebit(f.cfg.ID, f.now, gap, 0); len(got) != 0 {
		t.Fatalf("a paused mandate must not be debited, got %d", len(got))
	}

	// another merchant account's config never sees this attempt
	must(t, f.db.Model(&models.AutopayMandate{}).Where("id = ?", f.mandate.ID).Update("status", models.MandateStatusActive).Error)
	if got, _ = f.repo.FindChargeAttemptsReadyForDebit(uuid.New(), f.now, gap, 0); len(got) != 0 {
		t.Fatalf("the gate is scoped to the config, got %d", len(got))
	}
}

func TestDebitGateHonoursTheGatewayRetryGap(t *testing.T) {
	f := newFixture(t)
	const gap = 26 * time.Hour
	const retryGap = 90 * time.Minute
	old := f.now.Add(-27 * time.Hour)

	neverTried := f.newAttempt(t, 1, models.ChargeAttemptStatusNotificationDelivered, &old)
	releasedRecently := f.newAttempt(t, 2, models.ChargeAttemptStatusNotificationDelivered, &old)
	releasedLongAgo := f.newAttempt(t, 3, models.ChargeAttemptStatusNotificationDelivered, &old)
	// updated_at is when the attempt was released after its try
	set := func(id uuid.UUID, tries int, updatedAt time.Time) {
		must(t, f.db.Exec("UPDATE autopay_charge_attempts SET debit_submit_attempts = ?, updated_at = ? WHERE id = ?", tries, updatedAt, id).Error)
	}
	set(neverTried.ID, 0, f.now.Add(-time.Minute)) // untouched a minute ago, but it was never tried: no gap applies
	set(releasedRecently.ID, 1, f.now.Add(-20*time.Minute))
	set(releasedLongAgo.ID, 1, f.now.Add(-110*time.Minute))

	got, err := f.repo.FindChargeAttemptsReadyForDebit(f.cfg.ID, f.now, gap, retryGap)
	must(t, err)
	g := ids(got)
	if !g[neverTried.ID] || !g[releasedLongAgo.ID] || g[releasedRecently.ID] || len(g) != 2 {
		t.Fatalf("with a 90m retry gap want never-tried and released-110m-ago, got %v (released-20m-ago included: %v)", g, g[releasedRecently.ID])
	}

	// without a gap (Razorpay, Cashfree) the recently released attempt is eligible again
	got, err = f.repo.FindChargeAttemptsReadyForDebit(f.cfg.ID, f.now, gap, 0)
	must(t, err)
	if g = ids(got); !g[releasedRecently.ID] || len(g) != 3 {
		t.Fatalf("without a retry gap all three are eligible, got %v", g)
	}
}

func TestDebitClaimHasExactlyOneWinner(t *testing.T) {
	f := newFixture(t)
	old := f.now.Add(-27 * time.Hour)
	a := f.newAttempt(t, 1, models.ChargeAttemptStatusNotificationDelivered, &old)

	var winners int64
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			won, err := f.repo.ClaimChargeAttemptForDebit(a.ID, 0, "exec-"+uuid.NewString())
			if err != nil {
				t.Errorf("claim: %v", err)
				return
			}
			if won {
				atomic.AddInt64(&winners, 1)
			}
		}()
	}
	wg.Wait()
	if winners != 1 {
		t.Fatalf("%d claimers won the debit, want exactly 1", winners)
	}
	got, _ := f.repo.FindChargeAttemptByID(a.ID)
	if got.Status != models.ChargeAttemptStatusDebitSubmitted || got.DebitSubmitAttempts != 1 || got.DebitExecutionID == nil {
		t.Fatalf("claimed attempt wrong: %+v", got)
	}

	// once the claim is released (the service puts it back to notification_delivered) the next claim expects the
	// incremented counter
	must(t, f.repo.UpdateChargeAttemptFields(a.ID, map[string]interface{}{"status": models.ChargeAttemptStatusNotificationDelivered}))
	if won, _ := f.repo.ClaimChargeAttemptForDebit(a.ID, 0, "stale"); won {
		t.Fatal("a claim with a stale expected counter must lose")
	}
	if won, _ := f.repo.ClaimChargeAttemptForDebit(a.ID, 1, "exec-2"); !won {
		t.Fatal("a claim with the current counter must win after a release")
	}
}

func TestNotificationClaimAndWorkQueues(t *testing.T) {
	f := newFixture(t)
	a := f.newAttempt(t, 1, models.ChargeAttemptStatusAwaitingNotification, nil)

	got, err := f.repo.FindChargeAttemptsAwaitingPreDebitNotificationSend(f.cfg.ID, f.now.Add(time.Minute), 2)
	must(t, err)
	if !ids(got)[a.ID] {
		t.Fatal("a never-sent attempt must be queued for sending")
	}

	var winners int64
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if won, _ := f.repo.ClaimChargeAttemptForNotificationSend(a.ID, 0, "n-"+uuid.NewString()); won {
				atomic.AddInt64(&winners, 1)
			}
		}()
	}
	wg.Wait()
	if winners != 1 {
		t.Fatalf("%d claimers won the notification send, want 1", winners)
	}

	// after the send is recorded it moves from the "send" queue to the "outcome" queue
	sent := f.now.Add(-time.Hour)
	must(t, f.repo.UpdateChargeAttemptFields(a.ID, map[string]interface{}{"pre_debit_notification_sent_at": sent}))
	if got, _ = f.repo.FindChargeAttemptsAwaitingPreDebitNotificationSend(f.cfg.ID, f.now.Add(time.Minute), 2); len(got) != 0 {
		t.Fatal("a sent attempt must leave the send queue")
	}
	if got, _ = f.repo.FindChargeAttemptsAwaitingNotificationOutcome(f.cfg.ID, f.now); !ids(got)[a.ID] {
		t.Fatal("a sent attempt must be in the outcome queue")
	}

	// the per-attempt limit of 2 sends: at 2 send attempts it no longer needs sending even if never sent
	b := f.newAttempt(t, 2, models.ChargeAttemptStatusAwaitingNotification, nil)
	must(t, f.repo.UpdateChargeAttemptFields(b.ID, map[string]interface{}{"pre_debit_notification_send_attempts": 2}))
	if got, _ = f.repo.FindChargeAttemptsAwaitingPreDebitNotificationSend(f.cfg.ID, f.now.Add(time.Minute), 2); ids(got)[b.ID] {
		t.Fatal("an attempt that used its 2 sends must not be queued again")
	}

	// debit-submitted attempts are the sync cron's "awaiting result" queue
	c := f.newAttempt(t, 3, models.ChargeAttemptStatusDebitSubmitted, nil)
	if got, _ = f.repo.FindChargeAttemptsAwaitingDebitResult(f.cfg.ID, time.Now().UTC().Add(time.Minute)); !ids(got)[c.ID] {
		t.Fatal("a submitted attempt must await its result")
	}
	if open, _ := f.repo.HasOpenChargeAttemptForBillingCycle(f.cycle.ID); !open {
		t.Fatal("open attempts must block a new attempt on the cycle")
	}
}

func TestOpenAttemptCheckIgnoresTerminalAttempts(t *testing.T) {
	f := newFixture(t)
	f.newAttempt(t, 1, models.ChargeAttemptStatusDebitFailed, nil)
	f.newAttempt(t, 2, models.ChargeAttemptStatusDebitSucceeded, nil)
	if open, _ := f.repo.HasOpenChargeAttemptForBillingCycle(f.cycle.ID); open {
		t.Fatal("terminal attempts must not block a retry")
	}
}

func TestBillingWindowQueries(t *testing.T) {
	f := newFixture(t)
	start, end := f.now.Add(-48*time.Hour), f.now.Add(26*time.Hour)

	// the fixture cycle is awaiting payment with next_charge_attempt_at in the window -> retry queue
	got, err := f.repo.FindRecurringMandatesForRetry(f.cfg.ID, start, end)
	must(t, err)
	if len(got) != 1 || got[0].ID != f.mandate.ID {
		t.Fatalf("retry queue = %d mandates, want the fixture mandate", len(got))
	}
	if got, _ = f.repo.FindRecurringMandatesForRetry(uuid.New(), start, end); len(got) != 0 {
		t.Fatal("the retry queue is scoped to the config")
	}

	// new-cycle queue needs the LATEST cycle to be paid
	if got, _ = f.repo.FindRecurringMandatesForNewBillingCycle(f.cfg.ID, start, end); len(got) != 0 {
		t.Fatal("a mandate whose latest cycle is unpaid must not get a new cycle")
	}
	must(t, f.db.Model(&models.AutopayBillingCycle{}).Where("id = ?", f.cycle.ID).Update("status", models.BillingCycleStatusPaid).Error)
	if got, _ = f.repo.FindRecurringMandatesForNewBillingCycle(f.cfg.ID, start, end); len(got) != 1 {
		t.Fatalf("a mandate with a paid latest cycle and a due charge must be queued, got %d", len(got))
	}
	// ...but not when the mandate ends inside the window
	soon := f.now.Add(2 * time.Hour)
	must(t, f.db.Model(&models.AutopayMandate{}).Where("id = ?", f.mandate.ID).Update("end_at", soon).Error)
	if got, _ = f.repo.FindRecurringMandatesForNewBillingCycle(f.cfg.ID, start, end); len(got) != 0 {
		t.Fatal("a mandate ending inside the window must not be billed again")
	}
}

func TestIsSubscriptionActiveAndTrialUsed(t *testing.T) {
	f := newFixture(t)
	uid, app := f.mandate.UserID, f.cfg.AppName

	if active, _ := f.repo.IsSubscriptionActive(uid, app, f.now); active {
		t.Fatal("no paid cycle 0 yet: must not be active")
	}
	if used, _ := f.repo.HasCompletedAuthorizationPayment(uid, app); used {
		t.Fatal("no paid cycle 0 yet: the trial is not used")
	}

	auth := f.newCycle(t, 0, models.BillingCycleStatusPaid, nil)
	_ = auth
	if active, _ := f.repo.IsSubscriptionActive(uid, app, f.now.Add(time.Hour)); !active {
		t.Fatal("a paid cycle 0 covering now must be active")
	}
	if used, _ := f.repo.HasCompletedAuthorizationPayment(uid, app); !used {
		t.Fatal("a paid cycle 0 means the trial is used")
	}
	if active, _ := f.repo.IsSubscriptionActive(uid, app, f.now.Add(60*24*time.Hour)); active {
		t.Fatal("past the end of every cycle (+24h grace) must not be active")
	}
	if active, _ := f.repo.IsSubscriptionActive(uuid.New(), app, f.now); active {
		t.Fatal("another user is not active")
	}
}

func TestWebhookEventDedupeAndRetention(t *testing.T) {
	f := newFixture(t)
	key := "evt-" + uuid.NewString()

	first := &models.AutopayWebhookEvent{GatewayConfigID: f.cfg.ID, EventDedupeKey: key, RawBody: `{"a":1}`}
	if inserted, err := f.repo.InsertWebhookEvent(first); err != nil || !inserted {
		t.Fatalf("first insert: inserted=%v err=%v", inserted, err)
	}
	dup := &models.AutopayWebhookEvent{GatewayConfigID: f.cfg.ID, EventDedupeKey: key, RawBody: `{"a":1}`}
	if inserted, err := f.repo.InsertWebhookEvent(dup); err != nil || inserted {
		t.Fatalf("duplicate delivery must be reported as not inserted: inserted=%v err=%v", inserted, err)
	}

	pending, _ := f.repo.FindUnprocessedWebhookEvents(time.Now().UTC().Add(time.Minute), 10)
	found := false
	for _, e := range pending {
		found = found || e.ID == first.ID
	}
	if !found {
		t.Fatal("a received event is unprocessed work")
	}

	// retention: finished events older than the cutoff are deleted, unfinished ones never
	other := &models.AutopayWebhookEvent{GatewayConfigID: f.cfg.ID, EventDedupeKey: "evt-" + uuid.NewString(), RawBody: "{}"}
	_, _ = f.repo.InsertWebhookEvent(other)
	must(t, f.repo.MarkWebhookEvent(first.ID, models.WebhookEventStatusProcessed, ""))
	cutoff := time.Now().UTC().Add(time.Hour)
	n, err := f.repo.DeleteFinishedWebhookEventsBefore(cutoff, 100)
	must(t, err)
	if n < 1 {
		t.Fatalf("retention deleted %d rows, want the processed event", n)
	}
	var left int64
	f.db.Model(&models.AutopayWebhookEvent{}).Where("id = ?", other.ID).Count(&left)
	if left != 1 {
		t.Fatal("an unprocessed event must survive the retention job")
	}
	if err := f.repo.MarkWebhookEvent(uuid.New(), models.WebhookEventStatusProcessed, ""); err == nil {
		t.Fatal("marking an unknown event must be an error")
	}
}

func TestLookupsAreScopedByConfig(t *testing.T) {
	f := newFixture(t)
	a := f.newAttempt(t, 1, models.ChargeAttemptStatusAwaitingNotification, nil)
	order, pay, link := "order_"+uuid.NewString(), "pay_"+uuid.NewString(), "plink_"+uuid.NewString()
	must(t, f.repo.UpdateChargeAttemptFields(a.ID, map[string]interface{}{"gateway_order_id": order, "gateway_payment_id": pay, "gateway_payment_link_id": link}))

	for name, find := range map[string]func(uuid.UUID) (*models.AutopayChargeAttempt, error){
		"order": func(c uuid.UUID) (*models.AutopayChargeAttempt, error) {
			return f.repo.FindChargeAttemptByGatewayOrderID(c, order)
		},
		"payment": func(c uuid.UUID) (*models.AutopayChargeAttempt, error) {
			return f.repo.FindChargeAttemptByGatewayPaymentID(c, pay)
		},
		"link": func(c uuid.UUID) (*models.AutopayChargeAttempt, error) {
			return f.repo.FindChargeAttemptByGatewayPaymentLinkID(c, link)
		},
	} {
		if got, err := find(f.cfg.ID); err != nil || got.ID != a.ID {
			t.Errorf("%s lookup in its own config failed: %v", name, err)
		}
		if _, err := find(uuid.New()); err == nil {
			t.Errorf("%s lookup must not cross merchant accounts", name)
		}
	}
	if got, err := f.repo.FindAuthorizationChargeAttempt(f.mandate.ID); err == nil {
		t.Errorf("cycle 1 attempts are not authorization attempts, got %v", got.ID)
	}
}

// Cycles are half-open [start_at, end_at): recurring mandates get models.RecurringCycleGrace past end_at, one-time
// payments end exactly at end_at.
func TestIsSubscriptionActiveCycleBoundaryAndRecurringOnlyGrace(t *testing.T) {
	f := newFixture(t)
	app := f.cfg.AppName
	end := f.now.Add(30 * 24 * time.Hour) // newCycle's end_at

	// recurring: paid cycle 0 ending at `end`
	f.newCycle(t, 0, models.BillingCycleStatusPaid, nil)
	recurring := f.mandate.UserID
	for _, c := range []struct {
		at   time.Time
		want bool
	}{
		{end.Add(-time.Second), true},
		{end.Add(models.RecurringCycleGrace - time.Second), true}, // grace: the next cycle row may not exist yet
		{end.Add(models.RecurringCycleGrace), false},
	} {
		if got, _ := f.repo.IsSubscriptionActive(recurring, app, c.at); got != c.want {
			t.Fatalf("recurring at end%+v: active=%v, want %v", c.at.Sub(end), got, c.want)
		}
	}

	// one-time: a separate user with a paid cycle 0 ending at `end` gets no grace
	oneTime := models.AutopayMandate{
		UserID: newUser(t, f.db), AppName: app, GatewayConfigID: f.cfg.ID, PaymentType: models.PaymentTypeOneTime,
		MerchantAutopayID: "m-" + uuid.NewString(), Status: models.MandateStatusActive, MaxDebitAmount: 9900,
		BillingFrequency: "monthly", StartAt: &f.now, EndAt: &end,
	}
	must(t, f.repo.CreateMandate(nil, &oneTime))
	must(t, f.repo.CreateBillingCycle(nil, &models.AutopayBillingCycle{
		AutopayMandateID: oneTime.ID, CycleNumber: 0, StartAt: f.now, EndAt: &end, Amount: 9900, Status: models.BillingCycleStatusPaid,
	}))
	if got, _ := f.repo.IsSubscriptionActive(oneTime.UserID, app, end.Add(-time.Second)); !got {
		t.Fatal("one-time: active just before end_at")
	}
	if got, _ := f.repo.IsSubscriptionActive(oneTime.UserID, app, end); got {
		t.Fatal("one-time: must not be active at end_at (no grace)")
	}
}
