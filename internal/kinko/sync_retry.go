package kinko

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	defaultSyncMaxRetries   = 5
	maximumSyncMaxRetries   = 10
	defaultSyncInitialDelay = 500 * time.Millisecond
	defaultSyncMaxDelay     = 30 * time.Second
	maximumSyncMaxDelay     = 60 * time.Second
	defaultSyncTotalBudget  = 2 * time.Minute
	maximumSyncTotalBudget  = 5 * time.Minute
)

type syncRetryPolicy struct {
	MaxRetries   int
	InitialDelay time.Duration
	MaxDelay     time.Duration
	TotalBudget  time.Duration
}

type syncRetryClassifier interface {
	Retryable(error) (bool, time.Duration)
}

type syncClock interface {
	Now() time.Time
	Sleep(context.Context, time.Duration) error
}

type realSyncClock struct{}

func (realSyncClock) Now() time.Time { return time.Now() }

func (realSyncClock) Sleep(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

var syncRetryJitter = cryptoSyncRetryJitter

type syncRetryBudget struct {
	delayed time.Duration
}

type defaultSyncRetryClassifier struct{}

func (defaultSyncRetryClassifier) Retryable(err error) (bool, time.Duration) {
	if err == nil || errors.Is(err, context.Canceled) {
		return false, 0
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, errBWSTimeout) {
		return true, 0
	}
	var networkError net.Error
	if errors.As(err, &networkError) && (networkError.Timeout() || networkError.Temporary()) {
		return true, 0
	}
	message := strings.ToLower(err.Error())
	for _, forbidden := range []string{"authentication", "unauthorized", "forbidden", "permission", "validation", "invalid", "conflict"} {
		if strings.Contains(message, forbidden) {
			return false, 0
		}
	}
	retryable := strings.Contains(message, "rate limit") || strings.Contains(message, "too many requests") || strings.Contains(message, "status 429") || strings.Contains(message, "status code 429")
	for status := 500; status <= 599 && !retryable; status++ {
		code := strconv.Itoa(status)
		retryable = strings.Contains(message, "status "+code) || strings.Contains(message, "status code "+code)
	}
	return retryable, parseSyncRetryAfter(message)
}

func parseSyncRetryAfter(message string) time.Duration {
	for _, marker := range []string{"retry-after:", "retry after"} {
		index := strings.Index(message, marker)
		if index < 0 {
			continue
		}
		fields := strings.Fields(strings.TrimSpace(message[index+len(marker):]))
		if len(fields) == 0 {
			continue
		}
		value, err := strconv.Atoi(strings.Trim(fields[0], " ,;"))
		if err == nil && value >= 0 {
			return time.Duration(value) * time.Second
		}
	}
	return 0
}

func defaultSyncRetryPolicy() syncRetryPolicy {
	return syncRetryPolicy{MaxRetries: defaultSyncMaxRetries, InitialDelay: defaultSyncInitialDelay, MaxDelay: defaultSyncMaxDelay, TotalBudget: defaultSyncTotalBudget}
}

func withSyncReadRetry[T any](ctx context.Context, policy syncRetryPolicy, clock syncClock, classifier syncRetryClassifier, operation func(context.Context) (T, error)) (T, error) {
	return withSyncReadRetryBudget(ctx, policy, clock, classifier, nil, operation)
}

func withSyncReadRetryBudget[T any](ctx context.Context, policy syncRetryPolicy, clock syncClock, classifier syncRetryClassifier, budget *syncRetryBudget, operation func(context.Context) (T, error)) (T, error) {
	var zero T
	if err := validateSyncRetryPolicy(policy); err != nil {
		return zero, err
	}
	if clock == nil || classifier == nil || operation == nil {
		return zero, errors.New("sync retry dependencies are not initialized")
	}
	localDelayed := time.Duration(0)
	for attempt := 0; ; attempt++ {
		value, err := operation(ctx)
		if err == nil {
			return value, nil
		}
		retryable, retryAfter := classifier.Retryable(err)
		if !retryable || attempt >= policy.MaxRetries {
			return zero, err
		}
		delay, jitterErr := syncRetryDelay(policy, attempt, retryAfter)
		if jitterErr != nil {
			return zero, fmt.Errorf("calculate sync retry delay: %w", jitterErr)
		}
		globalDelayed := localDelayed
		if budget != nil {
			globalDelayed = budget.delayed
		}
		if localDelayed+delay > policy.TotalBudget || globalDelayed+delay > maximumSyncTotalBudget {
			return zero, fmt.Errorf("sync retry delay budget exhausted after %s: %w", globalDelayed, err)
		}
		if sleepErr := clock.Sleep(ctx, delay); sleepErr != nil {
			return zero, fmt.Errorf("wait before sync read retry: %w", sleepErr)
		}
		localDelayed += delay
		if budget != nil {
			budget.delayed += delay
		}
	}
}

func validateSyncRetryPolicy(policy syncRetryPolicy) error {
	if policy.MaxRetries < 0 || policy.MaxRetries > maximumSyncMaxRetries {
		return fmt.Errorf("sync max retries must be between 0 and %d", maximumSyncMaxRetries)
	}
	if policy.InitialDelay <= 0 || policy.MaxDelay <= 0 || policy.InitialDelay > policy.MaxDelay || policy.MaxDelay > maximumSyncMaxDelay {
		return errors.New("sync retry delays are invalid or exceed the 60 second maximum")
	}
	if policy.TotalBudget <= 0 || policy.TotalBudget > maximumSyncTotalBudget {
		return errors.New("sync retry total budget is invalid or exceeds five minutes")
	}
	return nil
}

func syncRetryDelay(policy syncRetryPolicy, attempt int, retryAfter time.Duration) (time.Duration, error) {
	capDelay := policy.InitialDelay
	for index := 0; index < attempt && capDelay < policy.MaxDelay; index++ {
		if capDelay > policy.MaxDelay/2 {
			capDelay = policy.MaxDelay
			break
		}
		capDelay *= 2
	}
	if capDelay > policy.MaxDelay {
		capDelay = policy.MaxDelay
	}
	delay, err := syncRetryJitter(capDelay)
	if err != nil {
		return 0, err
	}
	if retryAfter > delay {
		delay = retryAfter
	}
	if delay > policy.MaxDelay {
		delay = policy.MaxDelay
	}
	return delay, nil
}

func cryptoSyncRetryJitter(limit time.Duration) (time.Duration, error) {
	if limit <= 0 {
		return 0, nil
	}
	var value [8]byte
	if _, err := rand.Read(value[:]); err != nil {
		return 0, err
	}
	return time.Duration(binary.LittleEndian.Uint64(value[:]) % uint64(limit+1)), nil
}
