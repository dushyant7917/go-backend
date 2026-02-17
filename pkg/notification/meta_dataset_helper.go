package notification

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

// HashEmail hashes an email address with SHA256 for Meta Conversions API
// Meta requires lowercase and trimmed emails before hashing
func HashEmail(email string) string {
	if email == "" {
		return ""
	}

	email = strings.ToLower(strings.TrimSpace(email))
	hash := sha256.Sum256([]byte(email))
	return hex.EncodeToString(hash[:])
}

// HashPhone hashes a phone number with SHA256 for Meta Conversions API
// Meta requires phone numbers in E.164 format (with country code, no spaces/dashes)
// Example: +919876543210
func HashPhone(phone string) string {
	if phone == "" {
		return ""
	}

	formatted := FormatPhoneE164(phone)
	if formatted == "" {
		return ""
	}

	hash := sha256.Sum256([]byte(formatted))
	return hex.EncodeToString(hash[:])
}

// FormatPhoneE164 formats a phone number into E.164 with default country code +91 (India)
// If the number already starts with + and digits, it's returned as-is
func FormatPhoneE164(phone string) string {
	phone = strings.TrimSpace(phone)
	if phone == "" {
		return ""
	}

	// Already in E.164 format
	matched, _ := regexp.MatchString(`^\+[1-9]\d{7,14}$`, phone)
	if matched {
		return phone
	}

	// Remove any non-digit characters
	replacer := regexp.MustCompile(`\D`)
	digits := replacer.ReplaceAllString(phone, "")

	if len(digits) < 8 {
		fmt.Printf("[Meta Dataset] Invalid phone number for hashing: %s\n", phone)
		return ""
	}

	// Assume default country code +91 if no country code is present
	if strings.HasPrefix(digits, "0") {
		digits = strings.TrimLeft(digits, "0")
	}

	return "+91" + digits
}

// GenerateEventID generates a deterministic event_id for deduplication using payment ID and timestamp
func GenerateEventID(razorpayPaymentID string, timestamp int64) string {
	base := fmt.Sprintf("%s:%d", razorpayPaymentID, timestamp)
	hash := sha256.Sum256([]byte(base))
	return hex.EncodeToString(hash[:])
}
