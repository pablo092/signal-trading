package ports

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/trading-bot/bot-engine/internal/domain/entities"
)

// Intent repository errors.
var (
	ErrIntentNotFound = errors.New("order intent not found")
	// ErrIdempotencyKeyExists: (actor, Idempotency-Key) is already used.
	ErrIdempotencyKeyExists = errors.New("idempotency key already used by this actor")
	// ErrActiveIntentForSignal: another active intent uses the same signal (ADR-005 §5.4).
	ErrActiveIntentForSignal = errors.New("an active intent already exists for this signal")
	// ErrStaleIntent: the stored status was not the expected one (compare-and-set lost).
	ErrStaleIntent = errors.New("order intent changed concurrently")
)

// IntentFilter selects intents for listing. Zero Status = any.
type IntentFilter struct {
	Status entities.IntentStatus
	Limit  int
}

// IntentRepositoryPort persists order intents. Create enforces both uniqueness
// rules atomically; Update is a compare-and-set on the status, so two
// concurrent approvals cannot both win.
type IntentRepositoryPort interface {
	// Create stores a new intent and its creation snapshot (returned on
	// idempotent replay). Errors: ErrIdempotencyKeyExists, ErrActiveIntentForSignal.
	Create(ctx context.Context, intent *entities.OrderIntent) error
	Get(ctx context.Context, id uuid.UUID) (*entities.OrderIntent, error)
	// FindByIdempotencyKey returns the creation snapshot for (actorID, key).
	FindByIdempotencyKey(ctx context.Context, actorID, key string) (*entities.OrderIntent, error)
	// List returns intents newest first.
	List(ctx context.Context, f IntentFilter) ([]*entities.OrderIntent, error)
	// Update replaces the intent if its stored status is still from; else ErrStaleIntent.
	Update(ctx context.Context, intent *entities.OrderIntent, from entities.IntentStatus) error
	// ExpiredPending returns pending_approval intents whose deadline is at or before now.
	ExpiredPending(ctx context.Context, now time.Time) ([]*entities.OrderIntent, error)
}

// TradeUpdate is one event of the broker's trade-update stream (fill, cancel, ...).
type TradeUpdate struct {
	Event string // broker event name, e.g. "fill", "partial_fill", "canceled"
	At    time.Time
	Order BrokerOrder
}
