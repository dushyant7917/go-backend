package service

import (
	"strings"
	"testing"
	"time"

	"go-backend/internal/apps/autopay/gateway"
	"go-backend/internal/apps/autopay/models"

	"github.com/google/uuid"
)

var t0 = time.Date(2026, 11, 12, 1, 15, 0, 0, time.UTC)

func newEngine() *Engine {
	e := NewEngine()
	e.Now = func() time.Time { return t0 }
	return e
}

// newState builds an active monthly mandate with cycle 1 awaiting payment and its first attempt in the given status.
func newState(status models.ChargeAttemptStatus) State {
	due := t0.Add(-time.Hour)
	end := t0.Add(30 * 24 * time.Hour)
	m := &models.AutopayMandate{
		ID: uuid.New(), PaymentType: models.PaymentTypeRecurring, Status: models.MandateStatusActive,
		MaxDebitAmount: 29900, BillingFrequency: "monthly", NextChargeDueAt: &due, StartAt: &due,
	}
	c := &models.AutopayBillingCycle{
		ID: uuid.New(), AutopayMandateID: m.ID, CycleNumber: 1, StartAt: due, EndAt: &end, Amount: 29900,
		Status: models.BillingCycleStatusAwaitingPayment, CountedChargeAttempts: 1, NextChargeAttemptAt: &due,
	}
	sent := t0.Add(-27 * time.Hour)
	a := &models.AutopayChargeAttempt{
		ID: uuid.New(), BillingCycleID: c.ID, AttemptNumber: 1, CountsTowardAttemptLimit: true,
		MerchantPaymentID: "pay_x_a1", Status: status, Amount: 29900, PreDebitNotificationSentAt: &sent,
	}
	return State{Mandate: m, Cycle: c, Attempt: a}
}

// ---------- pre-debit notification ----------

func TestNotificationDeliveredUsesGatewayTimeThenSentTimeNeverNow(t *testing.T) {
	e := newEngine()

	s := newState(models.ChargeAttemptStatusAwaitingNotification)
	gw := t0.Add(-26*time.Hour - 10*time.Minute)
	if fx := e.OnPreDebitNotificationDelivered(s, gw); !fx.Changed || fx.Ignored != "" {
		t.Fatalf("fx = %+v", fx)
	}
	if s.Attempt.Status != models.ChargeAttemptStatusNotificationDelivered || !s.Attempt.PreDebitNotificationDeliveredAt.Equal(gw) {
		t.Fatalf("gateway time must be the gate clock: %+v", s.Attempt)
	}

	// no gateway time: fall back to when OUR send call returned (here 27h ago), not to "now"
	s = newState(models.ChargeAttemptStatusAwaitingNotification)
	e.OnPreDebitNotificationDelivered(s, time.Time{})
	if got := s.Attempt.PreDebitNotificationDeliveredAt; got == nil || !got.Equal(*s.Attempt.PreDebitNotificationSentAt) || got.Equal(t0) {
		t.Fatalf("fallback must be the send time, got %v", got)
	}

	// neither time is known: refuse to invent one
	s = newState(models.ChargeAttemptStatusAwaitingNotification)
	s.Attempt.PreDebitNotificationSentAt = nil
	if fx := e.OnPreDebitNotificationDelivered(s, time.Time{}); fx.Changed || fx.Ignored != "no_delivery_time" || s.Attempt.Status != models.ChargeAttemptStatusAwaitingNotification {
		t.Fatalf("fx = %+v status=%s", fx, s.Attempt.Status)
	}
}

func TestNotificationDeliveredIsIdempotentAndIgnoresStale(t *testing.T) {
	e := newEngine()
	s := newState(models.ChargeAttemptStatusAwaitingNotification)
	e.OnPreDebitNotificationDelivered(s, t0.Add(-27*time.Hour))
	if fx := e.OnPreDebitNotificationDelivered(s, t0.Add(-1*time.Hour)); fx.Changed || fx.Ignored != "duplicate" {
		t.Fatalf("second delivery must be a duplicate: %+v", fx)
	}
	if !s.Attempt.PreDebitNotificationDeliveredAt.Equal(t0.Add(-27 * time.Hour)) {
		t.Fatal("a duplicate must not move the gate clock")
	}

	for _, status := range []models.ChargeAttemptStatus{models.ChargeAttemptStatusDebitSubmitted, models.ChargeAttemptStatusDebitSucceeded, models.ChargeAttemptStatusDebitFailed} {
		s := newState(status)
		if fx := e.OnPreDebitNotificationDelivered(s, t0.Add(-27*time.Hour)); fx.Ignored != "stale" || s.Attempt.Status != status {
			t.Errorf("%s: a late delivery must be stale and not regress the status: %+v status=%s", status, fx, s.Attempt.Status)
		}
		// ... but it may fill a missing timestamp
		if s.Attempt.PreDebitNotificationDeliveredAt == nil {
			t.Errorf("%s: missing delivered_at should be recorded", status)
		}
	}
}

