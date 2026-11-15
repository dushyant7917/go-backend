package service

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"go-backend/internal/apps/autopay/gateway"
	"go-backend/internal/apps/autopay/models"
	"go-backend/internal/apps/autopay/ratelimit"
	"go-backend/internal/apps/autopay/repository"
	userModels "go-backend/internal/apps/user/models"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// End-to-end tests of the core against the real schema with a programmable fake gateway. They are the gateway-neutral
// conformance suite: the same scenarios must hold for every adapter. Run with AUTOPAY_IT_DSN (see ratelimit tests).

// ---- fake gateway ----

type fakeGateway struct {
	mu    sync.Mutex
	calls map[string]int
	caps  gateway.Capabilities

	createMandate     func(gateway.MandateAuthorizationRequest) (*gateway.MandateAuthorizationSession, error)
	authStatus        func(gateway.MandateRef) (*gateway.MandateAuthorizationStatus, error)
	mandateStatus     func(gateway.MandateRef) (*gateway.MandateStatus, error)
	sendNotification  func(gateway.PreDebitNotificationRequest) (*gateway.PreDebitNotificationResult, error)
	fetchNotification func(gateway.ChargeAttemptRef) (*gateway.PreDebitNotificationStatus, error)
	submitDebit       func(gateway.DebitRequest) (*gateway.DebitSubmission, error)
	fetchAttempt      func(gateway.ChargeAttemptRef) (*gateway.ChargeAttemptStatus, error)
	parseWebhook      func(http.Header, []byte) ([]gateway.Event, error)
	cancelErr         error
}

func newFakeGateway() *fakeGateway {
	return &fakeGateway{
		calls: map[string]int{},
		caps: gateway.Capabilities{
			ChargeAttemptIdentity:   gateway.PaymentIDUnderMandate,
			SupportsIdempotencyKeys: true, PushesMandateStatusWebhooks: true, SupportsMandateCancellation: true,
			OneTimePaymentKinds: []string{gateway.OneTimePaymentKindOrder}, MinAmountMinorUnits: 100, RequestsPerMinuteLimit: 60,
			MinNotificationToDebitGap: 26 * time.Hour,
		},
	}
}

func (f *fakeGateway) count(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[name]
}

func (f *fakeGateway) hit(name string) {
	f.mu.Lock()
	f.calls[name]++
	f.mu.Unlock()
}

func (f *fakeGateway) Name() gateway.Name                 { return "test" }
func (f *fakeGateway) Capabilities() gateway.Capabilities { return f.caps }

func (f *fakeGateway) CreateMandateAuthorization(_ context.Context, r gateway.MandateAuthorizationRequest) (*gateway.MandateAuthorizationSession, error) {
	f.hit("createMandate")
	if f.createMandate != nil {
		return f.createMandate(r)
	}
	return &gateway.MandateAuthorizationSession{GatewayMandateID: "gw_" + r.MerchantAutopayID, ClientPayload: map[string]any{"session": "s1"}}, nil
}
func (f *fakeGateway) FetchMandateAuthorizationStatus(_ context.Context, r gateway.MandateRef) (*gateway.MandateAuthorizationStatus, error) {
	f.hit("authStatus")
	if f.authStatus != nil {
		return f.authStatus(r)
	}
	return &gateway.MandateAuthorizationStatus{State: gateway.AuthorizationPending}, nil
}
func (f *fakeGateway) FetchMandateStatus(_ context.Context, r gateway.MandateRef) (*gateway.MandateStatus, error) {
	f.hit("mandateStatus")
	if f.mandateStatus != nil {
		return f.mandateStatus(r)
	}
	return &gateway.MandateStatus{State: gateway.MandateActive}, nil
}
func (f *fakeGateway) CancelMandate(_ context.Context, _ gateway.MandateRef) error {
	f.hit("cancel")
	return f.cancelErr
}
func (f *fakeGateway) SendPreDebitNotification(_ context.Context, r gateway.PreDebitNotificationRequest) (*gateway.PreDebitNotificationResult, error) {
	f.hit("sendNotification")
	if f.sendNotification != nil {
		return f.sendNotification(r)
	}
	return &gateway.PreDebitNotificationResult{PreDebitNotificationID: r.PreDebitNotificationID, State: gateway.NotificationPending}, nil
}
func (f *fakeGateway) FetchPreDebitNotificationStatus(_ context.Context, r gateway.ChargeAttemptRef) (*gateway.PreDebitNotificationStatus, error) {
	f.hit("fetchNotification")
	if f.fetchNotification != nil {
		return f.fetchNotification(r)
	}
	return &gateway.PreDebitNotificationStatus{State: gateway.NotificationPending}, nil
}
func (f *fakeGateway) SubmitDebit(_ context.Context, r gateway.DebitRequest) (*gateway.DebitSubmission, error) {
	f.hit("submitDebit")
	if f.submitDebit != nil {
		return f.submitDebit(r)
	}
	return &gateway.DebitSubmission{State: gateway.PaymentPending, DebitExecutionID: r.DebitExecutionID}, nil
}
func (f *fakeGateway) FetchChargeAttemptStatus(_ context.Context, r gateway.ChargeAttemptRef) (*gateway.ChargeAttemptStatus, error) {
	f.hit("fetchAttempt")
	if f.fetchAttempt != nil {
		return f.fetchAttempt(r)
	}
	return &gateway.ChargeAttemptStatus{DebitExists: true, Debit: gateway.PaymentPending}, nil
}
func (f *fakeGateway) CreateOneTimePayment(_ context.Context, r gateway.OneTimePaymentRequest) (*gateway.OneTimePaymentSession, error) {
	f.hit("createOneTime")
	return &gateway.OneTimePaymentSession{Kind: r.Kind, GatewayOrderID: "order_" + r.MerchantPaymentID, ClientPayload: map[string]any{"order": "o1"}}, nil
}
func (f *fakeGateway) FetchOneTimePaymentStatus(_ context.Context, _ gateway.OneTimePaymentRef) (*gateway.OneTimePaymentStatus, error) {
	f.hit("fetchOneTime")
	return &gateway.OneTimePaymentStatus{State: gateway.PaymentSucceeded, GatewayPaymentID: "pay_ot"}, nil
}
func (f *fakeGateway) VerifyAndParseWebhook(h http.Header, body []byte) ([]gateway.Event, error) {
	f.hit("parseWebhook")
	if f.parseWebhook != nil {
		return f.parseWebhook(h, body)
	}
	return nil, nil
}

