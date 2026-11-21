package service

import (
	"fmt"
	"time"

	"go-backend/internal/apps/autopay/gateway"
	"go-backend/internal/apps/autopay/models"
	"go-backend/pkg/utils"
)

// Engine applies gateway outcomes (webhook events, fetched statuses, call results) to the mandate / billing cycle /
// charge attempt rows. It is pure: it only mutates the structs it is given and reports what happened in Effects; the
// caller persists the rows in ONE transaction using an optimistic UPDATE ... WHERE status = <expected> (so two
// concurrent deliveries, or a webhook racing a cron, cannot both win) and publishes the Effects exactly once.
//
// The rules (ordering and idempotency) are the ones from the design:
//   - applying the same input twice is a no-op (Effects.Ignored = "duplicate");
//   - debit_succeeded is absorbing: any later event for the attempt is ignored;
//   - a success may be applied from any non-succeeded state (it jumps forward);
//   - a failure applies only from a non-terminal state; a failure for a succeeded attempt is stale;
//   - debit_failed -> debit_succeeded is the one correction (late success) and alerts when money may have been
//     collected twice;
//   - cancelled / expired mandates are absorbing.
type Engine struct {
	Policy RetryPolicy
	Now    func() time.Time // UTC
}

// NewEngine returns an Engine with the default retry policy and the real clock.
func NewEngine() *Engine {
	return &Engine{Policy: DefaultRetryPolicy(), Now: func() time.Time { return time.Now().UTC() }}
}

// State is the entity bundle a charge-related input applies to.
type State struct {
	Mandate *models.AutopayMandate
	Cycle   *models.AutopayBillingCycle
	Attempt *models.AutopayChargeAttempt
}

// Effects is the outcome of applying one input.
type Effects struct {
	// Changed: some row changed and must be persisted.
	Changed bool
	// Ignored explains why the input was not applied ("duplicate", "stale", ...); empty when it was applied. An
	// ignored input may still have Changed = true when it only recorded a missing fact.
	Ignored string

	// Things to publish after the rows are saved (each is true only when the transition actually happened):
	Charged                bool // a debit (or one-time payment) succeeded: purchase analytics
	AuthorizationCompleted bool // the mandate became active: trial-started analytics
	PaymentFailed          bool // a counted failure (or failed authorization): payment-failed analytics
	MandateCancelled       bool // a live mandate was cancelled: cancelled analytics
	LateSuccessCorrection  bool // debit_failed -> debit_succeeded

	// Alert is non-empty when a human must look (customer charged twice, conflicting state): send to Sentry.
	Alert string
}

// Failure describes why a debit (or authorization) failed.
type Failure struct {
	Category    models.FailureCategory
	Code        string
	Description string
	// Counts says whether the attempt counts toward the 9 per cycle and the reduced-amount ladder. Customer/bank
	// failures count; gateway_unavailable does not.
	Counts bool
	// MandateDead: the mandate itself is unusable (revoked, closed account): cancel it instead of retrying. Decided
	// by the adapter's error classification (gateway.ErrorKindMandateInvalid).
	MandateDead bool
}

func mandateEnded(m *models.AutopayMandate) bool {
	return m.Status == models.MandateStatusCancelled || m.Status == models.MandateStatusExpired
}

// ==================== Pre-debit notification ====================

// OnPreDebitNotificationDelivered applies a delivered notification. deliveredAt is the gateway's delivered time
// (zero when it gave none); the fallback is the time our send call returned, NEVER the time we noticed, because the
// 26h debit gate is measured from it.
func (e *Engine) OnPreDebitNotificationDelivered(s State, deliveredAt time.Time) Effects {
	a := s.Attempt
	switch a.Status {
	case models.ChargeAttemptStatusNotificationDelivered:
		return Effects{Ignored: "duplicate"}
	case models.ChargeAttemptStatusDebitSubmitted, models.ChargeAttemptStatusDebitSucceeded, models.ChargeAttemptStatusDebitFailed:
		// a late "delivered" after the debit moved on: only fill the timestamp if it is missing
		if a.PreDebitNotificationDeliveredAt == nil && !deliveredAt.IsZero() {
			t := deliveredAt.UTC()
			a.PreDebitNotificationDeliveredAt = &t
			return Effects{Changed: true, Ignored: "stale"}
		}
		return Effects{Ignored: "stale"}
	}

	at := deliveredAt.UTC()
	if deliveredAt.IsZero() {
		if a.PreDebitNotificationSentAt == nil {
			return Effects{Ignored: "no_delivery_time"}
		}
		at = a.PreDebitNotificationSentAt.UTC()
	}
	a.Status = models.ChargeAttemptStatusNotificationDelivered
	a.PreDebitNotificationDeliveredAt = &at
	delivered := string(gateway.NotificationDelivered)
	a.PreDebitNotificationStatus = &delivered
	return Effects{Changed: true}
}