func TestNotificationFailureEntersTheRetryLadder(t *testing.T) {
	e := newEngine()
	s := newState(models.ChargeAttemptStatusAwaitingNotification)
	fx := e.OnPreDebitNotificationFailed(s, "PDN_REJECTED", "bank rejected the notification")
	if !fx.Changed || !fx.PaymentFailed {
		t.Fatalf("fx = %+v", fx)
	}
	if s.Attempt.Status != models.ChargeAttemptStatusDebitFailed || *s.Attempt.FailureCategory != models.FailureCategoryNotificationFailed {
		t.Fatalf("attempt = %+v", s.Attempt)
	}
	if want := e.Policy.NextRetryAt(s.Cycle.StartAt, 1); !s.Cycle.NextChargeAttemptAt.Equal(want) {
		t.Fatalf("next attempt %v, want T+3 days %v", s.Cycle.NextChargeAttemptAt, want)
	}
	// only an attempt still awaiting its notification can fail that way
	if fx := e.OnPreDebitNotificationFailed(newState(models.ChargeAttemptStatusNotificationDelivered), "x", ""); fx.Ignored != "stale" {
		t.Fatalf("fx = %+v", fx)
	}
}

// ---------- debit success ----------

func TestChargeSucceededSettlesCycleAndSchedulesNextMonth(t *testing.T) {
	e := newEngine()
	s := newState(models.ChargeAttemptStatusDebitSubmitted)
	prevDue := *s.Mandate.NextChargeDueAt
	fx := e.OnChargeSucceeded(s, false)
	if !fx.Changed || !fx.Charged || fx.Alert != "" || fx.LateSuccessCorrection {
		t.Fatalf("fx = %+v", fx)
	}
	if s.Attempt.Status != models.ChargeAttemptStatusDebitSucceeded || s.Cycle.Status != models.BillingCycleStatusPaid || s.Cycle.NextChargeAttemptAt != nil {
		t.Fatalf("rows not settled: attempt=%s cycle=%s", s.Attempt.Status, s.Cycle.Status)
	}
	if want := prevDue.AddDate(0, 1, 0); !s.Mandate.NextChargeDueAt.Equal(want) {
		t.Fatalf("next charge %v, want %v", s.Mandate.NextChargeDueAt, want)
	}
	if s.Mandate.LastSuccessfulChargeAt == nil || !s.Mandate.LastSuccessfulChargeAt.Equal(t0) {
		t.Fatal("last_successful_charge_at must be set")
	}
}

func TestChargeSucceededIsIdempotentAndAbsorbing(t *testing.T) {
	e := newEngine()
	s := newState(models.ChargeAttemptStatusDebitSubmitted)
	e.OnChargeSucceeded(s, false)
	next := *s.Mandate.NextChargeDueAt
	if fx := e.OnChargeSucceeded(s, false); fx.Changed || fx.Charged || fx.Ignored != "duplicate" {
		t.Fatalf("a second success must be a no-op: %+v", fx)
	}
	if !s.Mandate.NextChargeDueAt.Equal(next) {
		t.Fatal("a duplicate success must not advance the schedule twice")
	}
	// a failure arriving AFTER the success is stale: succeeded is absorbing
	if fx := e.OnChargeFailed(s, Failure{Category: models.FailureCategoryRejectedPermanently, Counts: true}); fx.Changed || fx.Ignored != "stale" {
		t.Fatalf("failure after success: %+v", fx)
	}
	if s.Attempt.Status != models.ChargeAttemptStatusDebitSucceeded {
		t.Fatal("a stale failure must not regress the attempt")
	}
}