// ---- harness ----

type testResolver struct {
	rg *ResolvedGateway
}

func (r *testResolver) Resolve(_ context.Context, id uuid.UUID) (*ResolvedGateway, error) {
	if id == r.rg.Config.ID {
		return r.rg, nil
	}
	return nil, ErrNotFound
}
func (r *testResolver) ActiveConfigs(context.Context) ([]models.PaymentGatewayConfig, error) {
	return []models.PaymentGatewayConfig{*r.rg.Config}, nil
}
func (r *testResolver) Registered(n gateway.Name) bool { return n == "test" }

type unlimited struct{}

func (unlimited) Wait(context.Context, uuid.UUID, int, ratelimit.Class) error { return nil }

type testUsers struct{ user *userModels.User }

func (u testUsers) FindByID(uuid.UUID) (*userModels.User, error) { return u.user, nil }

type harness struct {
	t      *testing.T
	db     *gorm.DB
	repo   repository.Repository
	gw     *fakeGateway
	svc    *Service
	cfg    *models.PaymentGatewayConfig
	user   *userModels.User
	mu     sync.Mutex
	offset time.Duration // the test clock = real time + offset
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dsn := os.Getenv("AUTOPAY_IT_DSN")
	if dsn == "" {
		t.Skip("AUTOPAY_IT_DSN not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, db: db, repo: repository.New(db), gw: newFakeGateway()}
	cfg := &models.PaymentGatewayConfig{Gateway: "test", AppName: "app-" + uuid.NewString(), Environment: "test", IsActive: true, AcceptsNewAutopay: true}
	if err := db.Create(cfg).Error; err != nil {
		t.Fatal(err)
	}
	h.cfg = cfg
	t.Cleanup(func() {
		db.Exec(`DELETE FROM autopay_webhook_events WHERE gateway_config_id = ?`, cfg.ID)
		db.Exec(`DELETE FROM autopay_mandates WHERE gateway_config_id = ?`, cfg.ID)
		db.Unscoped().Delete(&models.PaymentGatewayConfig{}, "id = ?", cfg.ID)
	})
	email, phone, cc, name := "a@b.com", "9999999999", "91", "Test User"
	h.user = &userModels.User{ID: uuid.New(), Email: &email, Phone: &phone, CountryCode: &cc, Name: &name, AppName: cfg.AppName}
	// autopay_mandates.user_id is a foreign key to users, so the user must exist as a real row (removed afterwards)
	if err := db.Exec(`INSERT INTO users (id, app_name, email, phone, country_code) VALUES (?, ?, ?, ?, ?)`,
		h.user.ID, cfg.AppName, h.user.ID.String()+"@example.com", "9"+strings.ReplaceAll(h.user.ID.String(), "-", "")[:9], cc).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Exec(`DELETE FROM users WHERE id = ?`, h.user.ID) })

	rg := &ResolvedGateway{Config: cfg, Gateway: h.gw, RequestsPerMinute: 60}
	h.svc = New(Deps{
		Repo: h.repo, Gateways: &testResolver{rg: rg}, Limiter: unlimited{}, Users: testUsers{h.user},
		Environment: "test", Now: h.now, Sleep: func(context.Context, time.Duration) error { return nil },
	})
	return h
}

func (h *harness) now() time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	return time.Now().UTC().Add(h.offset)
}

func (h *harness) advanceTo(t time.Time) {
	h.mu.Lock()
	h.offset = t.Sub(time.Now().UTC())
	h.mu.Unlock()
}

func (h *harness) create() *CreateResult {
	h.t.Helper()
	res, err := h.svc.Create(context.Background(), CreateRequest{
		UserID: h.user.ID, AppName: h.cfg.AppName, Gateway: "test", PaymentType: models.PaymentTypeRecurring,
		AuthorizationAmount: 100, MaxDebitAmount: 29900, StartAt: h.now().Add(72 * time.Hour), BillingFrequency: "monthly",
	})
	if err != nil {
		h.t.Fatalf("create: %v", err)
	}
	return res
}

// activeMandate creates a mandate and completes its authorization, returning the mandate id.
func (h *harness) activeMandate() uuid.UUID {
	h.t.Helper()
	res := h.create()
	h.gw.authStatus = func(gateway.MandateRef) (*gateway.MandateAuthorizationStatus, error) {
		return &gateway.MandateAuthorizationStatus{State: gateway.AuthorizationSucceeded, GatewayPaymentID: "pay_auth_" + uuid.NewString()}, nil // unique per mandate: gateway payment ids are unique per config
	}
	view, err := h.svc.Verify(context.Background(), res.AutopayMandateID)
	if err != nil || view.Status != models.MandateStatusActive {
		h.t.Fatalf("verify: status=%v err=%v", view, err)
	}
	return res.AutopayMandateID
}

