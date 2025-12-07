package repository

import (
	"time"

	"go-backend/internal/apps/otp/models"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// EmailOTPRepository defines data operations for Email OTP
type EmailOTPRepository interface {
	Upsert(appName, email, value string, expiresAt time.Time) error
	FindByEmail(appName, email string) (*models.EmailOTP, error)
	Delete(appName, email string) error
}

// emailOTPRepository implements EmailOTPRepository
type emailOTPRepository struct {
	db *gorm.DB
}

// NewEmailOTPRepository creates an instance of EmailOTPRepository
func NewEmailOTPRepository(db *gorm.DB) EmailOTPRepository {
	return &emailOTPRepository{db: db}
}

// Upsert creates or updates OTP for an email address
func (r *emailOTPRepository) Upsert(appName, email, value string, expiresAt time.Time) error {
	otp := models.EmailOTP{
		AppName:   appName,
		Email:     email,
		Value:     value,
		ExpiresAt: expiresAt,
	}
	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "app_name"}, {Name: "email"}},
		DoUpdates: clause.AssignmentColumns([]string{"value", "expires_at", "updated_at"}),
	}).Create(&otp).Error
}

// FindByEmail retrieves OTP by app name and email address
func (r *emailOTPRepository) FindByEmail(appName, email string) (*models.EmailOTP, error) {
	var otp models.EmailOTP
	if err := r.db.Where("app_name = ? AND email = ?", appName, email).First(&otp).Error; err != nil {
		return nil, err
	}
	return &otp, nil
}

// Delete removes OTP by app name and email address
func (r *emailOTPRepository) Delete(appName, email string) error {
	return r.db.Where("app_name = ? AND email = ?", appName, email).Delete(&models.EmailOTP{}).Error
}
