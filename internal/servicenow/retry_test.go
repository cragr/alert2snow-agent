package servicenow

import (
	"context"
	"errors"
	"testing"
)

// TestIsRetryable pins the retry classification. ServiceNow returns 429 when
// rate limiting, which is transient and must be retried; other 4xx are
// permanent and retrying them only delays live alerts.
func TestIsRetryable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"429 rate limited", &RetryableError{Err: errors.New("x"), StatusCode: 429}, true},
		{"500 server error", &RetryableError{Err: errors.New("x"), StatusCode: 500}, true},
		{"503 unavailable", &RetryableError{Err: errors.New("x"), StatusCode: 503}, true},
		{"400 bad request", &RetryableError{Err: errors.New("x"), StatusCode: 400}, false},
		{"401 unauthorized", &RetryableError{Err: errors.New("x"), StatusCode: 401}, false},
		{"403 forbidden", &RetryableError{Err: errors.New("x"), StatusCode: 403}, false},
		{"404 not found", &RetryableError{Err: errors.New("x"), StatusCode: 404}, false},
		{"transport error", errors.New("connection refused"), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsRetryable(tt.err); got != tt.want {
				t.Errorf("IsRetryable(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// TestWithRetry_RetriesRateLimit asserts WithRetry and IsRetryable agree: a 429
// must actually be retried, not short-circuited as a client error.
func TestWithRetry_RetriesRateLimit(t *testing.T) {
	cfg := RetryConfig{MaxAttempts: 3, BaseDelay: 1, MaxDelay: 2}

	var attempts int
	err := WithRetry(context.Background(), cfg, func() error {
		attempts++
		return &RetryableError{Err: errors.New("rate limited"), StatusCode: 429}
	})

	if err == nil {
		t.Fatal("expected error after exhausting attempts")
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3 (429 was not retried)", attempts)
	}
}

// TestWithRetry_DoesNotRetryClientError asserts permanent failures give up
// immediately rather than burning the retry budget.
func TestWithRetry_DoesNotRetryClientError(t *testing.T) {
	cfg := RetryConfig{MaxAttempts: 3, BaseDelay: 1, MaxDelay: 2}

	var attempts int
	err := WithRetry(context.Background(), cfg, func() error {
		attempts++
		return &RetryableError{Err: errors.New("unauthorized"), StatusCode: 401}
	})

	if err == nil {
		t.Fatal("expected error")
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1 (401 was retried)", attempts)
	}
}