func (h *harness) mandate(id uuid.UUID) *models.AutopayMandate {
	m, err := h.repo.FindMandateByID(id)
	if err != nil {
		h.t.Fatal(err)
	}
	return m
}

func (h *harness) latestAttempt(mandateID uuid.UUID) *models.AutopayChargeAttempt {
	cycle, err := h.repo.FindLatestBillingCycle(mandateID)
	if err != nil {
		h.t.Fatal(err)
	}
	as, err := h.repo.FindChargeAttempts(cycle.ID)
	if err != nil || len(as) == 0 {
		h.t.Fatalf("no attempts: %v", err)
	}
	return &as[len(as)-1]
}

func (h *harness) cycleOf(a *models.AutopayChargeAttempt) *models.AutopayBillingCycle {
	c, err := h.repo.FindBillingCycleByID(a.BillingCycleID)
	if err != nil {
		h.t.Fatal(err)
	}
	return c
}

// deliverNotification pushes a "delivered" webhook for the attempt through the real HandleWebhook path.
func (h *harness) webhook(key string, events ...gateway.Event) error {
	for i := range events {
		events[i].DedupeKey = key
	}
	h.gw.parseWebhook = func(http.Header, []byte) ([]gateway.Event, error) { return events, nil }
	return h.svc.HandleWebhook(context.Background(), h.cfg.ID, http.Header{}, []byte(key))
}

// billingDay moves the clock to the night the first cycle is notified (the mandate's start) and runs the notify cron.
func (h *harness) firstCycleNotified(mandateID uuid.UUID) *models.AutopayChargeAttempt {
	h.t.Helper()
	m := h.mandate(mandateID)
	h.advanceTo(m.NextChargeDueAt.Add(time.Minute))
	if err := h.svc.NotifyNewCycles(context.Background()); err != nil {
		h.t.Fatalf("NotifyNewCycles: %v", err)
	}
	return h.latestAttempt(mandateID)
}

// readyToDebit delivers the notification 27h before the debit run and moves the clock there.
func (h *harness) readyToDebit(a *models.AutopayChargeAttempt) {
	h.t.Helper()
	delivered := h.now().Add(-time.Minute)
	if err := h.webhook("delivered-"+a.MerchantPaymentID, gateway.Event{
		Type: gateway.EventPreDebitNotificationDelivered, MerchantPaymentID: a.MerchantPaymentID, DeliveredAt: delivered,
	}); err != nil {
		h.t.Fatal(err)
	}
	h.advanceTo(delivered.Add(27 * time.Hour))
}

func (h *harness) debitRun() {
	h.t.Helper()
	if err := h.svc.DebitNotifiedAttempts(context.Background()); err != nil {
		h.t.Fatalf("DebitNotifiedAttempts: %v", err)
	}
}

func transient() error {
	return gateway.NewError(gateway.ErrorKindTransient, "503", "service unavailable", nil)
}

// ---- scenarios ----

func TestRecurringLifecycleEndToEnd(t *testing.T) {
	h := newHarness(t)
	id := h.activeMandate()
	m := h.mandate(id)
	if m.NextChargeDueAt == nil || !m.NextChargeDueAt.Equal(*m.StartAt) {
		t.Fatalf("first billing date must be the mandate start: %v vs %v", m.NextChargeDueAt, m.StartAt)
	}
	if active, _ := h.svc.Status(h.user.ID, h.cfg.AppName); active == nil || !active.UsedFreeTrial {
		t.Fatal("completing the authorization uses the free trial")
	}

	a := h.firstCycleNotified(id)
	if a.Status != models.ChargeAttemptStatusAwaitingNotification || a.PreDebitNotificationSentAt == nil || a.PreDebitNotificationSendAttempts != 1 {
		t.Fatalf("after the notify cron: %+v", a)
	}
	if h.gw.count("sendNotification") != 1 {
		t.Fatalf("notification sent %d times", h.gw.count("sendNotification"))
	}

	// not yet delivered: nothing may be debited even when the cycle is due
	h.advanceTo(h.now().Add(27 * time.Hour))
	h.debitRun()
	if h.gw.count("submitDebit") != 0 {
		t.Fatal("an undelivered notification must never be debited")
	}

	h.readyToDebit(a)
	h.debitRun()
	if got := h.latestAttempt(id); got.Status != models.ChargeAttemptStatusDebitSubmitted || got.DebitSubmitAttempts != 1 {
		t.Fatalf("after debit run: %+v", got)
	}
	h.debitRun() // the 3:45 run must NOT re-submit a debit that is with the bank
	if h.gw.count("submitDebit") != 1 {
		t.Fatalf("a submitted debit was submitted %d times", h.gw.count("submitDebit"))
	}

	if err := h.webhook("charged-1", gateway.Event{Type: gateway.EventAutopayChargeSucceeded, MerchantPaymentID: a.MerchantPaymentID, GatewayPaymentID: "pay_1"}); err != nil {
		t.Fatal(err)
	}
	got := h.latestAttempt(id)
	cycle := h.cycleOf(got)
	if got.Status != models.ChargeAttemptStatusDebitSucceeded || cycle.Status != models.BillingCycleStatusPaid {
		t.Fatalf("attempt=%s cycle=%s", got.Status, cycle.Status)
	}
	after := h.mandate(id)
	if want := m.NextChargeDueAt.AddDate(0, 1, 0); !after.NextChargeDueAt.Equal(want) || after.LastSuccessfulChargeAt == nil {
		t.Fatalf("next charge %v, want %v", after.NextChargeDueAt, want)
	}

	// a duplicate delivery of the same event is acknowledged and changes nothing
	before := h.mandate(id).UpdatedAt
	if err := h.webhook("charged-1", gateway.Event{Type: gateway.EventAutopayChargeSucceeded, MerchantPaymentID: a.MerchantPaymentID}); err != nil {
		t.Fatal(err)
	}
	if !h.mandate(id).UpdatedAt.Equal(before) {
		t.Fatal("a duplicate webhook must not touch the rows")
	}
	var rows int64
	h.db.Model(&models.AutopayWebhookEvent{}).Where("gateway_config_id = ? AND event_dedupe_key = ?", h.cfg.ID, "charged-1").Count(&rows)
	if rows != 1 {
		t.Fatalf("duplicate delivery stored %d rows", rows)
	}
}

