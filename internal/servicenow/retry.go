// Package servicenow provides a client for interacting with the ServiceNow Table API.
package servicenow

import (
	"context"
	"errors"
	"math"
	"net/http"
	"time"
)

// RetryConfig configures the retry behavior.
type RetryConfig struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
}

// DefaultRetryConfig returns the default retry configuration.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts: 3,
		BaseDelay:   1 * time.Second,
		MaxDelay:    10 * time.Second,
	}
}

// RetryableError represents an error that can be retried.
type RetryableError struct {
	Err        error
	StatusCode int
}

func (e *RetryableError) Error() string {
	return e.Err.Error()
}

func (e *RetryableError) Unwrap() error {
	return e.Err
}

// IsRetryable determines if an error should be retried. It is the single
// decision point for retry classification: WithRetry consults it directly so
// callers cannot disagree with it.
//
// Transient failures are 429 (ServiceNow rate limiting) and 5xx. Other 4xx are
// permanent — a malformed payload or a rejected credential will never succeed
// on retry, and retrying only burns the budget and delays live alerts.
// Errors that carry no status code are transport-level (connection refused,
// timeout) and are treated as transient.
func IsRetryable(err error) bool {
	var apiErr *RetryableError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusTooManyRequests || apiErr.StatusCode >= http.StatusInternalServerError
	}
	return true
}

// WithRetry executes a function with exponential backoff retry logic.
func WithRetry(ctx context.Context, cfg RetryConfig, fn func() error) error {
	var lastErr error

	for attempt := 0; attempt < cfg.MaxAttempts; attempt++ {
		lastErr = fn()
		if lastErr == nil {
			return nil
		}

		if !IsRetryable(lastErr) {
			return lastErr
		}

		// Don't sleep after the last attempt
		if attempt < cfg.MaxAttempts-1 {
			delay := calculateBackoff(attempt, cfg.BaseDelay, cfg.MaxDelay)

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
	}

	return lastErr
}

// calculateBackoff calculates the delay for a given attempt using exponential backoff.
func calculateBackoff(attempt int, baseDelay, maxDelay time.Duration) time.Duration {
	delay := time.Duration(float64(baseDelay) * math.Pow(2, float64(attempt)))
	if delay > maxDelay {
		delay = maxDelay
	}
	return delay
}
