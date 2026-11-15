package utils

import "unicode/utf8"

// FirstNonEmpty returns the first non-empty string, or "" when all are empty.
func FirstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// Truncate shortens s to at most n bytes without cutting a multi-byte character in half (a half character is invalid
// UTF-8, which PostgreSQL rejects), for values stored in bounded columns.
func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