func TestSuccessJumpsForwardFromAnyOpenState(t *testing.T) {
	e := newEngine()
	for _, status := range []models.ChargeAttemptStatus{
		models.ChargeAttemptStatusAwaitingNotification, models.ChargeAttemptStatusNotificationDelivered, models.ChargeAttemptStatusDebitSubmitted,
	} {
		s := newState(status)
		if fx := e.OnChargeSucceeded(s, false); !fx.Changed || !fx.Charged || s.Attempt.Status != models.ChargeAttemptStatusDebitSucceeded {
			t.Errorf("%s: a success must apply from any open state: %+v", status, fx)
		}
	}
}

func TestLateSuccessCorrectsAFailedAttempt(t *testing.T) {
	e := newEngine()
	s := newState(models.ChargeAttemptStatusDebitSubmitted)
	e.OnChargeFailed(s, Failure{Category: models.FailureCategoryRejectedPermanently, Code: "X", Counts: true})
	if s.Attempt.Status != models.ChargeAttemptStatusDebitFailed || s.Cycle.NextChargeAttemptAt == nil {
		t.Fatal("setup: attempt should have failed and scheduled a retry")
	}

	// the failed attempt later turns out to have been debited, and no replacement exists yet: correct quietly
	fx := e.OnChargeSucceeded(s, false)
	if !fx.LateSuccessCorrection || !fx.Charged || fx.Alert != "" {
		t.Fatalf("fx = %+v", fx)
	}
	if s.Cycle.Status != models.BillingCycleStatusPaid || s.Cycle.NextChargeAttemptAt != nil {
		t.Fatal("the cycle must be paid and the scheduled retry cancelled")
	}
}

func TestLateSuccessAfterReplacementAlertsPossibleDoubleCharge(t *testing.T) {
	e := newEngine()
	s := newState(models.ChargeAttemptStatusDebitFailed)
	fx := e.OnChargeSucceeded(s, true) // a replacement attempt already exists
	if !fx.LateSuccessCorrection || fx.Alert == "" || !strings.Contains(fx.Alert, "charged twice") {
		t.Fatalf("a replacement exists: must alert, got %+v", fx)
	}

	// the cycle was already paid by the replacement
	s = newState(models.ChargeAttemptStatusDebitFailed)
	s.Cycle.Status = models.BillingCycleStatusPaid
	before := *s.Mandate.NextChargeDueAt
	fx = e.OnChargeSucceeded(s, false)
	if fx.Alert == "" || fx.Charged {
		t.Fatalf("a paid cycle must alert and not count a second purchase: %+v", fx)
	}
	if !s.Mandate.NextChargeDueAt.Equal(before) {
		t.Fatal("the schedule must not advance a second time")
	}
	if s.Attempt.Status != models.ChargeAttemptStatusDebitSucceeded {
		t.Fatal("the money moved, so the attempt is recorded as succeeded")
	}
}

func TestYearlyPlanThatSucceededOnARetryGetsAShorterTenure(t *testing.T) {
	e := newEngine()
	s := newState(models.ChargeAttemptStatusDebitSubmitted)
	s.Mandate.BillingFrequency = "yearly"
	s.Attempt.AttemptNumber = 4 // -> 2 months
	e.OnChargeSucceeded(s, false)
	wantNext := s.Cycle.StartAt.AddDate(0, 2, 0)
	if !s.Mandate.NextChargeDueAt.Equal(wantNext) {
		t.Fatalf("next charge %v, want %v", s.Mandate.NextChargeDueAt, wantNext)
	}
	if s.Cycle.EndAt == nil || !s.Cycle.EndAt.Equal(wantNext) {
		t.Fatalf("cycle end %v", s.Cycle.EndAt)
	}
}

func TestSuccessOnAnEndedMandateDoesNotReschedule(t *testing.T) {
	e := newEngine()
	s := newState(models.ChargeAttemptStatusDebitSubmitted)
	s.Mandate.Status = models.MandateStatusCancelled
	s.Mandate.NextChargeDueAt = nil
	e.OnChargeSucceeded(s, false)
	if s.Mandate.NextChargeDueAt != nil || s.Mandate.Status != models.MandateStatusCancelled {
		t.Fatal("a debit that lands after cancellation must not revive or reschedule the mandate")
	}
}

// ---------- debit failure ----------

