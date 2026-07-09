package service

import (
	"encoding/json"
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

// twoFactorProvider sends OTP via 2factor.in API
type twoFactorProvider struct {
	apiKey       string
	templateName string
}

func (t *twoFactorProvider) SendOTP(countryCode, phone, _ /* appName */, otpValue string) error {
	reqURL := fmt.Sprintf("https://2factor.in/API/V1/%s/SMS/%s/%s/%s",
		url.PathEscape(t.apiKey),
		url.PathEscape(phone),
		url.PathEscape(otpValue),
		url.PathEscape(t.templateName),
	)

	resp, err := http.Get(reqURL)
	if err != nil {
		return fmt.Errorf("failed to send OTP via 2Factor: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("2Factor API returned status %d: %s", resp.StatusCode, string(body))
	}

	// 2Factor returns HTTP 200 even on errors; check the Status field.
	var result struct {
		Status  string `json:"Status"`
		Details string `json:"Details"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("2Factor API unexpected response: %s", string(body))
	}
	if result.Status != "Success" {
		return fmt.Errorf("2Factor API error: %s", result.Details)
	}

	fmt.Printf("[OTP 2Factor] Sent OTP to %s%s\n", countryCode, phone)
	return nil
}

// NewTwoFactorProvider creates a 2factor.in OTP provider.
// templateName is the SMS template registered in your 2Factor account.
func NewTwoFactorProvider(apiKey, templateName string) OTPProvider {
	return &twoFactorProvider{
		apiKey:       apiKey,
		templateName: templateName,
	}
}
