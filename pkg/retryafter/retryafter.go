// Package retryafter reads how long a gateway asks a rate-limited client to wait.
package retryafter

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// FromHeader returns the wait a gateway asks for in an HTTP 429 (or 503) response: the standard Retry-After header
// (delta-seconds or an HTTP date), else the first of the extra headers that carries a positive delta-seconds value
// (Cashfree documents x-ratelimit-retry and X-RateLimit-Reset). It returns 0 when no header says, and never a negative.
func FromHeader(h http.Header, extra ...string) time.Duration {
	return fromHeaderAt(time.Now(), h, extra...)
}

func fromHeaderAt(now time.Time, h http.Header, extra ...string) time.Duration {
	if v := strings.TrimSpace(h.Get("Retry-After")); v != "" {
		if secs, err := strconv.ParseFloat(v, 64); err == nil && secs > 0 {
			return time.Duration(secs * float64(time.Second))
		}
		if t, err := http.ParseTime(v); err == nil && t.After(now) {
			return t.Sub(now)
		}
	}
	for _, name := range extra {
		if secs, err := strconv.ParseFloat(strings.TrimSpace(h.Get(name)), 64); err == nil && secs > 0 {
			return time.Duration(secs * float64(time.Second))
		}
	}
	return 0
}
