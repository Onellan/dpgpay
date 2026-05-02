package models

import (
	"context"
	"errors"
	"strings"
	"time"
)

func WithWriteRetry(ctx context.Context, maxAttempts int, op func() error) error {
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := op()
		if err == nil {
			return nil
		}
		lastErr = err
		if !isSQLiteBusyError(err) || attempt == maxAttempts {
			return err
		}

		backoff := time.Duration(20*(1<<(attempt-1))) * time.Millisecond
		if backoff > 500*time.Millisecond {
			backoff = 500 * time.Millisecond
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
	}
	if lastErr != nil {
		return lastErr
	}
	return errors.New("write retry failed")
}

func isSQLiteBusyError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "database is locked") || strings.Contains(msg, "database is busy") || strings.Contains(msg, "busy")
}
