// Package usecases contains the application layer use cases.
// Each use case has a single responsibility and depends only on ports (interfaces).
// No framework code (Gin, HTTP) lives here.
package usecases

import (
	"context"
	"errors"
	"time"

	"github.com/shopspring/decimal"
)

// Domain event types published on the event bus (and relayed to WebSocket
// clients as the versioned events in contracts/, BL-16).
const (
	EventSignalReceived    = "SignalReceived"
	EventOrderPlaced       = "OrderPlaced"
	EventKillSwitchChanged = "KillSwitchChanged"
)

// KillSwitchChecker reports whether trading is halted (BL-19).
type KillSwitchChecker interface {
	Engaged(ctx context.Context) (bool, error)
}

// Errors the order path adds on top of the typed broker errors (ports.Err*),
// so the HTTP layer can choose a status without parsing messages.
var (
	// ErrSignalUnavailable: the signal service could not provide a signal.
	ErrSignalUnavailable = errors.New("signal unavailable")
	// ErrOutcomeUnknown: the broker may or may not have the order; it is left
	// pending until reconciled (BL-23). Never resubmit on this error.
	ErrOutcomeUnknown = errors.New("order outcome unknown")
	// ErrAuditUnavailable: the audit log refused the pre-submission record, so
	// the order was not sent (fail closed).
	ErrAuditUnavailable = errors.New("audit log unavailable")
)

// minOrderQty is the smallest quantity worth submitting.
var minOrderQty = decimal.RequireFromString("0.001")

// lookupTimeout bounds the reconciliation lookup after an unknown submit outcome.
const lookupTimeout = 5 * time.Second
