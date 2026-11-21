// Package repository is the data access layer of the autopay tables. All times are UTC. Methods that take a tx
// run inside that transaction when it is non-nil. The conditional "claim" updates are the concurrency guard: each is
// a single UPDATE ... WHERE <expected state>, so when two crons (or a cron and a webhook) race, exactly one wins.
package repository

import (
	"errors"
	"time"

	"go-backend/internal/apps/autopay/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Repository is the autopay data access interface.
type Repository interface {
	// Transactions
	BeginTransaction() *gorm.DB
	CommitTransaction(tx *gorm.DB) error
	RollbackTransaction(tx *gorm.DB) error

	// Gateway configs (credentials stay encrypted here; decrypting is the config service's job)
	FindConfigByID(id uuid.UUID) (*models.PaymentGatewayConfig, error)
	FindActiveConfig(gateway, appName, environment string) (*models.PaymentGatewayConfig, error)
	ListActiveConfigs() ([]models.PaymentGatewayConfig, error)
	ListConfigs() ([]models.PaymentGatewayConfig, error)
	CreateConfig(c *models.PaymentGatewayConfig) error
	UpdateConfig(c *models.PaymentGatewayConfig) error

	// Mandates (recurring mandates and one-time payments)
	CreateMandate(tx *gorm.DB, m *models.AutopayMandate) error
	UpdateMandate(tx *gorm.DB, m *models.AutopayMandate) error
	FindMandateByID(id uuid.UUID) (*models.AutopayMandate, error)
	FindMandateByMerchantAutopayID(merchantAutopayID string) (*models.AutopayMandate, error)
	FindMandateByGatewayMandateID(gatewayConfigID uuid.UUID, gatewayMandateID string) (*models.AutopayMandate, error)
	FindRecurringMandatesForNewBillingCycle(configID uuid.UUID, windowStart, windowEnd time.Time) ([]models.AutopayMandate, error)
	FindRecurringMandatesForRetry(configID uuid.UUID, windowStart, windowEnd time.Time) ([]models.AutopayMandate, error)
	FindRecurringMandatesAwaitingAuthorization(configID uuid.UUID, createdAfter, createdBefore time.Time) ([]models.AutopayMandate, error)
	FindMandatesPendingGatewayCancel(configID uuid.UUID) ([]models.AutopayMandate, error)

	// What the apps ask about a user
	IsSubscriptionActive(userID uuid.UUID, appName string, now time.Time) (bool, error)
	HasCompletedAuthorizationPayment(userID uuid.UUID, appName string) (bool, error)

	// Billing cycles
	CreateBillingCycle(tx *gorm.DB, c *models.AutopayBillingCycle) error
	UpdateBillingCycle(tx *gorm.DB, c *models.AutopayBillingCycle) error
	FindBillingCycleByID(id uuid.UUID) (*models.AutopayBillingCycle, error)
	FindLatestBillingCycle(autopayMandateID uuid.UUID) (*models.AutopayBillingCycle, error)
	FindBillingCycles(autopayMandateID uuid.UUID) ([]models.AutopayBillingCycle, error)

	// Charge attempts
	CreateChargeAttempt(tx *gorm.DB, a *models.AutopayChargeAttempt) error
	UpdateChargeAttempt(tx *gorm.DB, a *models.AutopayChargeAttempt) error
	UpdateChargeAttemptFields(id uuid.UUID, fields map[string]interface{}) error
	FindChargeAttemptByID(id uuid.UUID) (*models.AutopayChargeAttempt, error)
	FindChargeAttemptByMerchantPaymentID(merchantPaymentID string) (*models.AutopayChargeAttempt, error)
	FindChargeAttemptByGatewayOrderID(gatewayConfigID uuid.UUID, gatewayOrderID string) (*models.AutopayChargeAttempt, error)
	FindChargeAttemptByGatewayPaymentID(gatewayConfigID uuid.UUID, gatewayPaymentID string) (*models.AutopayChargeAttempt, error)
	FindChargeAttemptByGatewayPaymentLinkID(gatewayConfigID uuid.UUID, gatewayPaymentLinkID string) (*models.AutopayChargeAttempt, error)
	FindChargeAttempts(billingCycleID uuid.UUID) ([]models.AutopayChargeAttempt, error)
	FindAuthorizationChargeAttempt(autopayMandateID uuid.UUID) (*models.AutopayChargeAttempt, error)
	HasOpenChargeAttemptForBillingCycle(billingCycleID uuid.UUID) (bool, error)

	// Work queues of the crons
	FindChargeAttemptsReadyForDebit(configID uuid.UUID, now time.Time, minNotificationAge, minRetryGap time.Duration) ([]models.AutopayChargeAttempt, error)
	FindChargeAttemptsAwaitingPreDebitNotificationSend(configID uuid.UUID, createdBefore time.Time, maxSendAttempts int) ([]models.AutopayChargeAttempt, error)
	FindChargeAttemptsAwaitingNotificationOutcome(configID uuid.UUID, sentBefore time.Time) ([]models.AutopayChargeAttempt, error)
	FindChargeAttemptsAwaitingDebitResult(configID uuid.UUID, updatedBefore time.Time) ([]models.AutopayChargeAttempt, error)
	FindOneTimeChargeAttemptsAwaitingPayment(configID uuid.UUID, createdAfter, createdBefore time.Time) ([]models.AutopayChargeAttempt, error)
	FindChargeAttemptsNotificationSendExhausted(configID uuid.UUID, createdBefore time.Time, maxSendAttempts int) ([]models.AutopayChargeAttempt, error)

	// Row locking (call inside a transaction): serializes a webhook racing a cron on the same rows.
	LockChargeAttemptState(tx *gorm.DB, attemptID uuid.UUID) (*models.AutopayChargeAttempt, *models.AutopayBillingCycle, *models.AutopayMandate, error)
	LockMandate(tx *gorm.DB, mandateID uuid.UUID) (*models.AutopayMandate, *models.AutopayBillingCycle, error)
	CountNonCountedChargeAttempts(billingCycleID uuid.UUID, attemptNumber int) (int, error)

	// Claims: each returns true only for the single caller that won the race.
	ClaimChargeAttemptForNotificationSend(id uuid.UUID, expectedSendAttempts int, preDebitNotificationID string) (bool, error)
	ClaimChargeAttemptForDebit(id uuid.UUID, expectedSubmitAttempts int, debitExecutionID string) (bool, error)

	// Webhook events
	InsertWebhookEvent(e *models.AutopayWebhookEvent) (inserted bool, err error)
	MarkWebhookEvent(id uuid.UUID, status models.WebhookEventStatus, processingError string) error
	FindUnprocessedWebhookEvents(olderThan time.Time, limit int) ([]models.AutopayWebhookEvent, error)
	DeleteFinishedWebhookEventsBefore(cutoff time.Time, limit int) (int64, error)
}

type repository struct{ db *gorm.DB }

// New returns the GORM-backed Repository.
func New(db *gorm.DB) Repository { return &repository{db: db} }

func (r *repository) BeginTransaction() *gorm.DB { return r.db.Begin() }

func (r *repository) CommitTransaction(tx *gorm.DB) error { return tx.Commit().Error }

func (r *repository) RollbackTransaction(tx *gorm.DB) error { return tx.Rollback().Error }

func (r *repository) getDB(tx *gorm.DB) *gorm.DB {
	if tx != nil {
		return tx
	}
	return r.db
}

// ==================== Configs ====================

func (r *repository) FindConfigByID(id uuid.UUID) (*models.PaymentGatewayConfig, error) {
	var c models.PaymentGatewayConfig
	if err := r.db.Where("id = ?", id).First(&c).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

// FindActiveConfig returns the active config for a gateway + app + environment.
func (r *repository) FindActiveConfig(gateway, appName, environment string) (*models.PaymentGatewayConfig, error) {
	var c models.PaymentGatewayConfig
	err := r.db.Where("gateway = ? AND app_name = ? AND environment = ? AND is_active = true", gateway, appName, environment).First(&c).Error
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *repository) ListActiveConfigs() ([]models.PaymentGatewayConfig, error) {
	var cs []models.PaymentGatewayConfig
	err := r.db.Where("is_active = true").Order("gateway, app_name").Find(&cs).Error
	return cs, err
}

// ==================== Mandates ====================

func (r *repository) CreateMandate(tx *gorm.DB, m *models.AutopayMandate) error {
	return r.getDB(tx).Create(m).Error
}

func (r *repository) UpdateMandate(tx *gorm.DB, m *models.AutopayMandate) error {
	return r.getDB(tx).Save(m).Error
}

func (r *repository) FindMandateByID(id uuid.UUID) (*models.AutopayMandate, error) {
	var m models.AutopayMandate
	if err := r.db.Where("id = ?", id).First(&m).Error; err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *repository) FindMandateByMerchantAutopayID(merchantAutopayID string) (*models.AutopayMandate, error) {
	var m models.AutopayMandate
	if err := r.db.Where("merchant_autopay_id = ?", merchantAutopayID).First(&m).Error; err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *repository) FindMandateByGatewayMandateID(gatewayConfigID uuid.UUID, gatewayMandateID string) (*models.AutopayMandate, error) {
	var m models.AutopayMandate
	err := r.db.Where("gateway_config_id = ? AND gateway_mandate_id = ?", gatewayConfigID, gatewayMandateID).First(&m).Error
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// FindRecurringMandatesForNewBillingCycle returns active recurring mandates of the config whose next charge falls in
// the window, whose latest cycle is paid, and that do not end inside the window.
func (r *repository) FindRecurringMandatesForNewBillingCycle(configID uuid.UUID, windowStart, windowEnd time.Time) ([]models.AutopayMandate, error) {
	var ms []models.AutopayMandate
	err := r.db.Raw(`
		SELECT m.*
		  FROM autopay_mandates m
		 WHERE m.gateway_config_id = ?
		   AND m.payment_type = ?
		   AND m.status = ?
		   AND m.deleted_at IS NULL
		   AND m.next_charge_due_at >= ? AND m.next_charge_due_at <= ?
		   AND (m.end_at IS NULL OR m.end_at > ?)
		   AND (SELECT c.status FROM autopay_billing_cycles c
		         WHERE c.autopay_mandate_id = m.id ORDER BY c.cycle_number DESC LIMIT 1) = ?`,
		configID, models.PaymentTypeRecurring, models.MandateStatusActive,
		windowStart, windowEnd, windowEnd, models.BillingCycleStatusPaid,
	).Scan(&ms).Error
	return ms, err
}

// FindRecurringMandatesForRetry returns active recurring mandates of the config whose latest cycle (cycle >= 1) is
// awaiting payment with its next charge attempt in the window.
func (r *repository) FindRecurringMandatesForRetry(configID uuid.UUID, windowStart, windowEnd time.Time) ([]models.AutopayMandate, error) {
	var ms []models.AutopayMandate
	err := r.db.Raw(`
		SELECT m.*
		  FROM autopay_mandates m
		 WHERE m.gateway_config_id = ?
		   AND m.payment_type = ?
		   AND m.status = ?
		   AND m.deleted_at IS NULL
		   AND EXISTS (
		        SELECT 1 FROM autopay_billing_cycles c
		         WHERE c.autopay_mandate_id = m.id
		           AND c.status = ?
		           AND c.cycle_number >= 1
		           AND c.next_charge_attempt_at >= ? AND c.next_charge_attempt_at <= ?
		           AND c.cycle_number = (SELECT MAX(cycle_number) FROM autopay_billing_cycles WHERE autopay_mandate_id = m.id))`,
		configID, models.PaymentTypeRecurring, models.MandateStatusActive,
		models.BillingCycleStatusAwaitingPayment, windowStart, windowEnd,
	).Scan(&ms).Error
	return ms, err
}

// FindRecurringMandatesAwaitingAuthorization returns recent mandates whose authorization result has not arrived.
// One-time payments are excluded (see FindOneTimeChargeAttemptsAwaitingPayment).
func (r *repository) FindRecurringMandatesAwaitingAuthorization(configID uuid.UUID, createdAfter, createdBefore time.Time) ([]models.AutopayMandate, error) {
	var ms []models.AutopayMandate
	err := r.db.Where("gateway_config_id = ? AND payment_type = ? AND status = ? AND created_at >= ? AND created_at <= ? AND deleted_at IS NULL",
		configID, models.PaymentTypeRecurring, models.MandateStatusAwaitingAuthorization, createdAfter, createdBefore).Find(&ms).Error
	return ms, err
}

// FindMandatesPendingGatewayCancel returns the recurring mandates we retired ourselves (expired, or cancelled) whose
// gateway mandate has not been cancelled yet (metadata.gateway_cancel_pending, see models.MetadataKeyGatewayCancelPending).
func (r *repository) FindMandatesPendingGatewayCancel(configID uuid.UUID) ([]models.AutopayMandate, error) {
	var ms []models.AutopayMandate
	err := r.db.Where("gateway_config_id = ? AND payment_type = ? AND status IN ? AND metadata->>'"+models.MetadataKeyGatewayCancelPending+"' = 'true' AND deleted_at IS NULL",
		configID, models.PaymentTypeRecurring, []models.MandateStatus{models.MandateStatusExpired, models.MandateStatusCancelled}).Find(&ms).Error
	return ms, err
}

// IsSubscriptionActive: cycle 0 must be paid; cycles >= 1 may be paid or awaiting payment. Cycles are half-open
// [start_at, end_at); only recurring mandates get models.RecurringCycleGrace past end_at (a missing next cycle row
// must not cut a paying subscriber off); one-time payments end exactly at end_at.
func (r *repository) IsSubscriptionActive(userID uuid.UUID, appName string, now time.Time) (bool, error) {
	var count int64
	err := r.db.Raw(`
		SELECT COUNT(*)
		  FROM autopay_billing_cycles c
		  JOIN autopay_mandates m ON m.id = c.autopay_mandate_id
		 WHERE m.user_id = ? AND m.app_name = ? AND m.deleted_at IS NULL
		   AND c.start_at <= ? AND (c.end_at > ? OR (m.payment_type = ? AND c.end_at > ?))
		   AND ((c.cycle_number = 0 AND c.status = ?) OR (c.cycle_number >= 1 AND c.status IN ?))`,
		userID, appName, now, now, models.PaymentTypeRecurring, now.Add(-models.RecurringCycleGrace),
		models.BillingCycleStatusPaid,
		[]models.BillingCycleStatus{models.BillingCycleStatusPaid, models.BillingCycleStatusAwaitingPayment},
	).Scan(&count).Error
	return count > 0, err
}

// HasCompletedAuthorizationPayment reports whether the user ever completed a mandate authorization (a paid cycle 0
// of a recurring mandate): the "used the free trial" signal.
func (r *repository) HasCompletedAuthorizationPayment(userID uuid.UUID, appName string) (bool, error) {
	var count int64
	err := r.db.Raw(`
		SELECT COUNT(*)
		  FROM autopay_billing_cycles c
		  JOIN autopay_mandates m ON m.id = c.autopay_mandate_id
		 WHERE m.user_id = ? AND m.app_name = ? AND m.payment_type = ? AND c.cycle_number = 0 AND c.status = ?`,
		userID, appName, models.PaymentTypeRecurring, models.BillingCycleStatusPaid,
	).Scan(&count).Error
	return count > 0, err
}

// ==================== Billing cycles ====================

func (r *repository) CreateBillingCycle(tx *gorm.DB, c *models.AutopayBillingCycle) error {
	return r.getDB(tx).Create(c).Error
}

func (r *repository) UpdateBillingCycle(tx *gorm.DB, c *models.AutopayBillingCycle) error {
	return r.getDB(tx).Save(c).Error
}

func (r *repository) FindBillingCycleByID(id uuid.UUID) (*models.AutopayBillingCycle, error) {
	var c models.AutopayBillingCycle
	if err := r.db.Where("id = ?", id).First(&c).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *repository) FindLatestBillingCycle(autopayMandateID uuid.UUID) (*models.AutopayBillingCycle, error) {
	var c models.AutopayBillingCycle
	if err := r.db.Where("autopay_mandate_id = ?", autopayMandateID).Order("cycle_number DESC").First(&c).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *repository) FindBillingCycles(autopayMandateID uuid.UUID) ([]models.AutopayBillingCycle, error) {
	var cs []models.AutopayBillingCycle
	err := r.db.Where("autopay_mandate_id = ?", autopayMandateID).Order("cycle_number ASC").Find(&cs).Error
	return cs, err
}

// ==================== Charge attempts ====================

func (r *repository) CreateChargeAttempt(tx *gorm.DB, a *models.AutopayChargeAttempt) error {
	return r.getDB(tx).Create(a).Error
}

func (r *repository) UpdateChargeAttempt(tx *gorm.DB, a *models.AutopayChargeAttempt) error {
	return r.getDB(tx).Save(a).Error
}

// UpdateChargeAttemptFields updates only the given columns (and updated_at).
func (r *repository) UpdateChargeAttemptFields(id uuid.UUID, fields map[string]interface{}) error {
	fields["updated_at"] = time.Now().UTC()
	return r.db.Model(&models.AutopayChargeAttempt{}).Where("id = ?", id).Updates(fields).Error
}

func (r *repository) findAttempt(query string, args ...interface{}) (*models.AutopayChargeAttempt, error) {
	var a models.AutopayChargeAttempt
	if err := r.db.Where(query, args...).First(&a).Error; err != nil {
		return nil, err
	}
	return &a, nil
}

func (r *repository) FindChargeAttemptByID(id uuid.UUID) (*models.AutopayChargeAttempt, error) {
	return r.findAttempt("id = ?", id)
}

func (r *repository) FindChargeAttemptByMerchantPaymentID(merchantPaymentID string) (*models.AutopayChargeAttempt, error) {
	return r.findAttempt("merchant_payment_id = ?", merchantPaymentID)
}

func (r *repository) FindChargeAttemptByGatewayOrderID(gatewayConfigID uuid.UUID, gatewayOrderID string) (*models.AutopayChargeAttempt, error) {
	return r.findAttempt("gateway_config_id = ? AND gateway_order_id = ?", gatewayConfigID, gatewayOrderID)
}

func (r *repository) FindChargeAttemptByGatewayPaymentID(gatewayConfigID uuid.UUID, gatewayPaymentID string) (*models.AutopayChargeAttempt, error) {
	return r.findAttempt("gateway_config_id = ? AND gateway_payment_id = ?", gatewayConfigID, gatewayPaymentID)
}

func (r *repository) FindChargeAttemptByGatewayPaymentLinkID(gatewayConfigID uuid.UUID, gatewayPaymentLinkID string) (*models.AutopayChargeAttempt, error) {
	return r.findAttempt("gateway_config_id = ? AND gateway_payment_link_id = ?", gatewayConfigID, gatewayPaymentLinkID)
}

func (r *repository) FindChargeAttempts(billingCycleID uuid.UUID) ([]models.AutopayChargeAttempt, error) {
	var as []models.AutopayChargeAttempt
	err := r.db.Where("billing_cycle_id = ?", billingCycleID).Order("attempt_number ASC, created_at ASC").Find(&as).Error
	return as, err
}

// FindAuthorizationChargeAttempt returns the cycle-0 attempt of a mandate.
func (r *repository) FindAuthorizationChargeAttempt(autopayMandateID uuid.UUID) (*models.AutopayChargeAttempt, error) {
	var a models.AutopayChargeAttempt
	err := r.db.Table("autopay_charge_attempts a").
		Select("a.*").
		Joins("JOIN autopay_billing_cycles c ON c.id = a.billing_cycle_id").
		Where("c.autopay_mandate_id = ? AND c.cycle_number = 0", autopayMandateID).
		Order("a.attempt_number ASC, a.created_at ASC").
		First(&a).Error
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// HasOpenChargeAttemptForBillingCycle reports whether any attempt on the cycle is not yet terminal.
func (r *repository) HasOpenChargeAttemptForBillingCycle(billingCycleID uuid.UUID) (bool, error) {
	var count int64
	err := r.db.Model(&models.AutopayChargeAttempt{}).
		Where("billing_cycle_id = ? AND status IN ?", billingCycleID, openStatuses()).
		Count(&count).Error
	return count > 0, err
}

func openStatuses() []models.ChargeAttemptStatus {
	return []models.ChargeAttemptStatus{
		models.ChargeAttemptStatusAwaitingNotification,
		models.ChargeAttemptStatusNotificationDelivered,
		models.ChargeAttemptStatusDebitSubmitted,
	}
}

// ==================== Cron work queues ====================

// attemptQueue is the base of every cron work queue over charge attempts: the attempts of one config (merchant
// account) joined to their billing cycle (alias c) and, when the queue filters on the mandate, to the mandate (alias m).
func (r *repository) attemptQueue(configID uuid.UUID, withMandate bool) *gorm.DB {
	q := r.db.Table("autopay_charge_attempts a").
		Select("a.*").
		Joins("JOIN autopay_billing_cycles c ON c.id = a.billing_cycle_id").
		Where("a.gateway_config_id = ?", configID)
	if withMandate {
		q = q.Joins("JOIN autopay_mandates m ON m.id = c.autopay_mandate_id")
	}
	return q
}

// FindChargeAttemptsReadyForDebit is the debit gate: the notification is delivered and at least minNotificationAge
// old, the cycle is due, the mandate is active and, when minRetryGap is set, an attempt that was already tried (and
// released) has rested at least minRetryGap since it was released. Nothing else is ever debited.
func (r *repository) FindChargeAttemptsReadyForDebit(configID uuid.UUID, now time.Time, minNotificationAge, minRetryGap time.Duration) ([]models.AutopayChargeAttempt, error) {
	var as []models.AutopayChargeAttempt
	q := r.attemptQueue(configID, true).
		Where("a.status = ?", models.ChargeAttemptStatusNotificationDelivered).
		Where("a.pre_debit_notification_delivered_at IS NOT NULL AND a.pre_debit_notification_delivered_at <= ?", now.Add(-minNotificationAge)).
		Where("c.status = ?", models.BillingCycleStatusAwaitingPayment).
		Where("c.next_charge_attempt_at IS NOT NULL AND c.next_charge_attempt_at <= ?", now).
		Where("m.status = ? AND m.payment_type = ? AND m.deleted_at IS NULL", models.MandateStatusActive, models.PaymentTypeRecurring)
	if minRetryGap > 0 {
		// a never-tried attempt has no earlier try to wait for; a released one carries its release time in updated_at
		q = q.Where("(a.debit_submit_attempts = 0 OR a.updated_at <= ?)", now.Add(-minRetryGap))
	}
	err := q.Find(&as).Error
	return as, err
}

// FindChargeAttemptsAwaitingPreDebitNotificationSend returns attempts whose notification send has not succeeded yet
// (the process died before the call, or the call failed) and that still have send tries left.
func (r *repository) FindChargeAttemptsAwaitingPreDebitNotificationSend(configID uuid.UUID, createdBefore time.Time, maxSendAttempts int) ([]models.AutopayChargeAttempt, error) {
	return r.neverSentNotifications(configID, createdBefore, "<", maxSendAttempts)
}

// neverSentNotifications lists the attempts of active mandates whose notification was never sent successfully and whose
// send tries compare to maxSendAttempts by sendTriesOp ("<": tries left, ">=": all tries used).
func (r *repository) neverSentNotifications(configID uuid.UUID, createdBefore time.Time, sendTriesOp string, maxSendAttempts int) ([]models.AutopayChargeAttempt, error) {
	var as []models.AutopayChargeAttempt
	err := r.attemptQueue(configID, true).
		Where("a.status = ? AND a.pre_debit_notification_sent_at IS NULL AND a.pre_debit_notification_send_attempts "+sendTriesOp+" ? AND a.created_at <= ?",
			models.ChargeAttemptStatusAwaitingNotification, maxSendAttempts, createdBefore).
		Where("c.status = ? AND c.cycle_number >= 1", models.BillingCycleStatusAwaitingPayment).
		Where("m.status = ? AND m.deleted_at IS NULL", models.MandateStatusActive).
		Find(&as).Error
	return as, err
}

// FindChargeAttemptsAwaitingNotificationOutcome returns attempts whose notification was sent but whose delivery
// outcome has not been recorded.
func (r *repository) FindChargeAttemptsAwaitingNotificationOutcome(configID uuid.UUID, sentBefore time.Time) ([]models.AutopayChargeAttempt, error) {
	var as []models.AutopayChargeAttempt
	err := r.attemptQueue(configID, false).
		Where("a.status = ? AND a.pre_debit_notification_sent_at IS NOT NULL AND a.pre_debit_notification_delivered_at IS NULL AND a.pre_debit_notification_sent_at <= ?",
			models.ChargeAttemptStatusAwaitingNotification, sentBefore).
		Where("c.status = ? AND c.cycle_number >= 1", models.BillingCycleStatusAwaitingPayment).
		Find(&as).Error
	return as, err
}

// FindChargeAttemptsAwaitingDebitResult returns attempts whose debit was submitted but has no result yet.
func (r *repository) FindChargeAttemptsAwaitingDebitResult(configID uuid.UUID, updatedBefore time.Time) ([]models.AutopayChargeAttempt, error) {
	var as []models.AutopayChargeAttempt
	err := r.attemptQueue(configID, false).
		Where("a.status = ? AND a.updated_at <= ?", models.ChargeAttemptStatusDebitSubmitted, updatedBefore).
		Where("c.status = ? AND c.cycle_number >= 1", models.BillingCycleStatusAwaitingPayment). // cycle 0 (authorization) is synced through the mandate
		Find(&as).Error
	return as, err
}

// FindOneTimeChargeAttemptsAwaitingPayment returns the single attempt of one-time payments that is not settled yet:
// still awaiting_notification (the order/link was created), or debit_failed while the order/link is still open (a
// customer can retry on the same order, so a failed attempt can still be paid).
func (r *repository) FindOneTimeChargeAttemptsAwaitingPayment(configID uuid.UUID, createdAfter, createdBefore time.Time) ([]models.AutopayChargeAttempt, error) {
	var as []models.AutopayChargeAttempt
	err := r.attemptQueue(configID, true).
		Where("m.payment_type = ? AND m.status = ? AND m.deleted_at IS NULL", models.PaymentTypeOneTime, models.MandateStatusAwaitingAuthorization).
		Where("c.cycle_number = 0 AND c.status = ?", models.BillingCycleStatusAwaitingPayment).
		Where("a.status IN ?", []models.ChargeAttemptStatus{models.ChargeAttemptStatusAwaitingNotification, models.ChargeAttemptStatusDebitFailed}).
		Where("a.created_at >= ? AND a.created_at <= ?", createdAfter, createdBefore).
		Find(&as).Error
	return as, err
}

// ==================== Claims ====================

// ClaimChargeAttemptForNotificationSend records a send try before the gateway is called, so overlapping runs never
// send the same notification twice.
func (r *repository) ClaimChargeAttemptForNotificationSend(id uuid.UUID, expectedSendAttempts int, preDebitNotificationID string) (bool, error) {
	res := r.db.Model(&models.AutopayChargeAttempt{}).
		Where("id = ? AND status = ? AND pre_debit_notification_send_attempts = ?", id, models.ChargeAttemptStatusAwaitingNotification, expectedSendAttempts).
		Updates(map[string]interface{}{
			"pre_debit_notification_send_attempts": expectedSendAttempts + 1,
			"pre_debit_notification_id":            preDebitNotificationID,
			"updated_at":                           time.Now().UTC(),
		})
	return res.RowsAffected == 1, res.Error
}

// ClaimChargeAttemptForDebit moves notification_delivered -> debit_submitted before the gateway is called, so only
// one runner (the 3:30 and 5:20 runs, a retry) can debit an attempt.
func (r *repository) ClaimChargeAttemptForDebit(id uuid.UUID, expectedSubmitAttempts int, debitExecutionID string) (bool, error) {
	res := r.db.Model(&models.AutopayChargeAttempt{}).
		Where("id = ? AND status = ? AND debit_submit_attempts = ?", id, models.ChargeAttemptStatusNotificationDelivered, expectedSubmitAttempts).
		Updates(map[string]interface{}{
			"status":                models.ChargeAttemptStatusDebitSubmitted,
			"debit_submit_attempts": expectedSubmitAttempts + 1,
			"debit_execution_id":    debitExecutionID,
			"updated_at":            time.Now().UTC(),
		})
	return res.RowsAffected == 1, res.Error
}

// ==================== Webhook events ====================

// InsertWebhookEvent stores a verified event. inserted=false means the same (config, dedupe key) was already
// received: the caller acknowledges it and stops.
func (r *repository) InsertWebhookEvent(e *models.AutopayWebhookEvent) (bool, error) {
	res := r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "gateway_config_id"}, {Name: "event_dedupe_key"}},
		DoNothing: true,
	}).Create(e)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

func (r *repository) MarkWebhookEvent(id uuid.UUID, status models.WebhookEventStatus, processingError string) error {
	fields := map[string]interface{}{"status": status, "processed_at": time.Now().UTC()}
	if processingError != "" {
		fields["processing_error"] = processingError
	}
	res := r.db.Model(&models.AutopayWebhookEvent{}).Where("id = ?", id).Updates(fields)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errors.New("autopay webhook event not found")
	}
	return nil
}