func TestCountedFailureSchedulesTheNextRetryAtThreeDayIntervals(t *testing.T) {
	e := newEngine()
	for counted, wantDays := range map[int]int{1: 3, 2: 6, 3: 9} {
		s := newState(models.ChargeAttemptStatusDebitSubmitted)
		s.Cycle.CountedChargeAttempts = counted
		fx := e.OnChargeFailed(s, Failure{Category: models.FailureCategoryRejectedPermanently, Code: "INSUFFICIENT_FUNDS", Counts: true})
		if !fx.PaymentFailed || !fx.Changed {
			t.Fatalf("fx = %+v", fx)
		}
		if want := s.Cycle.StartAt.AddDate(0, 0, wantDays); !s.Cycle.NextChargeAttemptAt.Equal(want) {
			t.Errorf("after %d counted attempts next = %v, want T+%d", counted, s.Cycle.NextChargeAttemptAt, wantDays)
		}
	}
}

func TestNinthCountedFailureFailsTheCycleAndExpiresTheMandate(t *testing.T) {
	e := newEngine()
	s := newState(models.ChargeAttemptStatusDebitSubmitted)
	s.Cycle.CountedChargeAttempts = 9
	e.OnChargeFailed(s, Failure{Category: models.FailureCategoryRejectedPermanently, Counts: true})
	if s.Cycle.Status != models.BillingCycleStatusFailedAllAttempts || s.Cycle.NextChargeAttemptAt != nil {
		t.Fatalf("cycle = %+v", s.Cycle)
	}
	if s.Mandate.Status != models.MandateStatusExpired || s.Mandate.NextChargeDueAt != nil {
		t.Fatalf("mandate = %+v", s.Mandate)
	}
}

func TestGatewayUnavailableFailureDoesNotUseUpAnAttemptOrReduceTheAmount(t *testing.T) {
	e := newEngine()
	s := newState(models.ChargeAttemptStatusDebitSubmitted)
	s.Cycle.CountedChargeAttempts = 3
	fx := e.OnChargeFailed(s, Failure{Category: models.FailureCategoryGatewayUnavailable, Code: "503", Counts: false})
	if !fx.Changed || fx.PaymentFailed {
		t.Fatalf("a gateway outage is not a customer payment failure: %+v", fx)
	}
	if s.Attempt.CountsTowardAttemptLimit || s.Cycle.CountedChargeAttempts != 2 {
		t.Fatalf("must not count: attempt.counts=%v counted=%d", s.Attempt.CountsTowardAttemptLimit, s.Cycle.CountedChargeAttempts)
	}
	if s.Cycle.NextChargeAttemptAt == nil || !s.Cycle.NextChargeAttemptAt.Equal(t0) {
		t.Fatalf("the replacement must be due immediately (next notify run), got %v", s.Cycle.NextChargeAttemptAt)
	}
	// the replacement attempt (counted attempt number 3 again) is charged the same amount as the failed one
	if got, want := e.Policy.AttemptAmount(29900, s.Cycle.CountedChargeAttempts+1, "monthly"), e.Policy.AttemptAmount(29900, 3, "monthly"); got != want {
		t.Fatalf("amount ladder moved: %d vs %d", got, want)
	}
}

func TestDeadMandateIsCancelledInsteadOfRetried(t *testing.T) {
	e := newEngine()
	s := newState(models.ChargeAttemptStatusDebitSubmitted)
	fx := e.OnChargeFailed(s, Failure{Category: models.FailureCategoryRejectedPermanently, Code: "MANDATE_REVOKED", Counts: true, MandateDead: true})
	if !fx.MandateCancelled {
		t.Fatalf("fx = %+v", fx)
	}
	if s.Mandate.Status != models.MandateStatusCancelled || s.Cycle.Status != models.BillingCycleStatusCancelled || s.Cycle.NextChargeAttemptAt != nil {
		t.Fatalf("mandate=%s cycle=%s", s.Mandate.Status, s.Cycle.Status)
	}
}

func TestFailureIsIdempotentAndDoesNotReviveEndedMandates(t *testing.T) {
	e := newEngine()
	s := newState(models.ChargeAttemptStatusDebitSubmitted)
	f := Failure{Category: models.FailureCategoryRejectedPermanently, Counts: true}
	e.OnChargeFailed(s, f)
	next := *s.Cycle.NextChargeAttemptAt
	if fx := e.OnChargeFailed(s, f); fx.Changed || fx.Ignored != "duplicate" {
		t.Fatalf("second failure: %+v", fx)
	}
	if !s.Cycle.NextChargeAttemptAt.Equal(next) {
		t.Fatal("a duplicate failure must not reschedule")
	}

	ended := newState(models.ChargeAttemptStatusDebitSubmitted)
	ended.Mandate.Status = models.MandateStatusCancelled
	ended.Cycle.Status = models.BillingCycleStatusCancelled
	ended.Cycle.NextChargeAttemptAt = nil
	e.OnChargeFailed(ended, f)
	if ended.Mandate.Status != models.MandateStatusCancelled || ended.Cycle.Status != models.BillingCycleStatusCancelled || ended.Cycle.NextChargeAttemptAt != nil {
		t.Fatal("a late failure must not re-label an ended mandate or schedule a retry on a closed cycle")
	}
}

