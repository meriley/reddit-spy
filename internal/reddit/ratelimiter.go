package reddit

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// RateLimiter provides thread-safe rate limiting for Reddit API requests.
// It enforces a minimum delay between consecutive requests across all clients
// that share the same limiter instance.
type RateLimiter struct {
	mu          sync.Mutex
	lastRequest time.Time
	minDelay    time.Duration
}

// NewRateLimiter creates a new rate limiter with the specified minimum delay
// between requests.
func NewRateLimiter(minDelay time.Duration) *RateLimiter {
	return &RateLimiter{
		minDelay: minDelay,
	}
}

// WaitContext blocks until enough time has passed since the last request,
// or the context is cancelled. Returns ctx.Err() if cancelled.
func (r *RateLimiter) WaitContext(ctx context.Context) error {
	for {
		r.mu.Lock()
		elapsed := time.Since(r.lastRequest)
		if elapsed >= r.minDelay {
			r.lastRequest = time.Now()
			r.mu.Unlock()
			return nil
		}
		waitDuration := r.minDelay - elapsed
		r.mu.Unlock()

		timer := time.NewTimer(waitDuration)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// WaitWithDynamic waits using a dynamically calculated delay.
// The dynamic delay overrides minDelay if it is greater.
func (r *RateLimiter) WaitWithDynamic(dynamicDelay time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delay := max(r.minDelay, dynamicDelay)

	elapsed := time.Since(r.lastRequest)
	if elapsed < delay {
		time.Sleep(delay - elapsed)
	}
	r.lastRequest = time.Now()
}

// SetDelay updates the minimum delay between requests.
func (r *RateLimiter) SetDelay(d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.minDelay != d {
		slog.Debug("rate limiter delay updated",
			"old_ms", r.minDelay.Milliseconds(),
			"new_ms", d.Milliseconds(),
		)
		r.minDelay = d
	}
}
