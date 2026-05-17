package circuitbreaker_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/trading-bot/bot-engine/internal/infrastructure/circuitbreaker"
)

func newTestCB(failThreshold, successThreshold int, timeout time.Duration) *circuitbreaker.CircuitBreaker {
	return circuitbreaker.New("test", circuitbreaker.Config{
		FailureThreshold: failThreshold,
		SuccessThreshold: successThreshold,
		Timeout:          timeout,
	}, zap.NewNop())
}

func alwaysFail(_ context.Context) error  { return errors.New("boom") }
func alwaysSucceed(_ context.Context) error { return nil }

func TestCircuitBreaker_InitialStateClosed(t *testing.T) {
	cb := newTestCB(3, 2, time.Second)
	assert.Equal(t, circuitbreaker.StateClosed, cb.State())
}

func TestCircuitBreaker_OpensAfterThreshold(t *testing.T) {
	cb := newTestCB(3, 2, time.Second)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		_ = cb.Execute(ctx, alwaysFail)
	}

	assert.Equal(t, circuitbreaker.StateOpen, cb.State())
}

func TestCircuitBreaker_RejectsWhenOpen(t *testing.T) {
	cb := newTestCB(1, 2, time.Hour) // very long timeout
	ctx := context.Background()

	_ = cb.Execute(ctx, alwaysFail)
	require.Equal(t, circuitbreaker.StateOpen, cb.State())

	err := cb.Execute(ctx, alwaysSucceed)
	assert.ErrorIs(t, err, circuitbreaker.ErrCircuitOpen)
}

func TestCircuitBreaker_TransitionsToHalfOpenAfterTimeout(t *testing.T) {
	cb := newTestCB(1, 2, 50*time.Millisecond)
	ctx := context.Background()

	_ = cb.Execute(ctx, alwaysFail)
	require.Equal(t, circuitbreaker.StateOpen, cb.State())

	time.Sleep(100 * time.Millisecond)

	// First execute after timeout should be allowed (HalfOpen probe)
	err := cb.Execute(ctx, alwaysSucceed)
	assert.NoError(t, err)
}

func TestCircuitBreaker_ClosesAfterSuccessThresholdInHalfOpen(t *testing.T) {
	cb := newTestCB(1, 2, 50*time.Millisecond)
	ctx := context.Background()

	_ = cb.Execute(ctx, alwaysFail)
	time.Sleep(100 * time.Millisecond)

	// Two successes in HalfOpen → Closed
	_ = cb.Execute(ctx, alwaysSucceed)
	_ = cb.Execute(ctx, alwaysSucceed)

	assert.Equal(t, circuitbreaker.StateClosed, cb.State())
}

func TestCircuitBreaker_ReopensOnFailureInHalfOpen(t *testing.T) {
	cb := newTestCB(1, 3, 50*time.Millisecond)
	ctx := context.Background()

	_ = cb.Execute(ctx, alwaysFail)
	time.Sleep(100 * time.Millisecond)

	// Failure during probe → reopen
	_ = cb.Execute(ctx, alwaysFail)
	assert.Equal(t, circuitbreaker.StateOpen, cb.State())
}

func TestCircuitBreaker_ResetsFailureCountOnSuccess(t *testing.T) {
	cb := newTestCB(3, 2, time.Second)
	ctx := context.Background()

	// 2 failures, then success — should reset count
	_ = cb.Execute(ctx, alwaysFail)
	_ = cb.Execute(ctx, alwaysFail)
	_ = cb.Execute(ctx, alwaysSucceed)

	// One more failure should NOT open (count was reset)
	_ = cb.Execute(ctx, alwaysFail)
	assert.Equal(t, circuitbreaker.StateClosed, cb.State())
}

func TestCircuitBreaker_DefaultConfig(t *testing.T) {
	cfg := circuitbreaker.DefaultConfig()
	assert.Equal(t, 5, cfg.FailureThreshold)
	assert.Equal(t, 2, cfg.SuccessThreshold)
	assert.Equal(t, 30*time.Second, cfg.Timeout)
}

func TestCircuitBreaker_PassesThroughResult(t *testing.T) {
	cb := newTestCB(3, 2, time.Second)
	ctx := context.Background()

	expectedErr := errors.New("specific error")
	err := cb.Execute(ctx, func(_ context.Context) error {
		return expectedErr
	})
	assert.Equal(t, expectedErr, err)
}

func TestCircuitBreaker_NilErrorOnSuccess(t *testing.T) {
	cb := newTestCB(3, 2, time.Second)
	err := cb.Execute(context.Background(), alwaysSucceed)
	assert.NoError(t, err)
}
