package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go-backend/internal/apps/autopay/gateway"
	"go-backend/internal/apps/autopay/models"
	"go-backend/internal/apps/autopay/ratelimit"
	"go-backend/internal/common/billingwindow"
	"go-backend/pkg/utils"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// The four nightly crons, in run order (IST): SyncFromGateway 12:45, NotifyRetryAttempts 1:10, NotifyNewCycles 1:15,
// DebitNotifiedAttempts 3:30 and 5:20 (a second sync run at 5:10 sits between the two debit runs). Each loops over the
// active configs (merchant accounts) and isolates failures per config. Every gateway call goes through Call
// (rate-limit token per try, retries) with the Background class.

const cronClass = ratelimit.Background

// ==================== Notify new cycles ====================

// NotifyNewCycles creates the next billing cycle for recurring mandates charging inside the notification window and
// sends the pre-debit notification. The debit itself is DebitNotifiedAttempts, once the notification is old enough.
func (s *Service) NotifyNewCycles(ctx context.Context) error {
	return s.notifyInWindow(ctx, "autopay_notify_new_cycles", s.repo.FindRecurringMandatesForNewBillingCycle, s.notifyNewCycle)
}

// notifyInWindow is the body of both notify crons: for every config, find the mandates whose charge falls in the
// notification window (billingwindow.Bounds) and handle each one.
func (s *Service) notifyInWindow(
	ctx context.Context, name string,
	find func(configID uuid.UUID, windowStart, windowEnd time.Time) ([]models.AutopayMandate, error),
	handle func(ctx context.Context, rg *ResolvedGateway, m models.AutopayMandate) error,
) error {
	return s.forEachConfig(ctx, name, func(ctx context.Context, rg *ResolvedGateway) error {
		start, end := billingwindow.Bounds(s.now())
		ms, err := find(rg.Config.ID, start, end)
		if err != nil {
			return fmt.Errorf("find mandates: %w", err)
		}
		fmt.Printf("[%s] config %s: %d mandates\n", name, rg.Config.ID, len(ms))
		processInParallel(ms, func(m models.AutopayMandate) error { return handle(ctx, rg, m) }, name)
		return nil
	})
}

func (s *Service) notifyNewCycle(ctx context.Context, rg *ResolvedGateway, m models.AutopayMandate) error {
	if m.NextChargeDueAt == nil {
		return fmt.Errorf("autopay mandate %s has no next_charge_due_at", m.ID)
	}
	if !s.ensureMandateLive(ctx, rg, &m) {
		return nil
	}

	attemptID, err := s.createNextCycle(m.ID, *m.NextChargeDueAt)
	if err != nil || attemptID == uuid.Nil {
		return err
	}
	return s.sendNotification(ctx, rg, attemptID)
}

// createNextCycle creates cycle N with its first attempt under the mandate's row lock, re-checking that another run
// has not already done it. Returns uuid.Nil when there is nothing to do.
func (s *Service) createNextCycle(mandateID uuid.UUID, due time.Time) (uuid.UUID, error) {
	var attemptID uuid.UUID
	err := s.inTx(func(tx *gorm.DB) error {
		m, latest, err := s.repo.LockMandate(tx, mandateID)
		if err != nil {
			return err
		}
		// another run (or a webhook) may have moved the mandate on since it was selected
		if m.Status != models.MandateStatusActive || m.NextChargeDueAt == nil || !m.NextChargeDueAt.Equal(due) ||
			latest == nil || latest.Status != models.BillingCycleStatusPaid {
			return nil
		}
		next, err := NextChargeDueAt(m.BillingFrequency, due)
		if err != nil {
			return err
		}
		amount := s.engine.Policy.AttemptAmount(m.MaxDebitAmount, 1)
		cycle := &models.AutopayBillingCycle{
			ID: uuid.New(), AutopayMandateID: m.ID, CycleNumber: latest.CycleNumber + 1, StartAt: due, EndAt: &next, Amount: amount,
			NextChargeAttemptAt: &due, CountedChargeAttempts: 1, Status: models.BillingCycleStatusAwaitingPayment, Metadata: utils.Metadata{},
		}
		attempt := &models.AutopayChargeAttempt{
			ID: uuid.New(), BillingCycleID: cycle.ID, GatewayConfigID: m.GatewayConfigID, AttemptNumber: 1, CountsTowardAttemptLimit: true,
			MerchantPaymentID: MerchantPaymentIDFor(cycle.ID, 1, 0), Status: models.ChargeAttemptStatusAwaitingNotification, Amount: amount, Metadata: utils.Metadata{},
		}
		if err := s.repo.CreateBillingCycle(tx, cycle); err != nil {
			return fmt.Errorf("create billing cycle: %w", err)
		}
		if err := s.repo.CreateChargeAttempt(tx, attempt); err != nil {
			return fmt.Errorf("create charge attempt: %w", err)
		}
		attemptID = attempt.ID
		return nil
	})
	if err != nil {
		return uuid.Nil, err // nothing was committed, whatever attemptID says
	}
	return attemptID, nil
}

