// Package service is the gateway-agnostic autopay core: schedules, state transitions, the call policy around gateway
// calls, the API operations, the billing crons and the webhook handler. It talks to gateways only through
// gateway.Gateway.
package service

import (
	"fmt"
	"strings"
	"time"

	"go-backend/internal/common/billingwindow"

	"github.com/google/uuid"
)

// Per-attempt limits *(decided)*. They count tries of the SAME charge attempt and are separate from the 3 HTTP tries
// per gateway call and from the 9 counted attempts per billing cycle.
const (
	// maxTemporaryRejectionsPerChargeAttempt: rejections that are only a timing problem (UPI blackout window).
	// The 2nd one fails the attempt and it counts.
	maxTemporaryRejectionsPerChargeAttempt = 2
	// maxGatewayUnavailableRetriesPerChargeAttempt: releases after a gateway-side failure. After 2 the attempt fails as
	// gateway_unavailable without counting toward the 9.
	maxGatewayUnavailableRetriesPerChargeAttempt = 2
	// maxPreDebitNotificationSendAttempts: the first send plus one re-send by the next sync run.
	maxPreDebitNotificationSendAttempts = 2

	// notificationOutcomeTimeout: a notification that was sent but never reported delivered or failed within this long
	// will not be (the debit is due within ~26h of the send): the attempt fails into the retry ladder instead of
	// blocking its cycle forever.
	notificationOutcomeTimeout = 48 * time.Hour
	// debitPendingAlertAfter: a submitted debit still pending at the gateway this long needs a human (money may move).
	debitPendingAlertAfter = 72 * time.Hour

	// authorizationSyncWindow: how far back the sync run looks for mandates still awaiting authorization. Long enough
	// that a few missed nights still end with the stale mandate being closed instead of lingering for ever.
	authorizationSyncWindow = 30 * 24 * time.Hour

	// minNotificationAgeBeforeDebit is the least age of a delivered notification before a debit (26h: above the 24h
	// the gateways require). Defined once in billingwindow.
	minNotificationAgeBeforeDebit = billingwindow.Min
	// minAuthorizationLeadTime: start_at must be at least this far out so the notify cron's window catches cycle 1.
	minAuthorizationLeadTime = billingwindow.MinAuthLead

	// mandateTenureYears is how long a recurring mandate lives after start_at; it is sent to every gateway as the
	// mandate's expiry. Official docs: Razorpay token.expire_at default 10 years, max 30; PhonePe expireAt max 30
	// years; Cashfree subscription_expiry_time states no maximum.
	mandateTenureYears = 10
)

// RetryPolicy is the retry ladder of a billing cycle. DefaultRetryPolicy holds today's numbers for INR; it is a
// struct so an app can override it later without touching the engine.
type RetryPolicy struct {
	// MaxCountedChargeAttemptsPerBillingCycle: attempts (including the first) before the cycle fails and the mandate expires.
	MaxCountedChargeAttemptsPerBillingCycle int
	// RetryIntervalDays: gap between retry attempts (T+3, T+6, T+9, ... from the cycle start).
	RetryIntervalDays int
	// Amount ladder, in minor units (paise): every attempt after the first is DeductionPerRetry less than the
	// previous one, never below MinAttemptAmount. The same for every billing frequency.
	DeductionPerRetry int64 // Rs 100 less per attempt from attempt 2
	// MinAttemptAmount is the floor of the ladder and also the smallest max_debit_amount a recurring mandate may
	// be created with (a smaller maximum could not be retried without exceeding the mandate's limit).
	MinAttemptAmount int64 // Rs 99
}

// DefaultRetryPolicy is the unified autopay retry policy for INR (the old Razorpay stack keeps its own ladder).
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxCountedChargeAttemptsPerBillingCycle: 9,
		RetryIntervalDays:                       3,
		DeductionPerRetry:                       10000,
		MinAttemptAmount:                        9900,
	}
}

// NextRetryAt is when the cycle's next charge attempt is due after countedAttempts counted attempts:
// T+3, T+6, T+9, ... days from the cycle start.
func (p RetryPolicy) NextRetryAt(cycleStart time.Time, countedAttempts int) time.Time {
	return cycleStart.AddDate(0, 0, countedAttempts*p.RetryIntervalDays).UTC()
}

