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

// IsRetryable determines if an error should be retried.
func IsRetryable(err error) bool {
	var retryableErr *RetryableError
	if errors.As(err, &retryableErr) {
		// Retry on 5xx server errors
		return retryableErr.StatusCode >= 500
	}
	// Retry on connection errors
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

		// Check if error is retryable
		var retryableErr *RetryableError
		if errors.As(lastErr, &retryableErr) {
			// Don't retry 4xx client errors
			if retryableErr.StatusCode >= 400 && retryableErr.StatusCode < 500 {
				return lastErr
			}
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

// IsClientError checks if the status code indicates a client error (4xx).
func IsClientError(statusCode int) bool {
	return statusCode >= http.StatusBadRequest && statusCode < http.StatusInternalServerError
}

// IsServerError checks if the status code indicates a server error (5xx).
func IsServerError(statusCode int) bool {
	return statusCode >= http.StatusInternalServerError
}
