package models

import (
	"time"

	"github.com/google/uuid"
)

// PhoneOTP represents a one-time password for phone-based authentication
type PhoneOTP struct {
	ID          uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	AppName     string    `gorm:"size:255;not null" json:"app_name"`
	CountryCode string    `gorm:"size:5;not null" json:"country_code"`
	Phone       string    `gorm:"size:20;not null" json:"phone"`
	Value       string    `gorm:"size:6;not null" json:"-"`
	ExpiresAt   time.Time `json:"expires_at"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// PhoneOTPResponse represents the response after creating a phone OTP (without exposing the value)
type PhoneOTPResponse struct {
	ExpiresAt time.Time `json:"expires_at"`
}

// TableName sets the table name to 'phone_otp'
func (PhoneOTP) TableName() string { return "phone_otp" }

// CreatePhoneOTPRequest payload to create or override a phone OTP
type CreatePhoneOTPRequest struct {
	AppName     string `json:"app_name" binding:"required"`
	CountryCode string `json:"country_code" binding:"required"`
	Phone       string `json:"phone" binding:"required"`
}

// VerifyPhoneOTPRequest payload to verify phone OTP
type VerifyPhoneOTPRequest struct {
	AppName     string `json:"app_name" binding:"required"`
	CountryCode string `json:"country_code" binding:"required"`
	Phone       string `json:"phone" binding:"required"`
	Value       string `json:"value" binding:"required"`
}

// VerifyPhoneOTPResponse indicates verification result
type VerifyPhoneOTPResponse struct {
	Valid   bool   `json:"valid"`
	Message string `json:"message"`
}