func TestAuthorizationFailureHasNoRetryLadder(t *testing.T) {
	e := newEngine()
	s := newState(models.ChargeAttemptStatusDebitSubmitted)
	s.Cycle.CycleNumber = 0
	s.Mandate.Status = models.MandateStatusAwaitingAuthorization
	fx := e.OnChargeFailed(s, Failure{Category: models.FailureCategoryRejectedPermanently, Counts: true})
	if !fx.PaymentFailed || s.Cycle.Status != models.BillingCycleStatusAwaitingPayment || s.Mandate.Status != models.MandateStatusAwaitingAuthorization {
		t.Fatalf("cycle 0 failure must only mark the attempt: %+v cycle=%s mandate=%s", fx, s.Cycle.Status, s.Mandate.Status)
	}
}

// ---------- mandate lifecycle ----------

func TestMandateStatusChangedRules(t *testing.T) {
	e := newEngine()
	s := newState(models.ChargeAttemptStatusDebitSubmitted)
	m, c := s.Mandate, s.Cycle

	if fx := e.OnMandateStatusChanged(m, c, gateway.MandatePaused); !fx.Changed || m.Status != models.MandateStatusPaused {
		t.Fatalf("pause: %+v", fx)
	}
	if fx := e.OnMandateStatusChanged(m, c, gateway.MandatePaused); fx.Changed || fx.Ignored != "stale" {
		t.Fatalf("pausing twice must be ignored: %+v", fx)
	}
	if fx := e.OnMandateStatusChanged(m, c, gateway.MandateActive); !fx.Changed || m.Status != models.MandateStatusActive {
		t.Fatalf("resume: %+v", fx)
	}
	fx := e.OnMandateStatusChanged(m, c, gateway.MandateCancelled)
	if !fx.Changed || !fx.MandateCancelled || m.Status != models.MandateStatusCancelled || c.Status != models.BillingCycleStatusCancelled || m.NextChargeDueAt != nil {
		t.Fatalf("cancel: %+v mandate=%s cycle=%s", fx, m.Status, c.Status)
	}
	// cancelled is absorbing
	for _, state := range []gateway.MandateState{gateway.MandateActive, gateway.MandatePaused, gateway.MandateExpired} {
		if fx := e.OnMandateStatusChanged(m, c, state); fx.Changed || fx.Ignored != "terminal" || m.Status != models.MandateStatusCancelled {
			t.Errorf("%s after cancel: %+v status=%s", state, fx, m.Status)
		}
	}
}

func TestCancellingAMandateThatNeverWentLiveDoesNotFireTheCancelledEvent(t *testing.T) {
	e := newEngine()
	s := newState(models.ChargeAttemptStatusDebitSubmitted)
	s.Mandate.Status = models.MandateStatusAwaitingAuthorization
	if fx := e.OnMandateStatusChanged(s.Mandate, s.Cycle, gateway.MandateCancelled); !fx.Changed || fx.MandateCancelled {
		t.Fatalf("fx = %+v", fx)
	}
}

