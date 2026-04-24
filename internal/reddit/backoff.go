package reddit

import (
	"context"
	"time"
)

// BackoffState represents persisted rate limit backoff state.
type BackoffState struct {
	Until      time.Time `json:"until"`
	RetryCount int       `json:"retry_count"`
}

// RemainingBackoff returns the duration until the backoff expires.
func (s *BackoffState) RemainingBackoff() time.Duration {
	if s == nil {
		return 0
	}
	remaining := time.Until(s.Until)
	if remaining < 0 {
		return 0
	}
	return remaining
}

// BackoffStore persists rate-limit backoff state between requests.
type BackoffStore interface {
	Load(ctx context.Context) (*BackoffState, error)
	Save(ctx context.Context, state *BackoffState) error
	Clear(ctx context.Context) error
}

// NopBackoffStore is a no-op BackoffStore (no persistence across restarts).
type NopBackoffStore struct{}

func (NopBackoffStore) Load(context.Context) (*BackoffState, error) { return nil, nil }
func (NopBackoffStore) Save(context.Context, *BackoffState) error   { return nil }
func (NopBackoffStore) Clear(context.Context) error                 { return nil }
