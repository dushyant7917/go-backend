package utils

import (
	"sync"
	"time"
)

// RateLimiter implements a sliding window rate limiter
type RateLimiter struct {
	timestamps []time.Time
	mutex      sync.Mutex
	window     time.Duration
	limit      int
}

// NewRateLimiter creates a new rate limiter with the specified limit per time window
func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		timestamps: make([]time.Time, 0, limit),
		window:     window,
		limit:      limit,
	}
}

// Wait blocks until a request can be made within the rate limit
func (r *RateLimiter) Wait() {
	for {
		r.mutex.Lock()

		now := time.Now()
		cutoff := now.Add(-r.window)

		// Remove timestamps outside the window and free memory
		validIdx := 0
		for i, ts := range r.timestamps {
			if ts.After(cutoff) {
				validIdx = i
				break
			}
		}
		// Create new slice to free old entries (prevent memory leak)
		if validIdx > 0 {
			newTimestamps := make([]time.Time, len(r.timestamps)-validIdx)
			copy(newTimestamps, r.timestamps[validIdx:])
			r.timestamps = newTimestamps
		}

		// If at limit, wait until oldest request expires and retry
		if len(r.timestamps) >= r.limit {
			sleepDuration := r.timestamps[0].Add(r.window).Sub(now)
			if sleepDuration > 0 {
				r.mutex.Unlock()
				time.Sleep(sleepDuration)
				continue
			}
			// If sleep duration is negative or zero, just continue without sleeping
		}

		// Record this request
		r.timestamps = append(r.timestamps, time.Now())
		r.mutex.Unlock()
		return
	}
}
