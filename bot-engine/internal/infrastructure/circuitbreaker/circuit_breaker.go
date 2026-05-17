// Package circuitbreaker implements the Circuit Breaker pattern.
//
// Protects downstream calls (broker API, signal service) from cascade failures.
// States: Closed (normal) → Open (failing) → HalfOpen (probing recovery).
//
// Usage:
//
//	cb := circuitbreaker.New("alpaca", circuitbreaker.DefaultConfig())
//	err := cb.Execute(ctx, func(ctx context.Context) error {
//	    return broker.SubmitOrder(ctx, order)
//	})
package circuitbreaker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

// State represents the circuit breaker's current state.
type State int

const (
	StateClosed   State = iota // Normal operation — requests pass through
	StateOpen                  // Failing — requests are rejected immediately
	StateHalfOpen              // Probing — one request allowed through
)

func (s State) String() string {
	return [...]string{"closed", "open", "half-open"}[s]
}

// ErrCircuitOpen is returned when the circuit is open and the call is rejected.
var ErrCircuitOpen = errors.New("circuit breaker is open — downstream may be unavailable")

// Config holds circuit breaker parameters.
type Config struct {
	// FailureThreshold: consecutive failures before opening.
	FailureThreshold int
	// SuccessThreshold: consecutive successes in HalfOpen before closing.
	SuccessThreshold int
	// Timeout: how long to wait in Open before probing (HalfOpen).
	Timeout time.Duration
}

// DefaultConfig returns safe defaults for broker API calls.
func DefaultConfig() Config {
	return Config{
		FailureThreshold: 5,
		SuccessThreshold: 2,
		Timeout:          30 * time.Second,
	}
}

// CircuitBreaker protects a downstream dependency.
type CircuitBreaker struct {
	name       string
	config     Config
	logger     *zap.Logger
	mu         sync.Mutex
	state      State
	failures   int
	successes  int
	lastFailAt time.Time
}

// New creates a new CircuitBreaker with the given name and config.
func New(name string, config Config, logger *zap.Logger) *CircuitBreaker {
	return &CircuitBreaker{
		name:   name,
		config: config,
		logger: logger,
		state:  StateClosed,
	}
}

// Execute runs fn if the circuit allows it; otherwise returns ErrCircuitOpen.
func (cb *CircuitBreaker) Execute(ctx context.Context, fn func(context.Context) error) error {
	if err := cb.allow(); err != nil {
		return err
	}

	err := fn(ctx)
	cb.record(err)
	return err
}

// State returns the current state (safe for external inspection).
func (cb *CircuitBreaker) State() State {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

func (cb *CircuitBreaker) allow() error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case StateClosed:
		return nil

	case StateOpen:
		if time.Since(cb.lastFailAt) >= cb.config.Timeout {
			cb.logger.Info("circuit_breaker.probing",
				zap.String("name", cb.name),
				zap.String("state", StateHalfOpen.String()),
			)
			cb.state = StateHalfOpen
			return nil
		}
		return fmt.Errorf("%w (name=%s)", ErrCircuitOpen, cb.name)

	case StateHalfOpen:
		return nil // Let one through to probe

	default:
		return nil
	}
}

func (cb *CircuitBreaker) record(err error) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if err != nil {
		cb.failures++
		cb.successes = 0
		cb.lastFailAt = time.Now()

		if cb.state == StateHalfOpen || cb.failures >= cb.config.FailureThreshold {
			cb.logger.Warn("circuit_breaker.opened",
				zap.String("name", cb.name),
				zap.Int("failures", cb.failures),
			)
			cb.state = StateOpen
		}
		return
	}

	// Success path
	cb.failures = 0
	if cb.state == StateHalfOpen {
		cb.successes++
		if cb.successes >= cb.config.SuccessThreshold {
			cb.logger.Info("circuit_breaker.closed",
				zap.String("name", cb.name),
			)
			cb.state = StateClosed
			cb.successes = 0
		}
	}
}