// FindUnprocessedWebhookEvents returns events still received/refetch_required/failed that are older than olderThan.
func (r *repository) FindUnprocessedWebhookEvents(olderThan time.Time, limit int) ([]models.AutopayWebhookEvent, error) {
	var es []models.AutopayWebhookEvent
	err := r.db.Where("status IN ? AND received_at <= ?",
		[]models.WebhookEventStatus{models.WebhookEventStatusReceived, models.WebhookEventStatusRefetchRequired, models.WebhookEventStatusFailed}, olderThan).
		Order("received_at ASC").Limit(limit).Find(&es).Error
	return es, err
}

// DeleteFinishedWebhookEventsBefore is the retention job: it hard-deletes processed and ignored events received
// before cutoff (at most limit rows per call). Unfinished events are never deleted.
func (r *repository) DeleteFinishedWebhookEventsBefore(cutoff time.Time, limit int) (int64, error) {
	res := r.db.Exec(`
		DELETE FROM autopay_webhook_events
		 WHERE id IN (SELECT id FROM autopay_webhook_events
		               WHERE status IN (?, ?) AND received_at < ? LIMIT ?)`,
		models.WebhookEventStatusProcessed, models.WebhookEventStatusIgnoredUnknownEntity, cutoff, limit)
	return res.RowsAffected, res.Error
}

