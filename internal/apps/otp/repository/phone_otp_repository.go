package repository

import (
	"time"

	"go-backend/internal/apps/otp/models"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PhoneOTPRepository defines data operations for Phone OTP
type PhoneOTPRepository interface {
	Upsert(appName, countryCode, phone, value string, expiresAt time.Time) error
	FindByPhone(appName, countryCode, phone string) (*models.PhoneOTP, error)
	Delete(appName, countryCode, phone string) error
}

// phoneOTPRepository implements PhoneOTPRepository
type phoneOTPRepository struct {
	db *gorm.DB
}

// NewPhoneOTPRepository creates an instance of PhoneOTPRepository
func NewPhoneOTPRepository(db *gorm.DB) PhoneOTPRepository {
	return &phoneOTPRepository{db: db}
}

// Upsert creates or updates OTP for a phone number
func (r *phoneOTPRepository) Upsert(appName, countryCode, phone, value string, expiresAt time.Time) error {
	otp := models.PhoneOTP{
		AppName:     appName,
		CountryCode: countryCode,
		Phone:       phone,
		Value:       value,
		ExpiresAt:   expiresAt,
	}
	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "app_name"}, {Name: "country_code"}, {Name: "phone"}},
		DoUpdates: clause.AssignmentColumns([]string{"value", "expires_at", "updated_at"}),
	}).Create(&otp).Error
}

// FindByPhone retrieves OTP by app name and phone number
func (r *phoneOTPRepository) FindByPhone(appName, countryCode, phone string) (*models.PhoneOTP, error) {
	var otp models.PhoneOTP
	if err := r.db.Where("app_name = ? AND country_code = ? AND phone = ?", appName, countryCode, phone).First(&otp).Error; err != nil {
		return nil, err
	}
	return &otp, nil
}

// Delete removes OTP by app name and phone number
func (r *phoneOTPRepository) Delete(appName, countryCode, phone string) error {
	return r.db.Where("app_name = ? AND country_code = ? AND phone = ?", appName, countryCode, phone).Delete(&models.PhoneOTP{}).Error
}
