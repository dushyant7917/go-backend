package models

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Round-trips every model through the real schema, so a mismatch between the structs and the migration (column
// names, defaults, constraints) fails here. Run with AUTOPAY_IT_DSN pointing at a database with the autopay
// migration applied (see ratelimit/limiter_test.go); skipped otherwise.

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

func mustCreate(t *testing.T, db *gorm.DB, v any) {
	t.Helper()
	if err := db.Create(v).Error; err != nil {
		t.Fatalf("create %T: %v", v, err)
	}
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

func newFixture(t *testing.T, db *gorm.DB) (PaymentGatewayConfig, AutopayMandate, AutopayBillingCycle) {
	t.Helper()
	cfg := PaymentGatewayConfig{Gateway: "test", AppName: "app-" + uuid.NewString(), Environment: "test", IsActive: true, AcceptsNewAutopay: true}
	mustCreate(t, db, &cfg)
	t.Cleanup(func() {
		db.Exec(`DELETE FROM autopay_webhook_events WHERE gateway_config_id = ?`, cfg.ID)
		db.Exec(`DELETE FROM autopay_mandates WHERE gateway_config_id = ?`, cfg.ID) // cascades to cycles and attempts
		db.Unscoped().Delete(&PaymentGatewayConfig{}, "id = ?", cfg.ID)
	})

	now := time.Now().UTC()
	mandate := AutopayMandate{
		UserID: newUser(t, db), AppName: cfg.AppName, GatewayConfigID: cfg.ID, PaymentType: PaymentTypeRecurring,
		MerchantAutopayID: "m-" + uuid.NewString(), MaxDebitAmount: 29900, BillingFrequency: "monthly", StartAt: &now,
	}
	mustCreate(t, db, &mandate)

	cycle := AutopayBillingCycle{AutopayMandateID: mandate.ID, CycleNumber: 1, StartAt: now, Amount: 29900}
	mustCreate(t, db, &cycle)
	return cfg, mandate, cycle
}

func TestModelsRoundTripWithDefaults(t *testing.T) {
	db := integrationDB(t)
	cfg, mandate, cycle := newFixture(t, db)

	var gotCfg PaymentGatewayConfig
	if err := db.First(&gotCfg, "id = ?", cfg.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !gotCfg.IsActive || !gotCfg.AcceptsNewAutopay || gotCfg.GatewayRatelimitRPM != nil {
		t.Errorf("config defaults wrong: %+v", gotCfg)
	}

	var gotMandate AutopayMandate
	if err := db.First(&gotMandate, "id = ?", mandate.ID).Error; err != nil {
		t.Fatal(err)
	}
	if gotMandate.Status != MandateStatusAwaitingAuthorization || gotMandate.Currency != "INR" || gotMandate.IsOneTime() {
		t.Errorf("mandate defaults wrong: status=%s currency=%s", gotMandate.Status, gotMandate.Currency)
	}

	var gotCycle AutopayBillingCycle
	if err := db.First(&gotCycle, "id = ?", cycle.ID).Error; err != nil {
		t.Fatal(err)
	}
	if gotCycle.Status != BillingCycleStatusAwaitingPayment || gotCycle.CountedChargeAttempts != 0 {
		t.Errorf("cycle defaults wrong: %+v", gotCycle)
	}

	attempt := AutopayChargeAttempt{
		BillingCycleID: cycle.ID, GatewayConfigID: cfg.ID, AttemptNumber: 1, CountsTowardAttemptLimit: true,
		MerchantPaymentID: "p-" + uuid.NewString(), Amount: 29900,
	}
	mustCreate(t, db, &attempt)
	var gotAttempt AutopayChargeAttempt
	if err := db.First(&gotAttempt, "id = ?", attempt.ID).Error; err != nil {
		t.Fatal(err)
	}
	if gotAttempt.Status != ChargeAttemptStatusAwaitingNotification || !gotAttempt.CountsTowardAttemptLimit ||
		gotAttempt.PreDebitNotificationSendAttempts != 0 || gotAttempt.GatewayOrderID != nil {
		t.Errorf("attempt defaults wrong: %+v", gotAttempt)
	}

	event := AutopayWebhookEvent{GatewayConfigID: cfg.ID, EventDedupeKey: "evt-" + uuid.NewString(), RawBody: `{"event":"x"}`}
	mustCreate(t, db, &event)
	var gotEvent AutopayWebhookEvent
	if err := db.First(&gotEvent, "id = ?", event.ID).Error; err != nil {
		t.Fatal(err)
	}
	if gotEvent.Status != WebhookEventStatusReceived || gotEvent.ReceivedAt.IsZero() {
		t.Errorf("event defaults wrong: %+v", gotEvent)
	}
}

func TestConstraintsRejectBadData(t *testing.T) {
	db := integrationDB(t)
	cfg, mandate, cycle := newFixture(t, db)

	expectViolation := func(name string, err error, constraint string) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), constraint) {
			t.Errorf("%s: want violation of %q, got %v", name, constraint, err)
		}
	}

	bad := mandate
	bad.ID, bad.MerchantAutopayID, bad.Status = uuid.Nil, "m-"+uuid.NewString(), MandateStatus("bogus")
	expectViolation("bad mandate status", db.Create(&bad).Error, "ck_autopay_mandates_status_valid")

	bad = mandate
	bad.ID, bad.MerchantAutopayID, bad.Currency = uuid.Nil, "m-"+uuid.NewString(), "inr"
	expectViolation("lowercase currency", db.Create(&bad).Error, "ck_autopay_mandates_currency_format_valid")

	bad = mandate
	bad.ID, bad.MerchantAutopayID, bad.BillingFrequency = uuid.Nil, "m-"+uuid.NewString(), "as_presented"
	expectViolation("as_presented is not a billing frequency", db.Create(&bad).Error, "ck_autopay_mandates_billing_frequency_valid")

	dup := mandate
	dup.ID = uuid.Nil
	expectViolation("duplicate merchant_autopay_id", db.Create(&dup).Error, "uq_autopay_mandates_merchant_autopay_id")

	dupCycle := AutopayBillingCycle{AutopayMandateID: mandate.ID, CycleNumber: cycle.CycleNumber, StartAt: time.Now().UTC().Add(time.Hour), Amount: 100}
	expectViolation("duplicate cycle number", db.Create(&dupCycle).Error, "uq_autopay_billing_cycles_autopay_mandate_id_cycle_number")

	zero := AutopayBillingCycle{AutopayMandateID: mandate.ID, CycleNumber: 9, StartAt: time.Now().UTC().Add(2 * time.Hour), Amount: 0}
	expectViolation("zero cycle amount", db.Create(&zero).Error, "ck_autopay_billing_cycles_amount_positive")

	pid := "p-" + uuid.NewString()
	a1 := AutopayChargeAttempt{BillingCycleID: cycle.ID, GatewayConfigID: cfg.ID, AttemptNumber: 1, CountsTowardAttemptLimit: true, MerchantPaymentID: pid, Amount: 100}
	mustCreate(t, db, &a1)
	a2 := AutopayChargeAttempt{BillingCycleID: cycle.ID, GatewayConfigID: cfg.ID, AttemptNumber: 2, CountsTowardAttemptLimit: true, MerchantPaymentID: pid, Amount: 100}
	expectViolation("duplicate merchant_payment_id", db.Create(&a2).Error, "uq_autopay_charge_attempts_merchant_payment_id")

	// Two COUNTED attempts may not share a number...
	a3 := AutopayChargeAttempt{BillingCycleID: cycle.ID, GatewayConfigID: cfg.ID, AttemptNumber: 1, CountsTowardAttemptLimit: true, MerchantPaymentID: "p-" + uuid.NewString(), Amount: 100}
	expectViolation("duplicate counted attempt number", db.Create(&a3).Error, "uq_autopay_charge_attempts_cycle_attempt_number_counted")
	// ...but a non-counted (gateway_unavailable) attempt may reuse its number.
	a4 := AutopayChargeAttempt{BillingCycleID: cycle.ID, GatewayConfigID: cfg.ID, AttemptNumber: 1, CountsTowardAttemptLimit: false, MerchantPaymentID: "p-" + uuid.NewString(), Amount: 100}
	if err := db.Create(&a4).Error; err != nil {
		t.Errorf("a non-counted attempt must be allowed to reuse a number: %v", err)
	}
	var stored AutopayChargeAttempt
	if err := db.First(&stored, "id = ?", a4.ID).Error; err != nil || stored.CountsTowardAttemptLimit {
		t.Errorf("counts_toward_attempt_limit=false must be stored as false (err=%v, stored=%v)", err, stored.CountsTowardAttemptLimit)
	}

	// Gateway-issued ids are unique per merchant account.
	order := "order_" + uuid.NewString()
	b1 := AutopayChargeAttempt{BillingCycleID: cycle.ID, GatewayConfigID: cfg.ID, AttemptNumber: 5, CountsTowardAttemptLimit: true, MerchantPaymentID: "p-" + uuid.NewString(), Amount: 100, GatewayOrderID: &order}
	mustCreate(t, db, &b1)
	b2 := AutopayChargeAttempt{BillingCycleID: cycle.ID, GatewayConfigID: cfg.ID, AttemptNumber: 6, CountsTowardAttemptLimit: true, MerchantPaymentID: "p-" + uuid.NewString(), Amount: 100, GatewayOrderID: &order}
	expectViolation("duplicate gateway order id", db.Create(&b2).Error, "uq_autopay_charge_attempts_config_id_gateway_order_id")

	// Webhook dedupe: the same event for the same config is rejected.
	key := "evt-" + uuid.NewString()
	mustCreate(t, db, &AutopayWebhookEvent{GatewayConfigID: cfg.ID, EventDedupeKey: key, RawBody: "{}"})
	expectViolation("duplicate webhook event", db.Create(&AutopayWebhookEvent{GatewayConfigID: cfg.ID, EventDedupeKey: key, RawBody: "{}"}).Error,
		"uq_autopay_webhook_events_config_id_event_dedupe_key")
}

func TestOneTimeRowHasNoGatewayMandateIDAndSurvivesCascade(t *testing.T) {
	db := integrationDB(t)
	cfg, _, _ := newFixture(t, db)
	oneTime := AutopayMandate{
		UserID: newUser(t, db), AppName: cfg.AppName, GatewayConfigID: cfg.ID, PaymentType: PaymentTypeOneTime,
		MerchantAutopayID: "ot-" + uuid.NewString(), MaxDebitAmount: 9900, BillingFrequency: "monthly",
	}
	mustCreate(t, db, &oneTime)
	if !oneTime.IsOneTime() || oneTime.GatewayMandateID != nil || oneTime.NextChargeDueAt != nil {
		t.Fatalf("one-time row must have no mandate id and no next charge date: %+v", oneTime)
	}
}