// ==================== Row locking and small helpers ====================

// LockChargeAttemptState loads an attempt, its cycle and its mandate with SELECT ... FOR UPDATE, always in that
// order so concurrent transactions cannot deadlock. tx must be a transaction.
func (r *repository) LockChargeAttemptState(tx *gorm.DB, attemptID uuid.UUID) (*models.AutopayChargeAttempt, *models.AutopayBillingCycle, *models.AutopayMandate, error) {
	var a models.AutopayChargeAttempt
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", attemptID).First(&a).Error; err != nil {
		return nil, nil, nil, err
	}
	var c models.AutopayBillingCycle
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", a.BillingCycleID).First(&c).Error; err != nil {
		return nil, nil, nil, err
	}
	var m models.AutopayMandate
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", c.AutopayMandateID).First(&m).Error; err != nil {
		return nil, nil, nil, err
	}
	return &a, &c, &m, nil
}

// LockMandate loads a mandate and its latest cycle (nil when it has none) with SELECT ... FOR UPDATE.
func (r *repository) LockMandate(tx *gorm.DB, mandateID uuid.UUID) (*models.AutopayMandate, *models.AutopayBillingCycle, error) {
	var m models.AutopayMandate
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", mandateID).First(&m).Error; err != nil {
		return nil, nil, err
	}
	var c models.AutopayBillingCycle
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("autopay_mandate_id = ?", mandateID).Order("cycle_number DESC").First(&c).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &m, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return &m, &c, nil
}