// ==================== Notify retry attempts ====================

// NotifyRetryAttempts creates the next attempt (new id, reduced amount) for unpaid cycles and sends its pre-debit
// notification. It never stacks a new attempt on one that is still open.
func (s *Service) NotifyRetryAttempts(ctx context.Context) error {
	return s.notifyInWindow(ctx, "autopay_notify_retry_attempts", s.repo.FindRecurringMandatesForRetry, s.notifyRetryAttempt)
}

func (s *Service) notifyRetryAttempt(ctx context.Context, rg *ResolvedGateway, m models.AutopayMandate) error {
	if !s.ensureMandateLive(ctx, rg, &m) {
		return nil
	}
	attemptID, err := s.createRetryAttempt(m.ID)
	if err != nil || attemptID == uuid.Nil {
		return err
	}
	return s.sendNotification(ctx, rg, attemptID)
}

// createRetryAttempt creates the next attempt under the mandate's row lock. When the cycle has used all its counted
// attempts it fails the cycle and expires the mandate instead. Returns uuid.Nil when there is nothing to send.
func (s *Service) createRetryAttempt(mandateID uuid.UUID) (uuid.UUID, error) {
	var attemptID uuid.UUID
	err := s.inTx(func(tx *gorm.DB) error {
		m, cycle, err := s.repo.LockMandate(tx, mandateID)
		if err != nil {
			return err
		}
		if m.Status != models.MandateStatusActive || cycle == nil || cycle.CycleNumber < 1 || cycle.Status != models.BillingCycleStatusAwaitingPayment {
			return nil
		}
		if cycle.CountedChargeAttempts >= s.engine.Policy.MaxCountedChargeAttemptsPerBillingCycle {
			cycle.Status = models.BillingCycleStatusFailedAllAttempts
			cycle.NextChargeAttemptAt = nil
			m.Status = models.MandateStatusExpired
			m.NextChargeDueAt = nil
			if err := s.repo.UpdateBillingCycle(tx, cycle); err != nil {
				return err
			}
			return s.repo.UpdateMandate(tx, m)
		}
		// never stack a new attempt on top of one that is still in flight (a concurrent run already created it)
		if open, err := s.repo.HasOpenChargeAttemptForBillingCycle(cycle.ID); err != nil || open {
			return err
		}

		number := cycle.CountedChargeAttempts + 1
		replacement, err := s.repo.CountNonCountedChargeAttempts(cycle.ID, number)
		if err != nil {
			return err
		}
		amount := s.engine.Policy.AttemptAmount(m.MaxDebitAmount, number)
		attempt := &models.AutopayChargeAttempt{
			ID: uuid.New(), BillingCycleID: cycle.ID, GatewayConfigID: m.GatewayConfigID, AttemptNumber: number, CountsTowardAttemptLimit: true,
			MerchantPaymentID: MerchantPaymentIDFor(cycle.ID, number, replacement), Status: models.ChargeAttemptStatusAwaitingNotification,
			Amount: amount, Metadata: utils.Metadata{},
		}
		cycle.CountedChargeAttempts = number
		if err := s.repo.UpdateBillingCycle(tx, cycle); err != nil {
			return err
		}
		if err := s.repo.CreateChargeAttempt(tx, attempt); err != nil {
			return fmt.Errorf("create retry charge attempt: %w", err)
		}
		attemptID = attempt.ID
		return nil
	})
	if err != nil {
		return uuid.Nil, err
	}
	return attemptID, nil
}

// ==================== Pre-debit notification ====================