// AttemptAmount returns the amount to charge for the given counted attempt number (minor units), for every billing
// frequency: attempt 1 is maxAmount, then Rs 100 less per attempt, never below Rs 99 (max(99, previous - 100)).
// Mandates are created with maxAmount >= MinAttemptAmount, so the result never exceeds maxAmount.
func (p RetryPolicy) AttemptAmount(maxAmount int64, attemptNumber int) int64 {
	if attemptNumber <= 1 {
		return maxAmount
	}
	amount := maxAmount - int64(attemptNumber-1)*p.DeductionPerRetry
	if amount < p.MinAttemptAmount {
		return min(maxAmount, p.MinAttemptAmount) // min() only guards rows created before the minimum existed
	}
	return amount
}

// ActualTenureMonths is the tenure of a yearly plan by the counted attempt that succeeded:
// attempt 1 -> 12 months, 2-3 -> 3, 4-5 -> 2, 6+ -> 1.
func ActualTenureMonths(attemptNumber int) int {
	switch {
	case attemptNumber <= 1:
		return 12
	case attemptNumber <= 3:
		return 3
	case attemptNumber <= 5:
		return 2
	default:
		return 1
	}
}

// NextChargeDueAt is the next billing date after base for a billing frequency. Unknown frequencies are an error
// (the table's CHECK constraint only admits the real intervals).
func NextChargeDueAt(frequency string, base time.Time) (time.Time, error) {
	switch frequency {
	case "daily":
		return base.AddDate(0, 0, 1).UTC(), nil
	case "weekly":
		return base.AddDate(0, 0, 7).UTC(), nil
	case "fortnightly":
		return base.AddDate(0, 0, 15).UTC(), nil
	case "bimonthly":
		return base.AddDate(0, 2, 0).UTC(), nil
	case "monthly":
		return base.AddDate(0, 1, 0).UTC(), nil
	case "quarterly":
		return base.AddDate(0, 3, 0).UTC(), nil
	case "half_yearly":
		return base.AddDate(0, 6, 0).UTC(), nil
	case "yearly":
		return base.AddDate(1, 0, 0).UTC(), nil
	default:
		return time.Time{}, fmt.Errorf("unknown billing frequency %q", frequency)
	}
}

// ==================== Identifiers ====================
//
// "merchant_*" ids are issued by us and sent to the gateway; they must be unique and gateway-safe
// (letters, digits, underscore).

// NewMerchantAutopayID is the id of a new autopay_mandates row as the gateway knows it.
func NewMerchantAutopayID() string { return "ap_" + compactUUID(uuid.New()) }

// MerchantPaymentIDFor is the id of a charge attempt. replacement is how many earlier NON-counted (gateway
// unavailable) attempts of the same number the cycle already has, so a replacement attempt that reuses its number
// still gets a unique id.
func MerchantPaymentIDFor(billingCycleID uuid.UUID, attemptNumber, replacement int) string {
	id := fmt.Sprintf("pay_%s_a%d", compactUUID(billingCycleID), attemptNumber)
	if replacement > 0 {
		id += fmt.Sprintf("_r%d", replacement)
	}
	return id
}

// AuthorizationMerchantPaymentID is the placeholder id of the cycle-0 (authorization) attempt.
func AuthorizationMerchantPaymentID(autopayMandateID uuid.UUID) string {
	return "auth_" + compactUUID(autopayMandateID)
}

// PreDebitNotificationIDFor is the id (and idempotency key) of the n-th notification send of an attempt.
func PreDebitNotificationIDFor(merchantPaymentID string, n int) string {
	return fmt.Sprintf("%s_n%d", merchantPaymentID, n)
}

// DebitExecutionIDFor is the id (and idempotency key) of the n-th debit submission of an attempt.
func DebitExecutionIDFor(merchantPaymentID string, n int) string {
	return fmt.Sprintf("%s_e%d", merchantPaymentID, n)
}

func compactUUID(id uuid.UUID) string { return strings.ReplaceAll(id.String(), "-", "") }
