package cashfree

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
)

// ErrInvalidSignature is returned when a webhook signature does not match.
var ErrInvalidSignature = errors.New("invalid cashfree webhook signature")

// VerifyWebhookSignature checks x-webhook-signature: base64(HMAC-SHA256(timestamp + rawBody, clientSecret)).
// rawBody must be the exact bytes received (do not re-serialize parsed JSON).
func VerifyWebhookSignature(clientSecret, timestamp string, rawBody []byte, signature string) error {
	if clientSecret == "" || signature == "" {
		return ErrInvalidSignature
	}
	mac := hmac.New(sha256.New, []byte(clientSecret))
	mac.Write([]byte(timestamp))
	mac.Write(rawBody)
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(signature)) {
		return ErrInvalidSignature
	}
	return nil
}

// SignWebhook computes the signature Cashfree would send; used by tests.
func SignWebhook(clientSecret, timestamp string, rawBody []byte) string {
	mac := hmac.New(sha256.New, []byte(clientSecret))
	mac.Write([]byte(timestamp))
	mac.Write(rawBody)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}