// sendNotification sends the pre-debit notification of an attempt. The send is claimed first, so overlapping runs
// never send the same notification twice. After the 3 tries a transient or unknown failure leaves the attempt
// awaiting_notification: the sync cron re-sends it (at most maxPreDebitNotificationSendAttempts in total).
func (s *Service) sendNotification(ctx context.Context, rg *ResolvedGateway, attemptID uuid.UUID) error {
	a, err := s.repo.FindChargeAttemptByID(attemptID)
	if err != nil {
		return err
	}
	cycle, m, err := s.loadCycleAndMandate(a.BillingCycleID)
	if err != nil {
		return err
	}
	if a.Status != models.ChargeAttemptStatusAwaitingNotification || a.PreDebitNotificationSendAttempts >= maxPreDebitNotificationSendAttempts {
		return nil
	}
	// Load everything the call needs BEFORE claiming: a claim followed by a failed lookup would burn one of the sends.
	user, err := s.users.FindByID(m.UserID)
	if err != nil {
		return fmt.Errorf("load user %s: %w", m.UserID, err)
	}
	notificationID := PreDebitNotificationIDFor(a.MerchantPaymentID, a.PreDebitNotificationSendAttempts+1)
	won, err := s.repo.ClaimChargeAttemptForNotificationSend(a.ID, a.PreDebitNotificationSendAttempts, notificationID)
	if err != nil || !won {
		return err
	}
	res, callErr := Call(ctx, s.policyFor(rg, cronClass), func(ctx context.Context) (*gateway.PreDebitNotificationResult, error) {
		return rg.Gateway.SendPreDebitNotification(ctx, gateway.PreDebitNotificationRequest{
			Mandate: mandateRef(m), MerchantPaymentID: a.MerchantPaymentID, PreDebitNotificationID: notificationID,
			Amount: a.Amount, Currency: m.Currency, EarliestDebitAt: billingwindow.EarliestDebit(s.now(), scheduledDebitAt(cycle)), User: userInfo(user),
		})
	})

	switch {
	case callErr == nil:
		return s.recordNotificationSent(attemptID, res)
	case gateway.IsKind(callErr, gateway.ErrorKindAlreadyProcessed):
		return s.recordNotificationSent(attemptID, &gateway.PreDebitNotificationResult{State: gateway.NotificationPending})
	case gateway.IsKind(callErr, gateway.ErrorKindRejectedPermanently), gateway.IsKind(callErr, gateway.ErrorKindMandateInvalid):
		s.events.NotificationSendFailed(m, a.Amount, gatewayCode(callErr))
		dead := gateway.IsKind(callErr, gateway.ErrorKindMandateInvalid)
		return s.mutateAttempt(attemptID, func(st State) Effects {
			return s.engine.OnChargeFailed(st, Failure{Category: models.FailureCategoryNotificationFailed, Code: gatewayCode(callErr), Description: callErr.Error(), Counts: true, MandateDead: dead})
		})
	default: // transient / outcome unknown / rejected temporarily: leave it for the sync cron to re-send
		s.events.NotificationSendFailed(m, a.Amount, gatewayCode(callErr))
		return fmt.Errorf("send pre-debit notification for attempt %s (left for the sync run): %w", a.MerchantPaymentID, callErr)
	}
}

// recordNotificationSent stores the outcome of a successful send call: when it returned, the gateway ids, and a
// delivered/failed state when the gateway answered synchronously.
func (s *Service) recordNotificationSent(attemptID uuid.UUID, res *gateway.PreDebitNotificationResult) error {
	return s.mutateAttempt(attemptID, func(st State) Effects {
		a := st.Attempt
		now := s.now()
		a.PreDebitNotificationSentAt = &now
		if res.GatewayOrderID != "" {
			a.GatewayOrderID = strPtr(res.GatewayOrderID)
		}
		if res.PreDebitNotificationID != "" {
			a.PreDebitNotificationID = strPtr(res.PreDebitNotificationID)
		}
		// a webhook may have settled the notification while the send call was returning: never overwrite its outcome
		if a.Status == models.ChargeAttemptStatusAwaitingNotification {
			status := string(res.State)
			a.PreDebitNotificationStatus = &status
		}
		if !res.ValidAfter.IsZero() {
			setMetaTime(a, "valid_after", res.ValidAfter)
		}
		fx := Effects{Changed: true}
		switch res.State {
		case gateway.NotificationDelivered:
			fx = mergeEffects(fx, s.engine.OnPreDebitNotificationDelivered(st, res.DeliveredAt))
		case gateway.NotificationFailed:
			fx = mergeEffects(fx, s.engine.OnPreDebitNotificationFailed(st, "notification_failed", "the gateway reported the notification as failed"))
		}
		return fx
	})
}

// ==================== Debit ====================