func TestAuthorizationSucceededActivatesTheMandate(t *testing.T) {
	e := newEngine()
	s := newState(models.ChargeAttemptStatusDebitSubmitted)
	s.Cycle.CycleNumber = 0
	s.Mandate.Status = models.MandateStatusAwaitingAuthorization
	s.Mandate.NextChargeDueAt = nil
	start := t0.Add(60 * time.Hour)
	s.Mandate.StartAt = &start

	fx := e.OnMandateAuthorizationSucceeded(s, "token_123", "pay_auth_1")
	if !fx.Changed || !fx.AuthorizationCompleted {
		t.Fatalf("fx = %+v", fx)
	}
	if s.Mandate.Status != models.MandateStatusActive || s.Cycle.Status != models.BillingCycleStatusPaid || s.Attempt.Status != models.ChargeAttemptStatusDebitSucceeded {
		t.Fatalf("rows: mandate=%s cycle=%s attempt=%s", s.Mandate.Status, s.Cycle.Status, s.Attempt.Status)
	}
	if !s.Mandate.NextChargeDueAt.Equal(start) {
		t.Fatalf("the first billing date is the mandate start, got %v", s.Mandate.NextChargeDueAt)
	}
	if *s.Mandate.GatewayMandateID != "token_123" || *s.Attempt.GatewayPaymentID != "pay_auth_1" || s.Mandate.Metadata["authorized_at"] == nil {
		t.Fatal("gateway ids and authorized_at must be recorded")
	}
	if fx := e.OnMandateAuthorizationSucceeded(s, "token_123", "pay_auth_1"); fx.Changed || fx.Ignored != "duplicate" {
		t.Fatalf("second authorization: %+v", fx)
	}
}

func TestAuthorizationAfterCancellationIsIgnoredAndAlerts(t *testing.T) {
	e := newEngine()
	s := newState(models.ChargeAttemptStatusDebitSubmitted)
	s.Cycle.CycleNumber = 0
	s.Mandate.Status = models.MandateStatusCancelled
	fx := e.OnMandateAuthorizationSucceeded(s, "token", "pay")
	if fx.Changed || fx.Ignored != "terminal" || fx.Alert == "" || s.Mandate.Status != models.MandateStatusCancelled {
		t.Fatalf("fx = %+v status=%s", fx, s.Mandate.Status)
	}
}

func TestOneTimePaymentSucceededAndLateCorrection(t *testing.T) {
	e := newEngine()
	s := newState(models.ChargeAttemptStatusAwaitingNotification)
	s.Cycle.CycleNumber = 0
	s.Mandate.PaymentType = models.PaymentTypeOneTime
	s.Mandate.Status = models.MandateStatusAwaitingAuthorization
	s.Mandate.NextChargeDueAt = nil

	fx := e.OnOneTimePaymentSucceeded(s)
	if !fx.Changed || !fx.Charged || fx.LateSuccessCorrection || s.Mandate.Status != models.MandateStatusActive || s.Cycle.Status != models.BillingCycleStatusPaid {
		t.Fatalf("fx = %+v mandate=%s cycle=%s", fx, s.Mandate.Status, s.Cycle.Status)
	}
	if s.Mandate.NextChargeDueAt != nil {
		t.Fatal("a one-time payment must never get a next charge date")
	}
	if fx := e.OnOneTimePaymentSucceeded(s); fx.Changed || fx.Ignored != "duplicate" {
		t.Fatalf("duplicate: %+v", fx)
	}

	// a customer paying again on the same order after a failed attempt is a correction, not a duplicate purchase
	late := newState(models.ChargeAttemptStatusDebitFailed)
	late.Cycle.CycleNumber = 0
	if fx := e.OnOneTimePaymentSucceeded(late); !fx.LateSuccessCorrection || !fx.Charged {
		t.Fatalf("late one-time success: %+v", fx)
	}
}

func TestAuthorizationAfterExpiryAlertsToo(t *testing.T) {
	// the authorization payment moved money on a mandate the sync cron had already closed as expired: must alert
	e := newEngine()
	s := newState(models.ChargeAttemptStatusDebitSubmitted)
	s.Cycle.CycleNumber = 0
	s.Mandate.Status = models.MandateStatusExpired
	fx := e.OnMandateAuthorizationSucceeded(s, "token", "pay")
	if fx.Changed || fx.Ignored != "terminal" || fx.Alert == "" || s.Mandate.Status != models.MandateStatusExpired {
		t.Fatalf("fx = %+v status=%s", fx, s.Mandate.Status)
	}
}

func TestScheduledDebitAtPrefersTheRetryTime(t *testing.T) {
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	retry := start.AddDate(0, 0, 3)
	if got := scheduledDebitAt(&models.AutopayBillingCycle{StartAt: start}); !got.Equal(start) {
		t.Fatalf("a first attempt is scheduled at the cycle start, got %v", got)
	}
	if got := scheduledDebitAt(&models.AutopayBillingCycle{StartAt: start, NextChargeAttemptAt: &retry}); !got.Equal(retry) {
		t.Fatalf("a retry is scheduled at the cycle's next attempt time, got %v", got)
	}
}
