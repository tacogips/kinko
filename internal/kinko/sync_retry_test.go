package kinko

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeSyncClock struct{ sleeps []time.Duration }

func (clock *fakeSyncClock) Now() time.Time { return time.Unix(0, 0) }
func (clock *fakeSyncClock) Sleep(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	clock.sleeps = append(clock.sleeps, delay)
	return nil
}

type fixedRetryClassifier struct {
	retry      bool
	retryAfter time.Duration
}

func (classifier fixedRetryClassifier) Retryable(error) (bool, time.Duration) {
	return classifier.retry, classifier.retryAfter
}

func TestWithSyncReadRetryUsesFakeClockJitterRetryAfterAndBounds(t *testing.T) {
	originalJitter := syncRetryJitter
	syncRetryJitter = func(limit time.Duration) (time.Duration, error) { return limit / 2, nil }
	t.Cleanup(func() { syncRetryJitter = originalJitter })

	clock := &fakeSyncClock{}
	attempts := 0
	value, err := withSyncReadRetry(context.Background(), syncRetryPolicy{MaxRetries: 3, InitialDelay: time.Second, MaxDelay: 4 * time.Second, TotalBudget: 10 * time.Second}, clock, fixedRetryClassifier{retry: true, retryAfter: 1500 * time.Millisecond}, func(context.Context) (string, error) {
		attempts++
		if attempts < 3 {
			return "", errors.New("transient read")
		}
		return "ok", nil
	})
	if err != nil || value != "ok" || attempts != 3 {
		t.Fatalf("retry result=(%q,%v) attempts=%d", value, err, attempts)
	}
	if len(clock.sleeps) != 2 || clock.sleeps[0] != 1500*time.Millisecond || clock.sleeps[1] != 1500*time.Millisecond {
		t.Fatalf("unexpected fake sleeps: %v", clock.sleeps)
	}

	attempts = 0
	_, err = withSyncReadRetry(context.Background(), syncRetryPolicy{MaxRetries: 3, InitialDelay: time.Second, MaxDelay: 4 * time.Second, TotalBudget: time.Second}, &fakeSyncClock{}, fixedRetryClassifier{retry: true, retryAfter: 2 * time.Second}, func(context.Context) (struct{}, error) {
		attempts++
		return struct{}{}, errors.New("still transient")
	})
	if err == nil || attempts != 1 {
		t.Fatalf("budget exhaustion err=%v attempts=%d", err, attempts)
	}

	attempts = 0
	_, err = withSyncReadRetry(context.Background(), defaultSyncRetryPolicy(), &fakeSyncClock{}, fixedRetryClassifier{}, func(context.Context) (struct{}, error) {
		attempts++
		return struct{}{}, errors.New("authentication")
	})
	if err == nil || attempts != 1 {
		t.Fatalf("non-retryable read err=%v attempts=%d", err, attempts)
	}
}

func TestSyncRetryPolicyRejectsExcessiveLimits(t *testing.T) {
	for _, policy := range []syncRetryPolicy{
		{MaxRetries: 11, InitialDelay: time.Second, MaxDelay: time.Second, TotalBudget: time.Second},
		{MaxRetries: 1, InitialDelay: time.Second, MaxDelay: 61 * time.Second, TotalBudget: time.Second},
		{MaxRetries: 1, InitialDelay: time.Second, MaxDelay: time.Second, TotalBudget: 6 * time.Minute},
	} {
		if err := validateSyncRetryPolicy(policy); err == nil {
			t.Fatalf("expected policy rejection: %+v", policy)
		}
	}
}