// DebitNotifiedAttempts submits the debit for attempts whose notification is delivered and at least 26h old and
// whose cycle is due. It runs twice a night (3:30 and 5:20 AM): an attempt released after a transient problem is
// retried by the second run; an attempt left debit_submitted is never re-submitted.
func (s *Service) DebitNotifiedAttempts(ctx context.Context) error {
	return s.forEachConfig(ctx, "autopay_debit_notified_attempts", func(ctx context.Context, rg *ResolvedGateway) error {
		caps := rg.Gateway.Capabilities()
		as, err := s.repo.FindChargeAttemptsReadyForDebit(rg.Config.ID, s.now(), max(minNotificationAgeBeforeDebit, caps.MinNotificationToDebitGap), caps.MinDebitRetryGap)
		if err != nil {
			return fmt.Errorf("find attempts ready for debit: %w", err)
		}
		fmt.Printf("[autopay_debit_notified_attempts] config %s: %d attempts\n", rg.Config.ID, len(as))
		processInParallel(as, func(a models.AutopayChargeAttempt) error { return s.debitAttempt(ctx, rg, a) }, "autopay_debit_notified_attempts")
		return nil
	})
}

func (s *Service) debitAttempt(ctx context.Context, rg *ResolvedGateway, a models.AutopayChargeAttempt) error {
	_, m, err := s.loadCycleAndMandate(a.BillingCycleID)
	if err != nil {
		return err
	}
	// PhonePe tells us when the notification has matured; never debit before that.
	if t, ok := metaTime(a.Metadata, "valid_after"); ok && s.now().Before(t) {
		return nil
	}
	if !s.ensureMandateLive(ctx, rg, m) {
		return nil
	}

	// An order-per-attempt gateway debits the order its notification created. An attempt without one cannot be matched
	// at the gateway, so it is not claimed or sent: it stays notification_delivered and a human is told.
	if rg.Gateway.Capabilities().ChargeAttemptIdentity == gateway.OrderPerChargeAttempt && deref(a.GatewayOrderID) == "" {
		alert(fmt.Sprintf("autopay attempt %s has a delivered notification but no %s order id: not debited", a.MerchantPaymentID, rg.Config.Gateway))
		return nil
	}

	// Load everything the call needs BEFORE claiming: a claim followed by a failed lookup would strand the attempt in
	// debit_submitted without any call having been made.
	user, err := s.users.FindByID(m.UserID)
	if err != nil {
		return fmt.Errorf("load user %s: %w", m.UserID, err)
	}
	executionID := DebitExecutionIDFor(a.MerchantPaymentID, a.DebitSubmitAttempts+1)
	won, err := s.repo.ClaimChargeAttemptForDebit(a.ID, a.DebitSubmitAttempts, executionID)
	if err != nil || !won {
		return err
	}
	a.DebitSubmitAttempts++
	a.DebitExecutionID = &executionID
	a.Status = models.ChargeAttemptStatusDebitSubmitted
	sub, callErr := Call(ctx, s.policyFor(rg, cronClass), func(ctx context.Context) (*gateway.DebitSubmission, error) {
		return rg.Gateway.SubmitDebit(ctx, gateway.DebitRequest{Attempt: attemptRef(m, &a), Amount: a.Amount, DebitExecutionID: executionID, Currency: m.Currency, User: userInfo(user)})
	})
	return s.afterDebitSubmission(ctx, rg, m, &a, sub, callErr)
}

// afterDebitSubmission applies the outcome table: what a debit submission's result (after the 3 tries) means.
func (s *Service) afterDebitSubmission(ctx context.Context, rg *ResolvedGateway, m *models.AutopayMandate, a *models.AutopayChargeAttempt, sub *gateway.DebitSubmission, callErr error) error {
	code := gatewayCode(callErr)
	switch {
	case callErr == nil:
		return s.recordDebitSubmitted(a.ID, sub)

	case gateway.IsKind(callErr, gateway.ErrorKindAlreadyProcessed):
		return nil // the debit already went through; the webhook / sync settles it

	case gateway.IsKind(callErr, gateway.ErrorKindRejectedPermanently), gateway.IsKind(callErr, gateway.ErrorKindMandateInvalid):
		s.events.DebitSubmitFailed(m, a.Amount, code)
		dead := gateway.IsKind(callErr, gateway.ErrorKindMandateInvalid)
		return s.mutateAttempt(a.ID, func(st State) Effects {
			return s.engine.OnChargeFailed(st, Failure{Category: models.FailureCategoryRejectedPermanently, Code: code, Description: callErr.Error(), Counts: true, MandateDead: dead})
		})

	case gateway.IsKind(callErr, gateway.ErrorKindRejectedTemporarily):
		s.events.DebitSubmitFailed(m, a.Amount, code)
		return s.mutateAttempt(a.ID, func(st State) Effects { return s.temporaryRejection(st, code, callErr.Error()) })

	case gateway.IsKind(callErr, gateway.ErrorKindTransient):
		s.events.DebitSubmitFailed(m, a.Amount, code)
		return s.mutateAttempt(a.ID, func(st State) Effects { return s.gatewayUnavailable(st, code, callErr.Error()) })

	default: // outcome unknown (or a non-gateway error): verify with the gateway before deciding
		return s.verifyUnknownDebitOutcome(ctx, rg, m, a, callErr)
	}
}

