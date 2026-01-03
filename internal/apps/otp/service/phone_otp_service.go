package service

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"time"

	"go-backend/internal/apps/otp/models"
	"go-backend/internal/apps/otp/repository"

	"gorm.io/gorm"
)

// PhoneOTPService defines business logic for Phone OTP
type PhoneOTPService interface {
	CreateOrUpdateOTP(req models.CreatePhoneOTPRequest) (*models.PhoneOTPResponse, error)
	VerifyOTP(req models.VerifyPhoneOTPRequest) (*models.VerifyPhoneOTPResponse, error)
}

// phoneOTPService implements PhoneOTPService
type phoneOTPService struct {
	repo        repository.PhoneOTPRepository
	otpProvider OTPProvider
}

// NewPhoneOTPService creates a new instance of PhoneOTPService
func NewPhoneOTPService(repo repository.PhoneOTPRepository, provider OTPProvider) PhoneOTPService {
	return &phoneOTPService{
		repo:        repo,
		otpProvider: provider,
	}
}

// generateOTP generates a random 4-digit OTP
func generateOTP() string {
	return fmt.Sprintf("%04d", rand.Intn(10000))
}

// CreateOrUpdateOTP creates or overrides OTP for a phone number and sets expiry to 10 minutes from now
func (s *phoneOTPService) CreateOrUpdateOTP(req models.CreatePhoneOTPRequest) (*models.PhoneOTPResponse, error) {
	// Generate OTP - use fixed OTP for test phone number
	testCountryCode := os.Getenv("TEST_OTP_COUNTRY_CODE")
	testPhone := os.Getenv("TEST_OTP_PHONE")
	testOTP := os.Getenv("TEST_OTP_VALUE")

	var otpValue string
	isTestNumber := testCountryCode != "" && testPhone != "" && testOTP != "" &&
		req.CountryCode == testCountryCode && req.Phone == testPhone

	if isTestNumber {
		otpValue = testOTP
	} else {
		otpValue = generateOTP()
	}
	expiresAt := time.Now().Add(10 * time.Minute)

	if err := s.repo.Upsert(req.AppName, req.CountryCode, req.Phone, otpValue, expiresAt); err != nil {
		return nil, err
	}

	// Send OTP via provider - skip for test phone number, fail if sending fails for others
	if !isTestNumber {
		if err := s.otpProvider.SendOTP(req.CountryCode, req.Phone, req.AppName, otpValue); err != nil {
			return nil, fmt.Errorf("failed to send OTP: %w", err)
		}
	}

	return &models.PhoneOTPResponse{
		ExpiresAt: expiresAt,
	}, nil
}

// VerifyOTP verifies provided OTP value and expiry
func (s *phoneOTPService) VerifyOTP(req models.VerifyPhoneOTPRequest) (*models.VerifyPhoneOTPResponse, error) {
	otp, err := s.repo.FindByPhone(req.AppName, req.CountryCode, req.Phone)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("otp not found")
		}
		return nil, err
	}

	now := time.Now()
	// Skip expiry check for test phone number
	testCountryCode := os.Getenv("TEST_OTP_COUNTRY_CODE")
	testPhone := os.Getenv("TEST_OTP_PHONE")
	isTestNumber := testCountryCode != "" && testPhone != "" &&
		req.CountryCode == testCountryCode && req.Phone == testPhone

	var valid bool
	if isTestNumber {
		valid = req.Value == otp.Value
	} else {
		valid = (req.Value == otp.Value) && (now.Before(otp.ExpiresAt) || now.Equal(otp.ExpiresAt))
	}

	message := "OTP verified successfully"
	if !valid {
		if req.Value != otp.Value {
			message = "Invalid OTP"
		} else {
			message = "OTP expired"
		}
	}

	return &models.VerifyPhoneOTPResponse{
		Valid:   valid,
		Message: message,
	}, nil
}
