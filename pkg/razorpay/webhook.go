package razorpay

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// SignWebhook computes the X-Razorpay-Signature of a webhook: HMAC-SHA256 of the raw body under the webhook secret,
// lowercase hex. It is also the test helper for building signed requests.
func SignWebhook(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyWebhookSignature reports whether signature is the HMAC-SHA256 of body under secret (constant-time). An empty
// secret never verifies, so a config without a webhook secret cannot be spoofed.
func VerifyWebhookSignature(secret string, body []byte, signature string) bool {
	if secret == "" {
		return false
	}
	got, err := hex.DecodeString(strings.TrimSpace(signature))
	if err != nil || len(got) == 0 {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}