// verifyUnknownDebitOutcome settles a debit whose submission may or may not have reached the gateway: a debit that
// exists is applied as the gateway reports it; no record means it never happened, so it is treated as a transient
// failure (released, not counted); if the verification itself fails the attempt stays debit_submitted for the sync run.
func (s *Service) verifyUnknownDebitOutcome(ctx context.Context, rg *ResolvedGateway, m *models.AutopayMandate, a *models.AutopayChargeAttempt, callErr error) error {
	st, err := Call(ctx, s.policyFor(rg, cronClass), func(ctx context.Context) (*gateway.ChargeAttemptStatus, error) {
		return rg.Gateway.FetchChargeAttemptStatus(ctx, attemptRef(m, a))
	})
	if err != nil {
		return fmt.Errorf("debit of %s has an unknown outcome and could not be verified (left debit_submitted for the sync run): %v; verification: %w", a.MerchantPaymentID, callErr, err)
	}
	if st.DebitExists {
		return s.applyChargeStatus(a.ID, st)
	}
	err = s.mutateAttempt(a.ID, func(state State) Effects {
		return s.gatewayUnavailable(state, gatewayCode(callErr), "the debit never reached the gateway: "+callErr.Error())
	})
	return err
}

// recordDebitSubmitted stores a successful submission and applies a synchronous result when the gateway gave one.
func (s *Service) recordDebitSubmitted(attemptID uuid.UUID, sub *gateway.DebitSubmission) error {
	return s.mutateAttempt(attemptID, func(st State) Effects {
		a, c := st.Attempt, st.Cycle
		now := s.now()
		// a webhook may have settled the debit while the submit call was returning: never overwrite its outcome
		if a.Status == models.ChargeAttemptStatusDebitSubmitted {
			status := string(sub.State)
			a.DebitExecutionStatus = &status
		}
		if sub.GatewayPaymentID != "" {
			a.GatewayPaymentID = strPtr(sub.GatewayPaymentID)
		}
		c.LastChargeAttemptAt = &now
		fx := Effects{Changed: true}
		switch sub.State {
		case gateway.PaymentSucceeded:
			fx = mergeEffects(fx, s.engine.OnChargeSucceeded(st, s.laterAttemptExists(a)))
		case gateway.PaymentFailed:
			fx = mergeEffects(fx, s.engine.OnChargeFailed(st, permanentFailure("debit_failed", "the gateway reported the debit as failed")))
		}
		return fx
	})
}

// temporaryRejection counts a timing-only rejection (UPI blackout window): below the limit the claim is released so
// the next debit run retries it without using up an attempt; at the limit the attempt fails and counts.
func (s *Service) temporaryRejection(st State, code, description string) Effects {
	a := st.Attempt
	a.DebitTemporaryRejectionCount++
	if a.DebitTemporaryRejectionCount >= maxTemporaryRejectionsPerChargeAttempt {
		return s.engine.OnChargeFailed(st, permanentFailure(code, description))
	}
	if a.Status == models.ChargeAttemptStatusDebitSubmitted {
		a.Status = models.ChargeAttemptStatusNotificationDelivered
	}
	return Effects{Changed: true}
}

// gatewayUnavailable handles a gateway-side failure: below the limit the claim is released for the next debit run;
// at the limit the attempt fails as gateway_unavailable and does NOT count toward the 9 or reduce the amount.
func (s *Service) gatewayUnavailable(st State, code, description string) Effects {
	a := st.Attempt
	a.DebitGatewayUnavailableRetryCount++
	if a.DebitGatewayUnavailableRetryCount >= maxGatewayUnavailableRetriesPerChargeAttempt {
		return s.engine.OnChargeFailed(st, Failure{Category: models.FailureCategoryGatewayUnavailable, Code: code, Description: description, Counts: false})
	}
	if a.Status == models.ChargeAttemptStatusDebitSubmitted {
		a.Status = models.ChargeAttemptStatusNotificationDelivered
	}
	return Effects{Changed: true}
}

