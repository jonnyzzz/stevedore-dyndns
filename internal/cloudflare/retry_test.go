package cloudflare

import (
	"context"
	"errors"
	"testing"
	"time"
)

type timeoutError struct{}

func (timeoutError) Error() string   { return "timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

type permanentError struct{}

func (permanentError) Error() string { return "permanent" }

func TestWithRetryRetriesOnTimeout(t *testing.T) {
	origCfg := cfRetryConfig
	origSleep := cfRetrySleep
	defer func() {
		cfRetryConfig = origCfg
		cfRetrySleep = origSleep
	}()

	cfRetryConfig = retryConfig{maxRetries: 2, minDelay: 0, maxDelay: 0}
	cfRetrySleep = func(ctx context.Context, delay time.Duration) error { return nil }

	attempts := 0
	result, err := withRetry(context.Background(), "test-timeout", func() (string, error) {
		attempts++
		if attempts < 3 {
			return "", timeoutError{}
		}
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("expected retry success, got error: %v", err)
	}
	if result != "ok" {
		t.Fatalf("expected result ok, got %q", result)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
}

func TestWithRetryStopsOnPermanentError(t *testing.T) {
	origCfg := cfRetryConfig
	origSleep := cfRetrySleep
	defer func() {
		cfRetryConfig = origCfg
		cfRetrySleep = origSleep
	}()

	cfRetryConfig = retryConfig{maxRetries: 3, minDelay: 0, maxDelay: 0}
	cfRetrySleep = func(ctx context.Context, delay time.Duration) error { return nil }

	attempts := 0
	_, err := withRetry(context.Background(), "test-permanent", func() (string, error) {
		attempts++
		return "", permanentError{}
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if attempts != 1 {
		t.Fatalf("expected 1 attempt, got %d", attempts)
	}
}

func TestWithRetryHonorsContextCancel(t *testing.T) {
	origCfg := cfRetryConfig
	origSleep := cfRetrySleep
	defer func() {
		cfRetryConfig = origCfg
		cfRetrySleep = origSleep
	}()

	cfRetryConfig = retryConfig{maxRetries: 1, minDelay: 10 * time.Millisecond, maxDelay: 10 * time.Millisecond}
	cfRetrySleep = sleepWithContext

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	attempts := 0
	_, err := withRetry(ctx, "test-cancel", func() (string, error) {
		attempts++
		return "", timeoutError{}
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if attempts != 1 {
		t.Fatalf("expected 1 attempt, got %d", attempts)
	}
}
