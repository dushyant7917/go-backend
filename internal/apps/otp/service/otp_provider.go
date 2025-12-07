package service

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// OTPProvider defines the interface for sending OTP via SMS
type OTPProvider interface {
	SendOTP(countryCode, phone, appName, otpValue string) error
}

// noOpProvider skips OTP sending (for local environment)
type noOpProvider struct{}

func (n *noOpProvider) SendOTP(countryCode, phone, appName, otpValue string) error {
	fmt.Printf("[OTP NoOp] Skipping SMS for %s%s, OTP: %s, App: %s\n", countryCode, phone, otpValue, appName)
	return nil
}

// NewNoOpProvider creates a no-op OTP provider
func NewNoOpProvider() OTPProvider {
	return &noOpProvider{}
}

// authKeyProvider sends OTP via AuthKey.io API
type authKeyProvider struct {
	authKey    string
	templateID string
}

func (a *authKeyProvider) SendOTP(countryCode, phone, appName, otpValue string) error {
	baseURL := "https://api.authkey.io/request"
	params := url.Values{}
	params.Add("authkey", a.authKey)
	params.Add("mobile", phone)
	params.Add("country_code", countryCode)
	params.Add("sid", a.templateID)
	params.Add("company", appName)
	params.Add("otp", otpValue)

	reqURL := fmt.Sprintf("%s?%s", baseURL, params.Encode())
	resp, err := http.Get(reqURL)
	if err != nil {
		return fmt.Errorf("failed to send OTP via AuthKey: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("AuthKey API returned status %d: %s", resp.StatusCode, string(body))
	}

	fmt.Printf("[OTP AuthKey] Sent OTP to %s%s\n", countryCode, phone)
	return nil
}

// NewAuthKeyProvider creates an AuthKey.io OTP provider
func NewAuthKeyProvider(authKey, templateID string) OTPProvider {
	return &authKeyProvider{
		authKey:    authKey,
		templateID: templateID,
	}
}