// ensureMandateLive re-fetches the mandate state before a notification or a debit for gateways that do not push
// pause/cancel events. It returns false (skip tonight) when the mandate is not active or cannot be checked.
func (s *Service) ensureMandateLive(ctx context.Context, rg *ResolvedGateway, m *models.AutopayMandate) bool {
	if rg.Gateway.Capabilities().PushesMandateStatusWebhooks || m.GatewayMandateID == nil {
		return m.Status == models.MandateStatusActive
	}
	st, err := Call(ctx, s.policyFor(rg, cronClass), func(ctx context.Context) (*gateway.MandateStatus, error) {
		return rg.Gateway.FetchMandateStatus(ctx, mandateRef(m))
	})
	if err != nil {
		fmt.Printf("[autopay] cannot check mandate %s with %s, skipping tonight: %v\n", m.ID, rg.Config.Gateway, err)
		return false
	}
	if st.State == gateway.MandateActive {
		return true
	}
	if err := s.mutateMandate(m.ID, func(mm *models.AutopayMandate, latest *models.AutopayBillingCycle) Effects {
		return s.engine.OnMandateStatusChanged(mm, latest, st.State)
	}); err != nil {
		fmt.Printf("[autopay] cannot apply mandate state %s to %s: %v\n", st.State, m.ID, err)
	}
	return false
}

// ==================== Applying fetched state ====================

// applyChargeStatus applies the gateway's view of a charge attempt (used by sync and by unknown-outcome checks).
func (s *Service) applyChargeStatus(attemptID uuid.UUID, st *gateway.ChargeAttemptStatus) error {
	return s.mutateAttempt(attemptID, func(state State) Effects {
		a := state.Attempt
		fx := Effects{}
		if st.GatewayOrderID != "" && a.GatewayOrderID == nil {
			a.GatewayOrderID, fx.Changed = strPtr(st.GatewayOrderID), true
		}
		if st.GatewayPaymentID != "" && a.GatewayPaymentID == nil {
			a.GatewayPaymentID, fx.Changed = strPtr(st.GatewayPaymentID), true
		}
		if !st.ValidAfter.IsZero() {
			setMetaTime(a, "valid_after", st.ValidAfter)
			fx.Changed = true
		}
		switch {
		case st.DebitExists && st.Debit == gateway.PaymentSucceeded:
			return mergeEffects(fx, s.engine.OnChargeSucceeded(state, s.laterAttemptExists(a)))
		case st.DebitExists && st.Debit == gateway.PaymentFailed:
			if st.FailureTemporary {
				return mergeEffects(fx, s.temporaryRejection(state, st.FailureCode, st.FailureDescription))
			}
			return mergeEffects(fx, s.engine.OnChargeFailed(state, permanentFailure(st.FailureCode, st.FailureDescription)))
		}
		switch st.Notification {
		case gateway.NotificationDelivered:
			return mergeEffects(fx, s.engine.OnPreDebitNotificationDelivered(state, st.NotificationDeliveredAt))
		case gateway.NotificationFailed:
			return mergeEffects(fx, s.engine.OnPreDebitNotificationFailed(state, st.FailureCode, st.FailureDescription))
		}
		return fx
	})
}

// laterAttemptExists reports whether the cycle already has an attempt newer than a (a replacement).
func (s *Service) laterAttemptExists(a *models.AutopayChargeAttempt) bool {
	attempts, err := s.repo.FindChargeAttempts(a.BillingCycleID)
	if err != nil {
		return true // unknown: be conservative and alert on a late success
	}
	for _, other := range attempts {
		if other.ID != a.ID && other.CreatedAt.After(a.CreatedAt) {
			return true
		}
	}
	return false
}

// ==================== Sync from gateway ====================

