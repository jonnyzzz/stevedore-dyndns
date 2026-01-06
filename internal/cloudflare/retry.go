package cloudflare

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"time"
)

type retryConfig struct {
	maxRetries int
	minDelay   time.Duration
	maxDelay   time.Duration
}

var cfRetryConfig = retryConfig{
	maxRetries: 1,
	minDelay:   500 * time.Millisecond,
	maxDelay:   5 * time.Second,
}

var cfRetrySleep = sleepWithContext

func withRetry[T any](ctx context.Context, operation string, fn func() (T, error)) (T, error) {
	var zero T
	var err error

	for attempt := 0; attempt <= cfRetryConfig.maxRetries; attempt++ {
		var result T
		result, err = fn()
		if err == nil {
			return result, nil
		}
		if !isRetryableError(err) || attempt == cfRetryConfig.maxRetries {
			return zero, err
		}

		delay := retryDelay(attempt, cfRetryConfig.minDelay, cfRetryConfig.maxDelay)
		slog.Warn("Cloudflare API call failed, retrying",
			"operation", operation,
			"attempt", attempt+1,
			"delay", delay,
			"error", err,
		)

		if sleepErr := cfRetrySleep(ctx, delay); sleepErr != nil {
			return zero, sleepErr
		}
	}

	return zero, err
}

func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}

	return false
}

func retryDelay(attempt int, minDelay, maxDelay time.Duration) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	backoff := minDelay * time.Duration(1<<attempt)
	if backoff > maxDelay {
		return maxDelay
	}
	return backoff
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