func TestGatewayUnavailableDoesNotUseUpAnAttemptOrReduceTheAmount(t *testing.T) {
	h := newHarness(t)
	id := h.activeMandate()
	a := h.firstCycleNotified(id)
	h.readyToDebit(a)

	h.gw.submitDebit = func(gateway.DebitRequest) (*gateway.DebitSubmission, error) { return nil, transient() }
	h.debitRun() // 3:30: 3 HTTP tries, then released (limit 2 releases)
	mid := h.latestAttempt(id)
	if mid.Status != models.ChargeAttemptStatusNotificationDelivered || mid.DebitGatewayUnavailableRetryCount != 1 {
		t.Fatalf("after the first run the claim must be released: %+v", mid)
	}
	if h.gw.count("submitDebit") != 3 {
		t.Fatalf("a run makes 3 tries, got %d", h.gw.count("submitDebit"))
	}
	h.debitRun() // 3:45: second release limit reached -> fails as gateway_unavailable without counting
	end := h.latestAttempt(id)
	if end.Status != models.ChargeAttemptStatusDebitFailed || end.CountsTowardAttemptLimit || end.FailureCategory == nil || *end.FailureCategory != models.FailureCategoryGatewayUnavailable {
		t.Fatalf("final attempt: %+v", end)
	}
	cycle := h.cycleOf(end)
	if cycle.CountedChargeAttempts != 0 || cycle.Status != models.BillingCycleStatusAwaitingPayment {
		t.Fatalf("a gateway outage must not use up an attempt: counted=%d status=%s", cycle.CountedChargeAttempts, cycle.Status)
	}

	// the replacement is created by the next notify run, with the SAME number and the SAME (unreduced) amount
	h.gw.sendNotification = nil
	if err := h.svc.NotifyRetryAttempts(context.Background()); err != nil {
		t.Fatal(err)
	}
	repl := h.latestAttempt(id)
	if repl.ID == end.ID || repl.AttemptNumber != 1 || repl.Amount != 29900 || repl.MerchantPaymentID == end.MerchantPaymentID {
		t.Fatalf("replacement attempt wrong: number=%d amount=%d id=%s (old %s)", repl.AttemptNumber, repl.Amount, repl.MerchantPaymentID, end.MerchantPaymentID)
	}
}

func TestUnknownOutcomeIsVerifiedBeforeDeciding(t *testing.T) {
	// the debit DID go through: the gateway has it, so it is applied and no replacement is created
	h := newHarness(t)
	id := h.activeMandate()
	a := h.firstCycleNotified(id)
	h.readyToDebit(a)
	h.gw.submitDebit = func(gateway.DebitRequest) (*gateway.DebitSubmission, error) {
		return nil, gateway.NewError(gateway.ErrorKindOutcomeUnknown, "timeout", "no response", errors.New("i/o timeout"))
	}
	h.gw.fetchAttempt = func(gateway.ChargeAttemptRef) (*gateway.ChargeAttemptStatus, error) {
		return &gateway.ChargeAttemptStatus{DebitExists: true, Debit: gateway.PaymentSucceeded, GatewayPaymentID: "pay_x"}, nil
	}
	h.debitRun()
	if got := h.latestAttempt(id); got.Status != models.ChargeAttemptStatusDebitSucceeded || got.ID != a.ID {
		t.Fatalf("a debit the gateway has must be applied: %+v", got)
	}

	// the debit never happened: it is released (not failed, not counted) so the 3:45 run retries it
	h2 := newHarness(t)
	id2 := h2.activeMandate()
	a2 := h2.firstCycleNotified(id2)
	h2.readyToDebit(a2)
	h2.gw.submitDebit = h.gw.submitDebit
	h2.gw.fetchAttempt = func(gateway.ChargeAttemptRef) (*gateway.ChargeAttemptStatus, error) {
		return &gateway.ChargeAttemptStatus{DebitExists: false}, nil
	}
	h2.debitRun()
	got := h2.latestAttempt(id2)
	if got.Status != models.ChargeAttemptStatusNotificationDelivered || got.DebitGatewayUnavailableRetryCount != 1 || h2.cycleOf(got).CountedChargeAttempts != 1 {
		t.Fatalf("no record at the gateway must release the claim without counting: %+v", got)
	}

	// the verification itself fails: leave it debit_submitted for the sync run, never re-submit
	h3 := newHarness(t)
	id3 := h3.activeMandate()
	a3 := h3.firstCycleNotified(id3)
	h3.readyToDebit(a3)
	h3.gw.submitDebit = h.gw.submitDebit
	h3.gw.fetchAttempt = func(gateway.ChargeAttemptRef) (*gateway.ChargeAttemptStatus, error) { return nil, transient() }
	h3.debitRun()
	if got := h3.latestAttempt(id3); got.Status != models.ChargeAttemptStatusDebitSubmitted {
		t.Fatalf("an unverifiable outcome must stay debit_submitted, got %s", got.Status)
	}
	h3.debitRun()
	if h3.gw.count("submitDebit") != 3 {
		t.Fatalf("the unverified debit must not be submitted again, got %d submissions", h3.gw.count("submitDebit"))
	}
}