// OnPreDebitNotificationFailed fails an attempt whose notification could not be delivered; it goes into the normal
// retry ladder (a new attempt needs a new notification anyway).
func (e *Engine) OnPreDebitNotificationFailed(s State, code, description string) Effects {
	if s.Attempt.Status != models.ChargeAttemptStatusAwaitingNotification {
		return Effects{Ignored: "stale"}
	}
	failed := string(gateway.NotificationFailed)
	s.Attempt.PreDebitNotificationStatus = &failed
	return e.OnChargeFailed(s, Failure{Category: models.FailureCategoryNotificationFailed, Code: code, Description: description, Counts: true})
}

// ==================== Debit result ====================

// OnChargeSucceeded applies a successful debit of a billing cycle >= 1. laterAttemptExists says whether a newer
// attempt was already created on the cycle (only relevant for a late success on a failed attempt).
func (e *Engine) OnChargeSucceeded(s State, laterAttemptExists bool) Effects {
	a, c, m := s.Attempt, s.Cycle, s.Mandate
	if a.Status == models.ChargeAttemptStatusDebitSucceeded {
		return Effects{Ignored: "duplicate"}
	}
	now := e.Now()
	wasFailed := a.Status == models.ChargeAttemptStatusDebitFailed

	fx := Effects{Changed: true}
	a.Status = models.ChargeAttemptStatusDebitSucceeded
	a.FailureCategory, a.FailureCode, a.FailureDescription = nil, "", ""

	if wasFailed {
		fx.LateSuccessCorrection = true
		if c.Status == models.BillingCycleStatusPaid || laterAttemptExists {
			fx.Alert = fmt.Sprintf("autopay attempt %s succeeded after it was marked failed, but cycle %s is %s (later attempt: %t): the customer may have been charged twice and need a refund",
				a.MerchantPaymentID, c.ID, c.Status, laterAttemptExists)
		}
	}

	switch c.Status {
	case models.BillingCycleStatusAwaitingPayment:
		c.Status = models.BillingCycleStatusPaid
		c.NextChargeAttemptAt = nil
		c.LastChargeAttemptAt = &now
		c.Amount = a.Amount // the cycle amount is what was actually debited
		m.LastSuccessfulChargeAt = &now
		if !mandateEnded(m) {
			if alert := e.scheduleNextCharge(m, c, a, now); alert != "" {
				fx.Alert = joinAlerts(fx.Alert, alert)
			}
		}
		fx.Charged = true
	case models.BillingCycleStatusPaid:
		// paid by another attempt (the correction case above already alerted) or already settled
	default:
		fx.Alert = joinAlerts(fx.Alert, fmt.Sprintf("autopay attempt %s was debited but cycle %s is %s: the money moved on a closed cycle", a.MerchantPaymentID, c.ID, c.Status))
	}
	return fx
}

// scheduleNextCharge sets the mandate's next billing date after a paid cycle. A yearly plan that only succeeded on
// a retry gets a shorter tenure.
func (e *Engine) scheduleNextCharge(m *models.AutopayMandate, c *models.AutopayBillingCycle, a *models.AutopayChargeAttempt, now time.Time) string {
	base := now
	if m.NextChargeDueAt != nil {
		base = *m.NextChargeDueAt
	}
	next, err := NextChargeDueAt(m.BillingFrequency, base)
	if err != nil {
		return err.Error()
	}
	if m.BillingFrequency == "yearly" && a.AttemptNumber > 1 && a.CountsTowardAttemptLimit {
		next = c.StartAt.AddDate(0, ActualTenureMonths(a.AttemptNumber), 0).UTC()
		c.EndAt = &next
	}
	m.NextChargeDueAt = &next
	return ""
}

