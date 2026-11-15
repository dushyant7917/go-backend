package retryafter

import (
	"net/http"
	"testing"
	"time"
)

func TestFromHeader(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	h := func(kv ...string) http.Header {
		out := http.Header{}
		for i := 0; i < len(kv); i += 2 {
			out.Set(kv[i], kv[i+1])
		}
		return out
	}
	cases := []struct {
		name  string
		h     http.Header
		extra []string
		want  time.Duration
	}{
		{"seconds", h("Retry-After", "7"), nil, 7 * time.Second},
		{"http date", h("Retry-After", now.Add(30*time.Second).Format(http.TimeFormat)), nil, 30 * time.Second},
		{"date in the past", h("Retry-After", now.Add(-time.Minute).Format(http.TimeFormat)), nil, 0},
		{"cashfree header", h("x-ratelimit-retry", "12"), []string{"x-ratelimit-retry", "x-ratelimit-reset"}, 12 * time.Second},
		{"zero means none (cashfree sends 0 on every success)", h("x-ratelimit-retry", "0"), []string{"x-ratelimit-retry"}, 0},
		{"retry-after wins over extra", h("Retry-After", "3", "x-ratelimit-retry", "99"), []string{"x-ratelimit-retry"}, 3 * time.Second},
		{"garbage", h("Retry-After", "soon"), nil, 0},
		{"nothing", http.Header{}, []string{"x-ratelimit-retry"}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := fromHeaderAt(now, c.h, c.extra...); got != c.want {
				t.Fatalf("got %v want %v", got, c.want)
			}
		})
	}
}