func TestPermanentRejectionCountsAndSchedulesTheReducedRetry(t *testing.T) {
	h := newHarness(t)
	id := h.activeMandate()
	a := h.firstCycleNotified(id)
	h.readyToDebit(a)
	h.gw.submitDebit = func(gateway.DebitRequest) (*gateway.DebitSubmission, error) {
		return nil, gateway.NewError(gateway.ErrorKindRejectedPermanently, "INSUFFICIENT_FUNDS", "insufficient funds", nil)
	}
	h.debitRun()
	failed := h.latestAttempt(id)
	cycle := h.cycleOf(failed)
	if failed.Status != models.ChargeAttemptStatusDebitFailed || !failed.CountsTowardAttemptLimit || h.gw.count("submitDebit") != 1 {
		t.Fatalf("a rejection is final and not retried: %+v calls=%d", failed, h.gw.count("submitDebit"))
	}
	if want := cycle.StartAt.AddDate(0, 0, 3); cycle.NextChargeAttemptAt == nil || !cycle.NextChargeAttemptAt.Equal(want) {
		t.Fatalf("next attempt %v, want T+3 days %v", cycle.NextChargeAttemptAt, want)
	}

	h.advanceTo(*cycle.NextChargeAttemptAt)
	if err := h.svc.NotifyRetryAttempts(context.Background()); err != nil {
		t.Fatal(err)
	}
	retry := h.latestAttempt(id)
	if retry.AttemptNumber != 2 || retry.Amount != 27400 || retry.ID == failed.ID {
		t.Fatalf("retry: number=%d amount=%d (want 2 / 27400 = Rs 25 less)", retry.AttemptNumber, retry.Amount)
	}
	if retry.PreDebitNotificationSentAt == nil {
		t.Fatal("the retry attempt must have its own pre-debit notification")
	}
}

func TestRevokedMandateIsCancelledNotRetried(t *testing.T) {
	h := newHarness(t)
	id := h.activeMandate()
	a := h.firstCycleNotified(id)
	h.readyToDebit(a)
	h.gw.submitDebit = func(gateway.DebitRequest) (*gateway.DebitSubmission, error) {
		return nil, gateway.NewError(gateway.ErrorKindMandateInvalid, "MANDATE_REVOKED", "mandate revoked", nil)
	}
	h.debitRun()
	if m := h.mandate(id); m.Status != models.MandateStatusCancelled || m.NextChargeDueAt != nil {
		t.Fatalf("mandate = %s", m.Status)
	}
}

