package repository

import (
	"gorm.io/gorm"
)

// ReferralRepository defines the interface for referral data operations
type ReferralRepository interface {
	GetPendingReferralBonus(appName, referralCode string) (totalAmount int64, referralCount int, err error)
}

// referralRepository implements ReferralRepository interface
type referralRepository struct {
	db *gorm.DB
}

// NewReferralRepository creates a new instance of ReferralRepository
func NewReferralRepository(db *gorm.DB) ReferralRepository {
	return &referralRepository{db: db}
}

type referralBonusResult struct {
	ReferralCount int
	TotalAmount   int64
}

// GetPendingReferralBonus finds, for each distinct user referred by referralCode, one qualifying
// paid billing cycle created in the current calendar month: either cycle_number = 1, or
// cycle_number = 0 (authorization) with an amount of at least 9900. Each qualifying user
// contributes a flat bonus of 100 if that cycle's amount is exactly 69900, else 20.
// Each user counts at most once even if duplicate qualifying billing cycles exist.
func (r *referralRepository) GetPendingReferralBonus(appName, referralCode string) (int64, int, error) {
	var result referralBonusResult

	err := r.db.Raw(`
		SELECT COUNT(*) AS referral_count,
		       COALESCE(SUM(CASE WHEN amount = 69900 THEN 100 ELSE 20 END), 0) AS total_amount
		FROM (
			SELECT DISTINCT ON (u.id) u.id AS user_id, bc.amount
			FROM billing_cycles bc
			JOIN recurring_payments rp ON rp.id = bc.recurring_payment_id AND rp.deleted_at IS NULL
			JOIN users u ON u.id = rp.user_id AND u.deleted_at IS NULL
			WHERE u.app_name = ?
			  AND rp.app_name = ?
			  AND u.metadata->>'referral_code' = ?
			  AND bc.status = 'paid'
			  AND (bc.cycle_number = 1 OR (bc.cycle_number = 0 AND bc.amount >= 9900))
			  AND bc.created_at >= date_trunc('month', now())
			  AND bc.created_at < date_trunc('month', now()) + interval '1 month'
			ORDER BY u.id, bc.created_at ASC
		) sub
	`, appName, appName, referralCode).Scan(&result).Error

	if err != nil {
		return 0, 0, err
	}

	return result.TotalAmount, result.ReferralCount, nil
}