// OnChargeFailed applies a failed debit, a failed notification or a failed authorization.
func (e *Engine) OnChargeFailed(s State, f Failure) Effects {
	a, c, m := s.Attempt, s.Cycle, s.Mandate
	switch a.Status {
	case models.ChargeAttemptStatusDebitSucceeded:
		return Effects{Ignored: "stale"} // succeeded is absorbing
	case models.ChargeAttemptStatusDebitFailed:
		return Effects{Ignored: "duplicate"}
	}

	a.Status = models.ChargeAttemptStatusDebitFailed
	category := f.Category
	a.FailureCategory = &category
	a.FailureCode = utils.Truncate(f.Code, 255)
	a.FailureDescription = f.Description
	fx := Effects{Changed: true, PaymentFailed: f.Counts}

	// The authorization / one-time cycle has no retry ladder: the customer pays again or the sync cron closes it.
	if c.CycleNumber == 0 {
		fx.PaymentFailed = true
		return fx
	}
	// A late failure on a mandate or cycle that already ended must not revive or re-label it.
	if mandateEnded(m) || c.Status != models.BillingCycleStatusAwaitingPayment {
		return fx
	}

	now := e.Now()
	switch {
	case f.MandateDead:
		fx.MandateCancelled = e.cancelMandate(m, c)
	case !f.Counts:
		// Gateway-side failure: does not use up one of the 9 and does not reduce the next amount. The replacement
		// attempt reuses the number and is created by the next notify run.
		a.CountsTowardAttemptLimit = false
		if c.CountedChargeAttempts > 0 {
			c.CountedChargeAttempts--
		}
		c.NextChargeAttemptAt = &now
	case c.CountedChargeAttempts >= e.Policy.MaxCountedChargeAttemptsPerBillingCycle:
		e.failAllAttempts(m, c)
	default:
		next := e.Policy.NextRetryAt(c.StartAt, c.CountedChargeAttempts)
		c.NextChargeAttemptAt = &next
	}
	c.LastChargeAttemptAt = &now
	return fx
}

// ==================== Mandate lifecycle ====================

// failAllAttempts ends a billing cycle that used all its counted attempts: the cycle fails and the mandate expires.
// The gateway's mandate is still live, so it is flagged for cancellation at the gateway (see markGatewayCancelPending).
func (e *Engine) failAllAttempts(m *models.AutopayMandate, c *models.AutopayBillingCycle) {
	c.Status = models.BillingCycleStatusFailedAllAttempts
	c.NextChargeAttemptAt = nil
	m.Status = models.MandateStatusExpired
	m.NextChargeDueAt = nil
	markGatewayCancelPending(m)
}

// markGatewayCancelPending flags a mandate that we retired ourselves while the gateway's mandate may still be live:
// the nightly sync cancels it there so it stops incurring the gateway's maintenance fee. Only recurring mandates reach
// it (cycles >= 1 and authorizations exist only for them). The gateway mandate id is not required (Cashfree and PhonePe
// cancel by our merchant_autopay_id).
func markGatewayCancelPending(m *models.AutopayMandate) {
	if m.Metadata == nil {
		m.Metadata = utils.Metadata{}
	}
	m.Metadata[models.MetadataKeyGatewayCancelPending] = true
}

// cancelMandate marks the mandate cancelled and stops its pending cycle. Returns true when a live (active or
// paused) mandate was cancelled, i.e. when the cancelled analytics event should fire.
func (e *Engine) cancelMandate(m *models.AutopayMandate, latest *models.AutopayBillingCycle) bool {
	if mandateEnded(m) {
		return false
	}
	wasLive := m.Status == models.MandateStatusActive || m.Status == models.MandateStatusPaused
	m.Status = models.MandateStatusCancelled
	m.NextChargeDueAt = nil
	if latest != nil && latest.Status == models.BillingCycleStatusAwaitingPayment {
		latest.Status = models.BillingCycleStatusCancelled
		latest.NextChargeAttemptAt = nil
	}
	return wasLive
}

func (e *Engine) expireMandate(m *models.AutopayMandate, latest *models.AutopayBillingCycle) {
	if mandateEnded(m) {
		return
	}
	m.Status = models.MandateStatusExpired
	m.NextChargeDueAt = nil
	if latest != nil && latest.Status == models.BillingCycleStatusAwaitingPayment {
		latest.Status = models.BillingCycleStatusCancelled
		latest.NextChargeAttemptAt = nil
	}
}