// CountNonCountedChargeAttempts counts the gateway_unavailable attempts of a cycle that have the given number, so a
// replacement attempt that reuses the number still gets a unique merchant payment id.
func (r *repository) CountNonCountedChargeAttempts(billingCycleID uuid.UUID, attemptNumber int) (int, error) {
	var n int64
	err := r.db.Model(&models.AutopayChargeAttempt{}).
		Where("billing_cycle_id = ? AND attempt_number = ? AND counts_toward_attempt_limit = false", billingCycleID, attemptNumber).
		Count(&n).Error
	return int(n), err
}

// FindChargeAttemptsNotificationSendExhausted returns attempts whose notification was never sent although they used
// all their send tries; they fail into the retry ladder.
func (r *repository) FindChargeAttemptsNotificationSendExhausted(configID uuid.UUID, createdBefore time.Time, maxSendAttempts int) ([]models.AutopayChargeAttempt, error) {
	return r.neverSentNotifications(configID, createdBefore, ">=", maxSendAttempts)
}

// ==================== Config administration ====================

func (r *repository) ListConfigs() ([]models.PaymentGatewayConfig, error) {
	var cs []models.PaymentGatewayConfig
	err := r.db.Order("gateway, app_name, environment").Find(&cs).Error
	return cs, err
}

func (r *repository) CreateConfig(c *models.PaymentGatewayConfig) error { return r.db.Create(c).Error }

func (r *repository) UpdateConfig(c *models.PaymentGatewayConfig) error { return r.db.Save(c).Error }
