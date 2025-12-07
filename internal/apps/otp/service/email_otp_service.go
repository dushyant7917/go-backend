package service

import (
	"errors"
	"fmt"
	"time"

	"go-backend/internal/apps/otp/models"
	"go-backend/internal/apps/otp/repository"

	"gorm.io/gorm"
)

// EmailOTPService defines business logic for Email OTP
type EmailOTPService interface {
	CreateOrUpdateOTP(req models.CreateEmailOTPRequest) (*models.EmailOTPResponse, error)
	VerifyOTP(req models.VerifyEmailOTPRequest) (*models.VerifyEmailOTPResponse, error)
}

// emailOTPService implements EmailOTPService
type emailOTPService struct {
	repo repository.EmailOTPRepository
}

// NewEmailOTPService creates a new instance of EmailOTPService
func NewEmailOTPService(repo repository.EmailOTPRepository) EmailOTPService {
	return &emailOTPService{
		repo: repo,
	}
}

// CreateOrUpdateOTP creates or overrides OTP for an email and sets expiry to 10 minutes from now
func (s *emailOTPService) CreateOrUpdateOTP(req models.CreateEmailOTPRequest) (*models.EmailOTPResponse, error) {
	// Generate random 4-digit OTP
	otpValue := generateOTP()
	expiresAt := time.Now().Add(10 * time.Minute)

	if err := s.repo.Upsert(req.AppName, req.Email, otpValue, expiresAt); err != nil {
		return nil, err
	}

	// TODO: Send OTP via email provider
	// When email provider is implemented, fail if sending fails
	fmt.Printf("[Email OTP Service] OTP created for %s\n", req.Email)

	return &models.EmailOTPResponse{
		ExpiresAt: expiresAt,
	}, nil
}

// VerifyOTP verifies provided OTP value and expiry
func (s *emailOTPService) VerifyOTP(req models.VerifyEmailOTPRequest) (*models.VerifyEmailOTPResponse, error) {
	otp, err := s.repo.FindByEmail(req.AppName, req.Email)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("otp not found")
		}
		return nil, err
	}

	now := time.Now()
	valid := (req.Value == otp.Value) && (now.Before(otp.ExpiresAt) || now.Equal(otp.ExpiresAt))

	message := "OTP verified successfully"
	if !valid {
		if req.Value != otp.Value {
			message = "Invalid OTP"
		} else {
			message = "OTP expired"
		}
	}

	return &models.VerifyEmailOTPResponse{
		Valid:   valid,
		Message: message,
	}, nil
}