func TestNotificationSendFailureIsResentBySyncThenFailsIntoTheLadder(t *testing.T) {
	h := newHarness(t)
	id := h.activeMandate()
	h.gw.sendNotification = func(gateway.PreDebitNotificationRequest) (*gateway.PreDebitNotificationResult, error) {
		return nil, transient()
	}
	a := h.firstCycleNotified(id) // send 1 fails after 3 tries
	if a.Status != models.ChargeAttemptStatusAwaitingNotification || a.PreDebitNotificationSentAt != nil || a.PreDebitNotificationSendAttempts != 1 {
		t.Fatalf("after the failed send: %+v", a)
	}

	h.advanceTo(h.now().Add(24 * time.Hour))
	if err := h.svc.SyncFromGateway(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := h.latestAttempt(id); got.PreDebitNotificationSendAttempts != 2 {
		t.Fatalf("the sync run must re-send once on the SAME attempt: %+v", got)
	}

	h.advanceTo(h.now().Add(24 * time.Hour))
	if err := h.svc.SyncFromGateway(context.Background()); err != nil {
		t.Fatal(err)
	}
	final := h.latestAttempt(id)
	if final.Status != models.ChargeAttemptStatusDebitFailed || final.FailureCategory == nil || *final.FailureCategory != models.FailureCategoryNotificationFailed {
		t.Fatalf("with both sends used up the attempt must fail into the retry ladder: %+v", final)
	}
	if h.cycleOf(final).NextChargeAttemptAt == nil {
		t.Fatal("a retry must be scheduled")
	}
}

func TestSyncSettlesWhatWebhooksMissed(t *testing.T) {
	h := newHarness(t)
	id := h.activeMandate()
	a := h.firstCycleNotified(id)

	// notification outcome missed: the sync run fetches it
	h.gw.fetchNotification = func(gateway.ChargeAttemptRef) (*gateway.PreDebitNotificationStatus, error) {
		return &gateway.PreDebitNotificationStatus{State: gateway.NotificationDelivered, DeliveredAt: h.now().Add(-27 * time.Hour)}, nil
	}
	h.advanceTo(h.now().Add(24 * time.Hour))
	if err := h.svc.SyncFromGateway(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := h.latestAttempt(id); got.Status != models.ChargeAttemptStatusNotificationDelivered || got.PreDebitNotificationDeliveredAt == nil {
		t.Fatalf("sync must confirm the notification: %+v", got)
	}

	// debit result missed
	h.debitRun()
	h.gw.fetchAttempt = func(gateway.ChargeAttemptRef) (*gateway.ChargeAttemptStatus, error) {
		return &gateway.ChargeAttemptStatus{DebitExists: true, Debit: gateway.PaymentSucceeded, GatewayPaymentID: "pay_late"}, nil
	}
	h.advanceTo(h.now().Add(time.Hour))
	h.db.Exec(`UPDATE autopay_charge_attempts SET updated_at = now() - interval '1 hour' WHERE id = ?`, a.ID)
	h.advanceTo(h.now().Add(time.Hour))
	if err := h.svc.SyncFromGateway(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := h.latestAttempt(id); got.Status != models.ChargeAttemptStatusDebitSucceeded {
		t.Fatalf("sync must settle a debit the webhook missed: %s", got.Status)
	}
}

func TestWebhookRulesSignatureUnknownEntityAndOutOfOrder(t *testing.T) {
	h := newHarness(t)
	id := h.activeMandate()
	a := h.firstCycleNotified(id)

	// a bad signature is rejected and nothing is stored
	h.gw.parseWebhook = func(http.Header, []byte) ([]gateway.Event, error) { return nil, gateway.ErrInvalidSignature }
	if err := h.svc.HandleWebhook(context.Background(), h.cfg.ID, http.Header{}, []byte("forged")); !errors.Is(err, ErrInvalidWebhookSignature) {
		t.Fatalf("err = %v", err)
	}
	var rows int64
	h.db.Model(&models.AutopayWebhookEvent{}).Where("gateway_config_id = ?", h.cfg.ID).Count(&rows)
	if rows != 0 {
		t.Fatal("an unauthenticated webhook must not be stored")
	}
	// an unknown config is a 404
	if err := h.svc.HandleWebhook(context.Background(), uuid.New(), http.Header{}, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown config: %v", err)
	}

	// an event for an entity we do not own is acknowledged and marked ignored
	if err := h.webhook("unknown-1", gateway.Event{Type: gateway.EventAutopayChargeSucceeded, MerchantPaymentID: "pay_not_ours"}); err != nil {
		t.Fatal(err)
	}
	var statuses []string
	h.db.Model(&models.AutopayWebhookEvent{}).Where("gateway_config_id = ? AND event_dedupe_key = ?", h.cfg.ID, "unknown-1").Pluck("status", &statuses)
	if len(statuses) != 1 || statuses[0] != string(models.WebhookEventStatusIgnoredUnknownEntity) {
		t.Fatalf("statuses = %v", statuses)
	}

	// out of order: success first, then a stale "failed" for the same attempt must not regress it
	if err := h.webhook("ok", gateway.Event{Type: gateway.EventAutopayChargeSucceeded, MerchantPaymentID: a.MerchantPaymentID, GatewayEventAt: h.now()}); err != nil {
		t.Fatal(err)
	}
	if err := h.webhook("late-fail", gateway.Event{Type: gateway.EventAutopayChargeFailed, MerchantPaymentID: a.MerchantPaymentID, GatewayEventAt: h.now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if got := h.latestAttempt(id); got.Status != models.ChargeAttemptStatusDebitSucceeded {
		t.Fatalf("a stale failure regressed a succeeded attempt: %s", got.Status)
	}
}

func TestMandateStatusWebhookPausesAndCancels(t *testing.T) {
	h := newHarness(t)
	id := h.activeMandate()
	ref := gateway.Event{Type: gateway.EventMandateStatusChanged, MerchantAutopayID: h.mandate(id).MerchantAutopayID}
	paused := ref
	paused.MandateState = gateway.MandatePaused
	if err := h.webhook("pause", paused); err != nil {
		t.Fatal(err)
	}
	if h.mandate(id).Status != models.MandateStatusPaused {
		t.Fatal("pause not applied")
	}
	cancelled := ref
	cancelled.MandateState = gateway.MandateCancelled
	if err := h.webhook("cancel", cancelled); err != nil {
		t.Fatal(err)
	}
	if m := h.mandate(id); m.Status != models.MandateStatusCancelled || m.NextChargeDueAt != nil {
		t.Fatalf("cancel not applied: %s", m.Status)
	}
}

func TestCreateValidationAndGuards(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	base := CreateRequest{
		UserID: h.user.ID, AppName: h.cfg.AppName, Gateway: "test", PaymentType: models.PaymentTypeRecurring,
		AuthorizationAmount: 100, MaxDebitAmount: 29900, StartAt: h.now().Add(72 * time.Hour), BillingFrequency: "monthly",
	}
	cases := map[string]func(*CreateRequest){
		"unknown gateway":         func(r *CreateRequest) { r.Gateway = "nope" },
		"as_presented frequency":  func(r *CreateRequest) { r.BillingFrequency = "as_presented" },
		"start too soon":          func(r *CreateRequest) { r.StartAt = h.now().Add(time.Hour) },
		"recurring without start": func(r *CreateRequest) { r.StartAt = time.Time{} },
		"amount below minimum":    func(r *CreateRequest) { r.AuthorizationAmount = 50 },
		"max below authorization": func(r *CreateRequest) { r.MaxDebitAmount = 50 },
		"bad currency":            func(r *CreateRequest) { r.Currency = "rupees" },
	}
	for name, mutate := range cases {
		req := base
		mutate(&req)
		if _, err := h.svc.Create(ctx, req); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("%s: err = %v, want ErrInvalidRequest", name, err)
		}
	}
	if h.gw.count("createMandate") != 0 {
		t.Fatal("validation must fail before the gateway is called")
	}

	// kill switch: accepts_new_autopay = false rejects NEW autopays
	h.db.Model(&models.PaymentGatewayConfig{}).Where("id = ?", h.cfg.ID).Update("accepts_new_autopay", false)
	if _, err := h.svc.Create(ctx, base); !errors.Is(err, ErrGatewayNotAvailable) {
		t.Fatalf("kill switch: %v", err)
	}
	h.db.Model(&models.PaymentGatewayConfig{}).Where("id = ?", h.cfg.ID).Update("accepts_new_autopay", true)

	// a user may hold several mandates: a second create is not refused
	second, err := h.svc.Create(ctx, base)
	if err != nil {
		t.Fatalf("second mandate for the same user: %v", err)
	}
	// every mandate is sent to the gateway (and stored) with a 10-year expiry counted from start_at
	if end := h.mandate(second.AutopayMandateID).EndAt; end == nil || !end.Equal(base.StartAt.AddDate(10, 0, 0).UTC()) {
		t.Fatalf("mandate end_at = %v, want start_at + 10 years", end)
	}
	var existing int64
	h.db.Model(&models.AutopayMandate{}).Where("gateway_config_id = ?", h.cfg.ID).Count(&existing)

	// a gateway outage while creating is reported as unavailable and leaves no rows behind
	h.gw.createMandate = func(gateway.MandateAuthorizationRequest) (*gateway.MandateAuthorizationSession, error) {
		return nil, transient()
	}
	if _, err := h.svc.Create(ctx, base); !errors.Is(err, ErrGatewayUnavailable) {
		t.Fatalf("outage: %v", err)
	}
	var n int64
	h.db.Model(&models.AutopayMandate{}).Where("gateway_config_id = ?", h.cfg.ID).Count(&n)
	if n != existing {
		t.Fatalf("a failed create left %d new rows", n-existing)
	}
}

func TestOneTimePaymentVerifyAndCancelRules(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	res, err := h.svc.Create(ctx, CreateRequest{
		UserID: h.user.ID, AppName: h.cfg.AppName, Gateway: "test", PaymentType: models.PaymentTypeOneTime,
		Amount: 9900, StartAt: h.now().Add(30 * 24 * time.Hour), BillingFrequency: "monthly", // a client-sent start_at is ignored
	})
	if err != nil {
		t.Fatal(err)
	}
	if m := h.mandate(res.AutopayMandateID); m.StartAt == nil || !m.StartAt.Equal(h.now().UTC()) {
		t.Fatalf("a one-time payment starts at the server's now, got start_at=%v want %v", m.StartAt, h.now().UTC())
	}
	if _, err := h.svc.Cancel(ctx, res.AutopayMandateID); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("a one-time payment cannot be cancelled: %v", err)
	}
	view, err := h.svc.Verify(ctx, res.AutopayMandateID)
	if err != nil || view.Status != models.MandateStatusActive {
		t.Fatalf("verify: %+v %v", view, err)
	}
	m := h.mandate(res.AutopayMandateID)
	if m.NextChargeDueAt != nil || m.LastSuccessfulChargeAt == nil {
		t.Fatal("a paid one-time payment has no next charge date")
	}
	if active, _ := h.svc.Status(h.user.ID, h.cfg.AppName); !active.ActiveSubscription {
		t.Fatal("a paid one-time payment gives access for its window")
	}
}

type failingUsers struct{}

func (failingUsers) FindByID(uuid.UUID) (*userModels.User, error) {
	return nil, errors.New("users db down")
}

func TestFailedUserLookupDoesNotBurnANotificationSendOrStrandADebit(t *testing.T) {
	h := newHarness(t)
	id := h.activeMandate()
	h.svc.users = failingUsers{}

	m := h.mandate(id)
	h.advanceTo(m.NextChargeDueAt.Add(time.Minute))
	if err := h.svc.NotifyNewCycles(context.Background()); err != nil {
		t.Fatal(err)
	}
	a := h.latestAttempt(id)
	if a.PreDebitNotificationSendAttempts != 0 || h.gw.count("sendNotification") != 0 {
		t.Fatalf("no call was made, so no send may be used up: sends=%d calls=%d", a.PreDebitNotificationSendAttempts, h.gw.count("sendNotification"))
	}

	// same for the debit: the claim must not happen before the call can be made
	h.svc.users = testUsers{h.user}
	if err := h.svc.NotifyRetryAttempts(context.Background()); err != nil { // sends the notification now that users work
		t.Fatal(err)
	}
	a = h.latestAttempt(id)
	h.readyToDebit(a)
	h.svc.users = failingUsers{}
	h.debitRun()
	after := h.latestAttempt(id)
	if after.Status != models.ChargeAttemptStatusNotificationDelivered || after.DebitSubmitAttempts != 0 || h.gw.count("submitDebit") != 0 {
		t.Fatalf("an attempt must not be claimed (debit_submitted) when the debit cannot be made: %+v", after)
	}
}

func TestUnknownDebitOutcomeOnAGatewayWithoutIdempotencyKeysIsVerifiedNotRepeated(t *testing.T) {
	h := newHarness(t)
	h.gw.caps.SupportsIdempotencyKeys = false
	id := h.activeMandate()
	a := h.firstCycleNotified(id)
	h.readyToDebit(a)
	h.gw.submitDebit = func(gateway.DebitRequest) (*gateway.DebitSubmission, error) {
		return nil, gateway.NewError(gateway.ErrorKindOutcomeUnknown, "timeout", "no response", errors.New("i/o timeout"))
	}
	h.gw.fetchAttempt = func(gateway.ChargeAttemptRef) (*gateway.ChargeAttemptStatus, error) {
		return &gateway.ChargeAttemptStatus{DebitExists: true, Debit: gateway.PaymentSucceeded, Notification: gateway.NotificationDelivered}, nil
	}
	h.debitRun()
	if n := h.gw.count("submitDebit"); n != 1 {
		t.Fatalf("a debit that may have gone through must not be submitted again, got %d submissions", n)
	}
	if got := h.latestAttempt(id); got.Status != models.ChargeAttemptStatusDebitSucceeded {
		t.Fatalf("the verification found the debit: %+v", got)
	}
}

func TestOrderPerAttemptGatewayNeverDebitsWithoutAnOrder(t *testing.T) {
	h := newHarness(t)
	h.gw.caps.ChargeAttemptIdentity = gateway.OrderPerChargeAttempt
	id := h.activeMandate()
	a := h.firstCycleNotified(id) // the fake's notification result carries no order id
	h.readyToDebit(a)
	h.debitRun()
	if h.gw.count("submitDebit") != 0 {
		t.Fatal("a debit without the gateway order of its notification cannot be matched and must not be sent")
	}
	if got := h.latestAttempt(id); got.Status != models.ChargeAttemptStatusNotificationDelivered || got.DebitSubmitAttempts != 0 {
		t.Fatalf("the attempt must stay unclaimed: %+v", got)
	}

	// once the order id is known the debit goes ahead
	orderID := "order_x"
	if err := h.repo.UpdateChargeAttemptFields(a.ID, map[string]interface{}{"gateway_order_id": orderID}); err != nil {
		t.Fatal(err)
	}
	h.debitRun()
	if h.gw.count("submitDebit") != 1 {
		t.Fatalf("with an order id the debit is sent, got %d", h.gw.count("submitDebit"))
	}
}

func TestNotificationWithoutAnOutcomeForTooLongFailsIntoTheRetryLadder(t *testing.T) {
	h := newHarness(t)
	id := h.activeMandate()
	a := h.firstCycleNotified(id) // sent; the gateway keeps answering "pending"

	h.advanceTo(h.now().Add(24 * time.Hour))
	if err := h.svc.SyncFromGateway(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := h.latestAttempt(id); got.ID != a.ID || got.Status != models.ChargeAttemptStatusAwaitingNotification {
		t.Fatalf("a day later it is still waiting: %+v", got)
	}

	h.advanceTo(h.now().Add(30 * time.Hour)) // > 48h after the send
	if err := h.svc.SyncFromGateway(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := h.latestAttempt(id)
	if got.Status != models.ChargeAttemptStatusDebitFailed || got.FailureCategory == nil || *got.FailureCategory != models.FailureCategoryNotificationFailed || got.FailureCode != "notification_outcome_timeout" {
		t.Fatalf("an attempt whose notification never resolves must fail, not block the cycle forever: %+v", got)
	}
	if c := h.cycleOf(got); c.NextChargeAttemptAt == nil || c.Status != models.BillingCycleStatusAwaitingPayment {
		t.Fatalf("the cycle must be scheduled for a retry: %+v", c)
	}
}

func TestLateSendResultDoesNotOverwriteWhatAWebhookAlreadySettled(t *testing.T) {
	h := newHarness(t)
	id := h.activeMandate()
	a := h.firstCycleNotified(id)
	h.readyToDebit(a) // the delivered webhook won the race against the send call's bookkeeping

	if err := h.svc.recordNotificationSent(a.ID, &gateway.PreDebitNotificationResult{State: gateway.NotificationPending}); err != nil {
		t.Fatal(err)
	}
	got := h.latestAttempt(id)
	if got.Status != models.ChargeAttemptStatusNotificationDelivered || got.PreDebitNotificationStatus == nil || *got.PreDebitNotificationStatus != "delivered" {
		t.Fatalf("a late 'pending' must not undo 'delivered': %+v", got)
	}

	// same for the debit: a webhook settled it before the submit bookkeeping ran
	h.debitRun()
	if err := h.webhook("ok-"+a.MerchantPaymentID, gateway.Event{Type: gateway.EventAutopayChargeSucceeded, MerchantPaymentID: a.MerchantPaymentID}); err != nil {
		t.Fatal(err)
	}
	if err := h.repo.UpdateChargeAttemptFields(a.ID, map[string]interface{}{"debit_execution_status": "succeeded"}); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.recordDebitSubmitted(a.ID, &gateway.DebitSubmission{State: gateway.PaymentPending}); err != nil {
		t.Fatal(err)
	}
	got = h.latestAttempt(id)
	if got.Status != models.ChargeAttemptStatusDebitSucceeded || got.DebitExecutionStatus == nil || *got.DebitExecutionStatus != "succeeded" {
		t.Fatalf("a late 'pending' must not undo the settled debit: %+v", got)
	}
}

func TestPanickingTransitionReleasesItsRowLocks(t *testing.T) {
	h := newHarness(t)
	id := h.activeMandate()
	a := h.firstCycleNotified(id)

	func() {
		defer func() { _ = recover() }() // what the batch runner does
		_ = h.svc.mutateAttempt(a.ID, func(State) Effects { panic("boom") })
	}()

	done := make(chan error, 1)
	go func() { done <- h.svc.mutateAttempt(a.ID, func(State) Effects { return Effects{} }) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the panicked transaction still holds its row locks")
	}
}

func TestCancellingAnAlreadyCancelledMandateStillCancelsLocally(t *testing.T) {
	h := newHarness(t)
	id := h.activeMandate()
	// the gateway says the mandate is already gone (the adapters report this as already-processed)
	h.gw.cancelErr = gateway.NewError(gateway.ErrorKindAlreadyProcessed, "already_cancelled", "already cancelled", nil)
	view, err := h.svc.Cancel(context.Background(), id)
	if err != nil || view.Status != models.MandateStatusCancelled {
		t.Fatalf("cancel of an already cancelled mandate must succeed locally: %+v %v", view, err)
	}
	// a real rejection is still an error and leaves the mandate alone
	id2 := h.activeMandate()
	h.gw.cancelErr = gateway.NewError(gateway.ErrorKindRejectedPermanently, "x", "cannot", nil)
	if _, err := h.svc.Cancel(context.Background(), id2); err == nil {
		t.Fatal("a rejected cancel must fail")
	}
	if m := h.mandate(id2); m.Status != models.MandateStatusActive {
		t.Fatalf("a failed cancel must not change the mandate: %s", m.Status)
	}
}
