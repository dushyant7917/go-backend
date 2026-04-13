package repository

import (
	"time"

	"go-backend/internal/apps/razorpay/recurring_payment/models"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// RecurringPaymentRepository defines the interface for recurring payment data operations
type RecurringPaymentRepository interface {
	// Transaction support
	BeginTransaction() *gorm.DB
	CommitTransaction(tx *gorm.DB) error
	RollbackTransaction(tx *gorm.DB) error

	// RecurringPayment operations
	CreateRecurringPayment(tx *gorm.DB, rp *models.RecurringPayment) error
	FindRecurringPaymentByID(id uuid.UUID) (*models.RecurringPayment, error)
	FindRecurringPaymentsForNotification(windowStart, windowEnd time.Time) ([]models.RecurringPayment, error)
	FindRecurringPaymentsForNewBillingCycle(windowStart, windowEnd time.Time) ([]models.RecurringPayment, error)
	FindRecurringPaymentsForRetry(windowStart, windowEnd time.Time) ([]models.RecurringPayment, error)
	FindActiveRecurringPaymentByUserID(userID uuid.UUID, appName string) (*models.RecurringPayment, error)
	HasCompletedAuthorizationPayment(userID uuid.UUID, appName string) (bool, error)
	UpdateRecurringPayment(tx *gorm.DB, rp *models.RecurringPayment) error

	// BillingCycle operations
	CreateBillingCycle(tx *gorm.DB, bc *models.BillingCycle) error
	FindBillingCycleByID(id uuid.UUID) (*models.BillingCycle, error)
	FindBillingCyclesByRecurringPayment(recurringPaymentID uuid.UUID) ([]models.BillingCycle, error)
	FindLatestBillingCycleByRecurringPayment(recurringPaymentID uuid.UUID) (*models.BillingCycle, error)
	FindPendingBillingCyclesForRetry(retryDate time.Time) ([]models.BillingCycle, error)
	UpdateBillingCycle(tx *gorm.DB, bc *models.BillingCycle) error

	// PaymentAttempt operations
	CreatePaymentAttempt(tx *gorm.DB, pa *models.PaymentAttempt) error
	FindPaymentAttemptByOrderID(orderID string) (*models.PaymentAttempt, error)
	FindPaymentAttemptByPaymentID(paymentID string) (*models.PaymentAttempt, error)
	FindPaymentAttemptsByBillingCycle(billingCycleID uuid.UUID) ([]models.PaymentAttempt, error)
	FindPendingPaymentAttempts(chargeBefore time.Time) ([]models.PaymentAttempt, error)
	UpdatePaymentAttempt(tx *gorm.DB, pa *models.PaymentAttempt) error

	// Combined operations
	HasPendingPaymentAttemptForBillingCycle(billingCycleID uuid.UUID) (bool, error)
}

// recurringPaymentRepository implements RecurringPaymentRepository interface
type recurringPaymentRepository struct {
	db *gorm.DB
}

// NewRecurringPaymentRepository creates a new instance of RecurringPaymentRepository
func NewRecurringPaymentRepository(db *gorm.DB) RecurringPaymentRepository {
	return &recurringPaymentRepository{db: db}
}

// ==================== Transaction support ====================

// BeginTransaction starts a new database transaction
func (r *recurringPaymentRepository) BeginTransaction() *gorm.DB {
	return r.db.Begin()
}

// CommitTransaction commits a transaction
func (r *recurringPaymentRepository) CommitTransaction(tx *gorm.DB) error {
	return tx.Commit().Error
}

// RollbackTransaction rolls back a transaction
func (r *recurringPaymentRepository) RollbackTransaction(tx *gorm.DB) error {
	return tx.Rollback().Error
}

// getDB returns the transaction db if provided, otherwise returns the default db
func (r *recurringPaymentRepository) getDB(tx *gorm.DB) *gorm.DB {
	if tx != nil {
		return tx
	}
	return r.db
}

// ==================== RecurringPayment operations ====================

// CreateRecurringPayment creates a new recurring payment in the database
func (r *recurringPaymentRepository) CreateRecurringPayment(tx *gorm.DB, rp *models.RecurringPayment) error {
	return r.getDB(tx).Create(rp).Error
}

// FindRecurringPaymentByID retrieves a recurring payment by its ID
func (r *recurringPaymentRepository) FindRecurringPaymentByID(id uuid.UUID) (*models.RecurringPayment, error) {
	var rp models.RecurringPayment
	err := r.db.Where("id = ?", id).First(&rp).Error
	if err != nil {
		return nil, err
	}
	return &rp, nil
}

// FindRecurringPaymentsForNotification finds recurring payments needing pre-debit notification
// Finds payments where next_charge_at is within the notification window (48-72 hours away)
func (r *recurringPaymentRepository) FindRecurringPaymentsForNotification(windowStart, windowEnd time.Time) ([]models.RecurringPayment, error) {
	var rps []models.RecurringPayment
	err := r.db.Where("status = ? AND next_charge_at >= ? AND next_charge_at <= ?",
		models.RecurringPaymentStatusActive, windowStart, windowEnd).
		Find(&rps).Error
	if err != nil {
		return nil, err
	}
	return rps, nil
}

// FindRecurringPaymentsForNewBillingCycle finds recurring payments ready for a new billing cycle
// Only returns payments where the latest billing cycle is paid (cycle 0 authorization is always paid)
func (r *recurringPaymentRepository) FindRecurringPaymentsForNewBillingCycle(windowStart, windowEnd time.Time) ([]models.RecurringPayment, error) {
	var rps []models.RecurringPayment
	// Find active recurring payments where:
	// 1. next_charge_at is in the notification window (48-72 hours away)
	// 2. Latest billing cycle is paid (cycle 0 authorization is always paid, so first billing cycle qualifies)
	// 3. Mandate hasn't expired (end_at is null OR end_at > window_end)
	err := r.db.Raw(`
		SELECT rp.*
		FROM recurring_payments rp
		WHERE rp.status = ?
		AND rp.next_charge_at >= ?
		AND rp.next_charge_at <= ?
		AND (rp.end_at IS NULL OR rp.end_at > ?)
		AND (
			-- Latest billing cycle is paid
			SELECT bc.status
			FROM billing_cycles bc
			WHERE bc.recurring_payment_id = rp.id
			ORDER BY bc.cycle_number DESC
			LIMIT 1
		) = ?
	`, models.RecurringPaymentStatusActive, windowStart, windowEnd, windowEnd, models.BillingCycleStatusPaid).
		Scan(&rps).Error
	if err != nil {
		return nil, err
	}
	return rps, nil
}

// FindRecurringPaymentsForRetry finds active recurring payments whose latest billing cycle is pending
// and has next_attempt_at in the notification window (48-72 hours away)
func (r *recurringPaymentRepository) FindRecurringPaymentsForRetry(windowStart, windowEnd time.Time) ([]models.RecurringPayment, error) {
	var rps []models.RecurringPayment
	// Find active recurring payments where:
	// 1. Latest billing cycle status is pending
	// 2. Latest billing cycle's next_attempt_at is in the notification window (48-72 hours away)
	err := r.db.Raw(`
		SELECT rp.*
		FROM recurring_payments rp
		WHERE rp.status = ?
		AND EXISTS (
			SELECT 1 FROM billing_cycles bc
			WHERE bc.recurring_payment_id = rp.id
			AND bc.status = ?
			AND bc.next_attempt_at >= ?
			AND bc.next_attempt_at <= ?
			AND bc.cycle_number = (
				SELECT MAX(cycle_number)
				FROM billing_cycles
				WHERE recurring_payment_id = rp.id
			)
		)
	`, models.RecurringPaymentStatusActive, models.BillingCycleStatusPending, windowStart, windowEnd).
		Scan(&rps).Error
	if err != nil {
		return nil, err
	}
	return rps, nil
}

// UpdateRecurringPayment updates an existing recurring payment
func (r *recurringPaymentRepository) UpdateRecurringPayment(tx *gorm.DB, rp *models.RecurringPayment) error {
	return r.getDB(tx).Save(rp).Error
}

// FindActiveRecurringPaymentByUserID finds an active recurring payment for a user and app
func (r *recurringPaymentRepository) FindActiveRecurringPaymentByUserID(userID uuid.UUID, appName string) (*models.RecurringPayment, error) {
	var rp models.RecurringPayment
	err := r.db.Where("user_id = ? AND app_name = ? AND status = ?",
		userID, appName, models.RecurringPaymentStatusActive).
		First(&rp).Error
	if err != nil {
		return nil, err
	}
	return &rp, nil
}

// HasCompletedAuthorizationPayment checks if user has ever completed authorization
// Checks for authorized_at key in metadata (set when cycle 0 payment is captured)
func (r *recurringPaymentRepository) HasCompletedAuthorizationPayment(userID uuid.UUID, appName string) (bool, error) {
	var count int64
	err := r.db.Model(&models.RecurringPayment{}).
		Where("user_id = ? AND app_name = ?", userID, appName).
		Where("metadata ? 'authorized_at'").
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// ==================== BillingCycle operations ====================

// CreateBillingCycle creates a new billing cycle in the database
func (r *recurringPaymentRepository) CreateBillingCycle(tx *gorm.DB, bc *models.BillingCycle) error {
	return r.getDB(tx).Create(bc).Error
}

// FindBillingCycleByID retrieves a billing cycle by its ID
func (r *recurringPaymentRepository) FindBillingCycleByID(id uuid.UUID) (*models.BillingCycle, error) {
	var bc models.BillingCycle
	err := r.db.Where("id = ?", id).First(&bc).Error
	if err != nil {
		return nil, err
	}
	return &bc, nil
}

// FindBillingCyclesByRecurringPayment retrieves all billing cycles for a recurring payment
func (r *recurringPaymentRepository) FindBillingCyclesByRecurringPayment(recurringPaymentID uuid.UUID) ([]models.BillingCycle, error) {
	var bcs []models.BillingCycle
	err := r.db.Where("recurring_payment_id = ?", recurringPaymentID).
		Order("cycle_number ASC").
		Find(&bcs).Error
	if err != nil {
		return nil, err
	}
	return bcs, nil
}

// FindLatestBillingCycleByRecurringPayment retrieves the latest billing cycle for a recurring payment
func (r *recurringPaymentRepository) FindLatestBillingCycleByRecurringPayment(recurringPaymentID uuid.UUID) (*models.BillingCycle, error) {
	var bc models.BillingCycle
	err := r.db.Where("recurring_payment_id = ?", recurringPaymentID).
		Order("cycle_number DESC").
		First(&bc).Error
	if err != nil {
		return nil, err
	}
	return &bc, nil
}

// FindPendingBillingCyclesForRetry finds billing cycles that need retry processing
func (r *recurringPaymentRepository) FindPendingBillingCyclesForRetry(retryDate time.Time) ([]models.BillingCycle, error) {
	var bcs []models.BillingCycle
	err := r.db.Where("status = ? AND next_attempt_at <= ?", models.BillingCycleStatusPending, retryDate).
		Find(&bcs).Error
	if err != nil {
		return nil, err
	}
	return bcs, nil
}

// UpdateBillingCycle updates an existing billing cycle
func (r *recurringPaymentRepository) UpdateBillingCycle(tx *gorm.DB, bc *models.BillingCycle) error {
	return r.getDB(tx).Save(bc).Error
}

// ==================== PaymentAttempt operations ====================

// CreatePaymentAttempt creates a new payment attempt in the database
func (r *recurringPaymentRepository) CreatePaymentAttempt(tx *gorm.DB, pa *models.PaymentAttempt) error {
	return r.getDB(tx).Create(pa).Error
}

// FindPaymentAttemptByOrderID retrieves a payment attempt by Razorpay order ID
func (r *recurringPaymentRepository) FindPaymentAttemptByOrderID(orderID string) (*models.PaymentAttempt, error) {
	var pa models.PaymentAttempt
	err := r.db.Where("razorpay_order_id = ?", orderID).First(&pa).Error
	if err != nil {
		return nil, err
	}
	return &pa, nil
}

// FindPaymentAttemptByPaymentID retrieves a payment attempt by Razorpay payment ID
func (r *recurringPaymentRepository) FindPaymentAttemptByPaymentID(paymentID string) (*models.PaymentAttempt, error) {
	var pa models.PaymentAttempt
	err := r.db.Where("razorpay_payment_id = ?", paymentID).First(&pa).Error
	if err != nil {
		return nil, err
	}
	return &pa, nil
}

// FindPaymentAttemptsByBillingCycle retrieves all payment attempts for a billing cycle
func (r *recurringPaymentRepository) FindPaymentAttemptsByBillingCycle(billingCycleID uuid.UUID) ([]models.PaymentAttempt, error) {
	var pas []models.PaymentAttempt
	err := r.db.Where("billing_cycle_id = ?", billingCycleID).
		Order("attempt_number ASC").
		Find(&pas).Error
	if err != nil {
		return nil, err
	}
	return pas, nil
}

// FindPendingPaymentAttempts finds payment attempts where billing_cycle.next_attempt_at has passed
// Filters by status (created/pending) and joins with billing_cycles to check next_attempt_at
func (r *recurringPaymentRepository) FindPendingPaymentAttempts(chargeBefore time.Time) ([]models.PaymentAttempt, error) {
	var pas []models.PaymentAttempt
	err := r.db.Table("payment_attempts pa").
		Select("pa.*").
		Joins("JOIN billing_cycles bc ON bc.id = pa.billing_cycle_id").
		Where("pa.status IN ?", []models.PaymentAttemptStatus{models.PaymentAttemptStatusCreated, models.PaymentAttemptStatusPending}).
		Where("bc.next_attempt_at IS NOT NULL AND bc.next_attempt_at <= ?", chargeBefore).
		Find(&pas).Error
	if err != nil {
		return nil, err
	}
	return pas, nil
}

// UpdatePaymentAttempt updates an existing payment attempt
func (r *recurringPaymentRepository) UpdatePaymentAttempt(tx *gorm.DB, pa *models.PaymentAttempt) error {
	return r.getDB(tx).Save(pa).Error
}

// ==================== Combined operations ====================

// HasPendingPaymentAttemptForBillingCycle checks if there's a pending payment attempt for a billing cycle
func (r *recurringPaymentRepository) HasPendingPaymentAttemptForBillingCycle(billingCycleID uuid.UUID) (bool, error) {
	var count int64
	err := r.db.Model(&models.PaymentAttempt{}).
		Where("billing_cycle_id = ? AND status IN ?",
			billingCycleID,
			[]models.PaymentAttemptStatus{models.PaymentAttemptStatusCreated, models.PaymentAttemptStatusPending}).
		Count(&count).Error

	if err != nil {
		return false, err
	}
	return count > 0, nil
}