// SyncFromGateway settles the local records with what each gateway says. It runs first every night so the notify
// crons see settled state: it confirms authorizations and one-time payments, re-sends notifications that were never
// sent, records notification outcomes and debit results missed by webhooks, and finishes unprocessed webhook events.
func (s *Service) SyncFromGateway(ctx context.Context) error {
	return s.forEachConfig(ctx, "autopay_sync_from_gateway", func(ctx context.Context, rg *ResolvedGateway) error {
		now := s.now()
		cfgID := rg.Config.ID
		name := "autopay_sync_from_gateway"

		// 1. authorizations that have not arrived; one nobody completed within a day is closed (see syncAuthorization)
		ms, err := s.repo.FindRecurringMandatesAwaitingAuthorization(cfgID, now.Add(-authorizationSyncWindow), now.Add(-10*time.Minute))
		if err != nil {
			return err
		}
		processInParallel(ms, func(m models.AutopayMandate) error { return s.syncAuthorization(ctx, rg, m) }, name)

		// 2. one-time payments not settled yet
		ots, err := s.repo.FindOneTimeChargeAttemptsAwaitingPayment(cfgID, now.Add(-7*24*time.Hour), now.Add(-5*time.Minute))
		if err != nil {
			return err
		}
		processInParallel(ots, func(a models.AutopayChargeAttempt) error { return s.syncOneTime(ctx, rg, a) }, name)

		// 3. notifications never sent: re-send, or fail the attempt when its sends are used up
		sends, err := s.repo.FindChargeAttemptsAwaitingPreDebitNotificationSend(cfgID, now.Add(-10*time.Minute), maxPreDebitNotificationSendAttempts)
		if err != nil {
			return err
		}
		processInParallel(sends, func(a models.AutopayChargeAttempt) error { return s.sendNotification(ctx, rg, a.ID) }, name)
		exhausted, err := s.repo.FindChargeAttemptsNotificationSendExhausted(cfgID, now.Add(-10*time.Minute), maxPreDebitNotificationSendAttempts)
		if err != nil {
			return err
		}
		processInParallel(exhausted, func(a models.AutopayChargeAttempt) error {
			return s.mutateAttempt(a.ID, func(st State) Effects {
				return s.engine.OnPreDebitNotificationFailed(st, "notification_send_exhausted", "the pre-debit notification could not be sent")
			})
		}, name)

		// 4. notification outcomes missed by webhooks
		outs, err := s.repo.FindChargeAttemptsAwaitingNotificationOutcome(cfgID, now.Add(-15*time.Minute))
		if err != nil {
			return err
		}
		processInParallel(outs, func(a models.AutopayChargeAttempt) error { return s.syncNotificationOutcome(ctx, rg, a) }, name)

		// 4b. a notification that is still without an outcome after the fetch above and long overdue never will get one
		overdue, err := s.repo.FindChargeAttemptsAwaitingNotificationOutcome(cfgID, now.Add(-notificationOutcomeTimeout))
		if err != nil {
			return err
		}
		processInParallel(overdue, func(a models.AutopayChargeAttempt) error {
			return s.mutateAttempt(a.ID, func(st State) Effects {
				return s.engine.OnPreDebitNotificationFailed(st, "notification_outcome_timeout", "the pre-debit notification was sent but its outcome never arrived")
			})
		}, name)

		// 5. debit results missed by webhooks
		results, err := s.repo.FindChargeAttemptsAwaitingDebitResult(cfgID, now.Add(-10*time.Minute))
		if err != nil {
			return err
		}
		processInParallel(results, func(a models.AutopayChargeAttempt) error { return s.syncDebitResult(ctx, rg, a) }, name)

		// 6. webhook events that were stored but never finished
		return s.reprocessWebhookEvents(ctx, rg)
	})
}

func (s *Service) syncAuthorization(ctx context.Context, rg *ResolvedGateway, m models.AutopayMandate) error {
	attempt, err := s.repo.FindAuthorizationChargeAttempt(m.ID)
	if err != nil {
		return err
	}
	if err := s.syncAuthorizationOrPayment(ctx, rg, &m, attempt, cronClass); err != nil {
		return err
	}
	// an authorization nobody completed within a day is closed
	if fresh, err := s.repo.FindMandateByID(m.ID); err == nil && fresh.Status == models.MandateStatusAwaitingAuthorization && fresh.CreatedAt.Before(s.now().Add(-24*time.Hour)) {
		return s.mutateMandate(m.ID, func(mm *models.AutopayMandate, latest *models.AutopayBillingCycle) Effects {
			return s.engine.OnMandateStatusChanged(mm, latest, gateway.MandateExpired)
		})
	}
	return nil
}

func (s *Service) syncOneTime(ctx context.Context, rg *ResolvedGateway, a models.AutopayChargeAttempt) error {
	_, m, err := s.loadCycleAndMandate(a.BillingCycleID)
	if err != nil {
		return err
	}
	return s.syncAuthorizationOrPayment(ctx, rg, m, &a, cronClass)
}

