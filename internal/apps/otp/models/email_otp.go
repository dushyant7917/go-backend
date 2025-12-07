package models

import (
	"time"

	"github.com/google/uuid"
)

// EmailOTP represents a one-time password for email-based authentication
type EmailOTP struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	AppName   string    `gorm:"size:255;not null" json:"app_name"`
	Email     string    `gorm:"size:255;not null" json:"email"`
	Value     string    `gorm:"size:6;not null" json:"-"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// EmailOTPResponse represents the response after creating an email OTP (without exposing the value)
type EmailOTPResponse struct {
	ExpiresAt time.Time `json:"expires_at"`
}

// TableName sets the table name to 'email_otp'
func (EmailOTP) TableName() string { return "email_otp" }

// CreateEmailOTPRequest payload to create or override an email OTP
type CreateEmailOTPRequest struct {
	AppName string `json:"app_name" binding:"required"`
	Email   string `json:"email" binding:"required,email"`
}

// VerifyEmailOTPRequest payload to verify email OTP
type VerifyEmailOTPRequest struct {
	AppName string `json:"app_name" binding:"required"`
	Email   string `json:"email" binding:"required,email"`
	Value   string `json:"value" binding:"required"`
}

// VerifyEmailOTPResponse indicates verification result
type VerifyEmailOTPResponse struct {
	Valid   bool   `json:"valid"`
	Message string `json:"message"`
}
