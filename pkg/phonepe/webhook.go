package phonepe

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
)

// SignWebhook computes the signature PhonePe puts on a webhook: HMAC-SHA256 of the raw request body under the
// webhook's secret key, lowercase hex. It is also the test helper for building signed requests.
func SignWebhook(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyWebhookSignature reports whether signature is the HMAC-SHA256 of body under secret. The comparison is
// constant-time. PhonePe's docs name neither the hash nor the encoding, so the signature is accepted as hex (any
// case) or base64, with an optional "sha256=" prefix; both encodings are of the same MAC, so this does not weaken
// the check. An empty secret never verifies, so an app without a configured secret can't be spoofed.
func VerifyWebhookSignature(secret string, body []byte, signature string) bool {
	if secret == "" {
		return false
	}
	sig := strings.TrimSpace(signature)
	if len(sig) >= 7 && strings.EqualFold(sig[:7], "sha256=") {
		sig = sig[7:]
	}
	if sig == "" {
		return false
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := mac.Sum(nil)

	if got, err := hex.DecodeString(sig); err == nil && hmac.Equal(got, want) {
		return true
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.RawURLEncoding} {
		if got, err := enc.DecodeString(sig); err == nil && hmac.Equal(got, want) {
			return true
		}
	}
	return false
}