// OnMandateStatusChanged applies a mandate state the gateway reports (webhook or FetchMandateStatus). latest is the
// mandate's latest billing cycle (may be nil).
func (e *Engine) OnMandateStatusChanged(m *models.AutopayMandate, latest *models.AutopayBillingCycle, state gateway.MandateState) Effects {
	if mandateEnded(m) {
		return Effects{Ignored: "terminal"}
	}
	switch state {
	case gateway.MandatePaused:
		if m.Status != models.MandateStatusActive {
			return Effects{Ignored: "stale"}
		}
		m.Status = models.MandateStatusPaused
		return Effects{Changed: true}
	case gateway.MandateActive:
		if m.Status != models.MandateStatusPaused {
			return Effects{Ignored: "stale"} // activation of a new mandate is MandateAuthorizationSucceeded
		}
		m.Status = models.MandateStatusActive
		return Effects{Changed: true}
	case gateway.MandateCancelled:
		return Effects{Changed: true, MandateCancelled: e.cancelMandate(m, latest)}
	case gateway.MandateExpired:
		e.expireMandate(m, latest)
		return Effects{Changed: true}
	default:
		return Effects{Ignored: "stale"}
	}
}

// OnMandateAuthorizationSucceeded completes authorization: cycle 0 becomes paid and the mandate becomes active, with
// its first billing date at the mandate's start. State.Cycle/Attempt are the cycle-0 rows. Idempotent.
func (e *Engine) OnMandateAuthorizationSucceeded(s State, gatewayMandateID, gatewayPaymentID string) Effects {
	a, c, m := s.Attempt, s.Cycle, s.Mandate
	if a.Status == models.ChargeAttemptStatusDebitSucceeded {
		return Effects{Ignored: "duplicate"}
	}
	if mandateEnded(m) {
		// the authorization payment moved money on a mandate we had already closed (cancelled, or expired because the
		// customer took too long): a human decides whether to refund it
		// the gateway's mandate is live now and we will never charge it: cancel it there
		if gatewayMandateID != "" && m.GatewayMandateID == nil {
			m.GatewayMandateID = &gatewayMandateID
		}
		markGatewayCancelPending(m)
		return Effects{Changed: true, Ignored: "terminal", Alert: fmt.Sprintf("autopay mandate %s was authorized after it was %s: check whether the customer was charged", m.ID, m.Status)}
	}
	now := e.Now()
	a.Status = models.ChargeAttemptStatusDebitSucceeded
	a.FailureCategory, a.FailureCode, a.FailureDescription = nil, "", ""
	if gatewayPaymentID != "" {
		a.GatewayPaymentID = &gatewayPaymentID
	}
	c.Status = models.BillingCycleStatusPaid
	c.LastChargeAttemptAt = &now

	m.Status = models.MandateStatusActive
	m.LastSuccessfulChargeAt = &now
	m.NextChargeDueAt = m.StartAt
	if gatewayMandateID != "" {
		m.GatewayMandateID = &gatewayMandateID
	}
	if m.Metadata == nil {
		m.Metadata = utils.Metadata{}
	}
	m.Metadata["authorized_at"] = now.Format(time.RFC3339)
	return Effects{Changed: true, AuthorizationCompleted: true}
}

// OnOneTimePaymentSucceeded applies a successful one-time payment (cycle 0 of a one_time row). A late success on a
// previously failed attempt is a correction (a customer can pay again on the same order or link).
func (e *Engine) OnOneTimePaymentSucceeded(s State) Effects {
	a, c, m := s.Attempt, s.Cycle, s.Mandate
	if a.Status == models.ChargeAttemptStatusDebitSucceeded {
		return Effects{Ignored: "duplicate"}
	}
	now := e.Now()
	fx := Effects{Changed: true, Charged: true, LateSuccessCorrection: a.Status == models.ChargeAttemptStatusDebitFailed}
	a.Status = models.ChargeAttemptStatusDebitSucceeded
	a.FailureCategory, a.FailureCode, a.FailureDescription = nil, "", ""
	c.Status = models.BillingCycleStatusPaid
	c.LastChargeAttemptAt = &now
	if !mandateEnded(m) {
		m.Status = models.MandateStatusActive
	}
	m.LastSuccessfulChargeAt = &now
	return fx
}

// joinAlerts concatenates alert messages.
func joinAlerts(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	return a + "; " + b
}