func (s *Service) syncNotificationOutcome(ctx context.Context, rg *ResolvedGateway, a models.AutopayChargeAttempt) error {
	_, m, err := s.loadCycleAndMandate(a.BillingCycleID)
	if err != nil {
		return err
	}
	st, err := Call(ctx, s.policyFor(rg, cronClass), func(ctx context.Context) (*gateway.PreDebitNotificationStatus, error) {
		return rg.Gateway.FetchPreDebitNotificationStatus(ctx, attemptRef(m, &a))
	})
	if err != nil {
		return err
	}
	err = s.mutateAttempt(a.ID, func(state State) Effects {
		fx := Effects{}
		if !st.ValidAfter.IsZero() {
			setMetaTime(state.Attempt, "valid_after", st.ValidAfter)
			fx.Changed = true
		}
		switch st.State {
		case gateway.NotificationDelivered:
			return mergeEffects(fx, s.engine.OnPreDebitNotificationDelivered(state, st.DeliveredAt))
		case gateway.NotificationFailed:
			return mergeEffects(fx, s.engine.OnPreDebitNotificationFailed(state, st.FailureCode, st.FailureDescription))
		}
		return fx
	})
	return err
}

func (s *Service) syncDebitResult(ctx context.Context, rg *ResolvedGateway, a models.AutopayChargeAttempt) error {
	_, m, err := s.loadCycleAndMandate(a.BillingCycleID)
	if err != nil {
		return err
	}
	st, err := Call(ctx, s.policyFor(rg, cronClass), func(ctx context.Context) (*gateway.ChargeAttemptStatus, error) {
		return rg.Gateway.FetchChargeAttemptStatus(ctx, attemptRef(m, &a))
	})
	if err != nil {
		return err
	}
	if st.DebitExists && st.Debit == gateway.PaymentPending {
		if a.UpdatedAt.Before(s.now().Add(-debitPendingAlertAfter)) {
			alert(fmt.Sprintf("autopay attempt %s has been pending at %s for over %s: check it", a.MerchantPaymentID, rg.Config.Gateway, debitPendingAlertAfter))
		}
		return nil // still with the bank
	}
	if !st.DebitExists {
		// submitted long ago but the gateway has no record: it never happened; release it like a transient failure
		return s.mutateAttempt(a.ID, func(state State) Effects {
			if state.Attempt.Status != models.ChargeAttemptStatusDebitSubmitted {
				return Effects{Ignored: "stale"}
			}
			return s.gatewayUnavailable(state, "no_gateway_record", "the gateway has no record of the submitted debit")
		})
	}
	return s.applyChargeStatus(a.ID, st)
}

// ==================== Small helpers ====================

// mergeEffects combines the effects of two steps applied in one transaction.
func mergeEffects(a, b Effects) Effects {
	out := Effects{
		Changed: a.Changed || b.Changed, Charged: a.Charged || b.Charged, AuthorizationCompleted: a.AuthorizationCompleted || b.AuthorizationCompleted,
		PaymentFailed: a.PaymentFailed || b.PaymentFailed, MandateCancelled: a.MandateCancelled || b.MandateCancelled,
		LateSuccessCorrection: a.LateSuccessCorrection || b.LateSuccessCorrection, Alert: joinAlerts(a.Alert, b.Alert),
	}
	if b.Ignored != "" && !b.Changed && !a.Changed {
		out.Ignored = b.Ignored
	}
	return out
}

func gatewayCode(err error) string {
	var ge *gateway.Error
	if errors.As(err, &ge) {
		return ge.Code
	}
	return ""
}

func setMetaTime(a *models.AutopayChargeAttempt, key string, t time.Time) {
	if a.Metadata == nil {
		a.Metadata = utils.Metadata{}
	}
	a.Metadata[key] = t.UTC().Format(time.RFC3339)
}

func metaTime(md utils.Metadata, key string) (time.Time, bool) {
	v, _ := md[key].(string)
	if v == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, v)
	return t, err == nil
}

// scheduledDebitAt is when the cycle's current attempt is meant to be debited: the retry time the cycle carries, else
// the cycle start (a first attempt is scheduled at the cycle start).
func scheduledDebitAt(c *models.AutopayBillingCycle) time.Time {
	if c.NextChargeAttemptAt != nil {
		return *c.NextChargeAttemptAt
	}
	return c.StartAt
}

// permanentFailure is a customer/bank-side failure: it counts toward the 9 attempts and drives the amount ladder.
func permanentFailure(code, description string) Failure {
	return Failure{Category: models.FailureCategoryRejectedPermanently, Code: code, Description: description, Counts: true}
}
