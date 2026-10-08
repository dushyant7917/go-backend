package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestGlobalRateLimitRejectsNonPositiveLimit(t *testing.T) {
	for _, limit := range []int{0, -1} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("limit %d: expected panic", limit)
				}
			}()
			GlobalRateLimit("test", limit)
		}()
	}
}

func TestGlobalRateLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)

	clock := time.Now()
	alerts := 0
	router := gin.New()
	router.Use(newGlobalRateLimit("test", 3, func() time.Time { return clock }, func(string) { alerts++ }))
	router.POST("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	call := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/x", nil))
		return w
	}

	// Spread three requests over 30s; the window is full from here on.
	for i := 0; i < 3; i++ {
		if w := call(); w.Code != http.StatusOK {
			t.Fatalf("request %d: got %d, want 200", i+1, w.Code)
		}
		clock = clock.Add(10 * time.Second)
	}

	// 30s after the first request: its slot frees in 30s.
	w := call()
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("4th request: got %d, want 429", w.Code)
	}
	if got := w.Header().Get("Retry-After"); got != "30" {
		t.Errorf("Retry-After = %q, want 30", got)
	}
	if alerts != 1 {
		t.Fatalf("alerts after first breach = %d, want 1", alerts)
	}

	call()
	if alerts != 1 {
		t.Errorf("alerts during ongoing breach = %d, want 1 (throttled)", alerts)
	}

	// Once the oldest request leaves the window, exactly one slot opens.
	clock = clock.Add(30 * time.Second)
	if w := call(); w.Code != http.StatusOK {
		t.Fatalf("after oldest expired: got %d, want 200", w.Code)
	}
	if w := call(); w.Code != http.StatusTooManyRequests {
		t.Fatalf("only one slot should have opened: got %d, want 429", w.Code)
	}

	// A sustained breach alerts again after alertInterval.
	clock = clock.Add(alertInterval)
	for i := 0; i < 3; i++ {
		call()
	}
	call()
	if alerts != 2 {
		t.Errorf("alerts after second breach = %d, want 2", alerts)
	}
}
