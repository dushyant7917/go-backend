package middleware

import (
	"fmt"
	"log"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/gin-gonic/gin"
)

// rateWindow is the rolling window over which the request cap applies.
const rateWindow = time.Minute

// alertInterval is the minimum gap between Sentry alerts while a limit stays breached.
const alertInterval = time.Minute

// GlobalRateLimit caps the total requests in any rolling one-minute window passing through
// the middleware, regardless of client. It is in-memory, so the cap applies per server instance.
// Sentry is alerted when the limit first trips and at most once per minute afterwards.
func GlobalRateLimit(name string, perMinute int) gin.HandlerFunc {
	return newGlobalRateLimit(name, perMinute, time.Now, func(msg string) {
		sentry.WithScope(func(scope *sentry.Scope) {
			scope.SetLevel(sentry.LevelWarning)
			scope.SetTag("limiter", name)
			sentry.CaptureMessage(msg)
		})
	})
}

func newGlobalRateLimit(name string, perMinute int, now func() time.Time, alert func(string)) gin.HandlerFunc {
	if perMinute <= 0 {
		// Fail at startup: a non-positive limit would reject every request.
		panic(fmt.Sprintf("rate limiter %q: perMinute must be positive, got %d", name, perMinute))
	}

	var mu sync.Mutex
	var hits []time.Time // timestamps of accepted requests within the window, oldest first
	var lastAlert time.Time

	return func(c *gin.Context) {
		t := now()

		mu.Lock()
		cutoff := t.Add(-rateWindow)
		drop := 0
		for drop < len(hits) && !hits[drop].After(cutoff) {
			drop++
		}
		hits = hits[drop:]

		if len(hits) < perMinute {
			hits = append(hits, t)
			mu.Unlock()
			c.Next()
			return
		}

		retryAfter := hits[0].Add(rateWindow).Sub(t)
		shouldAlert := lastAlert.IsZero() || t.Sub(lastAlert) >= alertInterval
		if shouldAlert {
			lastAlert = t
		}
		mu.Unlock()

		if shouldAlert {
			msg := fmt.Sprintf("rate limit %q breached: more than %d requests/min", name, perMinute)
			log.Println(msg)
			alert(msg)
		}

		seconds := int(math.Ceil(retryAfter.Seconds()))
		if seconds < 1 {
			seconds = 1
		}
		c.Header("Retry-After", strconv.Itoa(seconds))
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "too many requests"})
	}
}
