// Package ports defines the interfaces (ports) for the bot engine.
// These are the boundaries between the application core and the infrastructure.
// Nothing in the domain or application layers depends on concrete implementations.
package ports

import (
	"context"
	"errors"
	"time"

	"github.com/shopspring/decimal"

	"github.com/trading-bot/bot-engine/internal/domain/entities"
)

// Signal is the DTO received from the signal service.
type Signal struct {
	ID           string
	Symbol       string
	Direction    string // "BUY", "SELL", "HOLD"
	Strength     string // "STRONG", "MODERATE", "WEAK"
	Price        decimal.Decimal
	Confidence   float64
	Reason       string
	StrategyName string
	IsActionable bool
	GeneratedAt  time.Time
}

// DomainEvent is published when significant domain actions occur.
type DomainEvent struct {
	Type    string      // e.g. "OrderPlaced", "OrderFilled"
	Payload interface{} // the event data (usually an entity)
}

// ErrOrderNotFound is returned by order repositories for an unknown order.
var ErrOrderNotFound = errors.New("order not found")

// OrderRepositoryPort is the outbound port for persisting and querying orders.
type OrderRepositoryPort interface {
	Save(ctx context.Context, order *entities.Order) error
	FindByID(ctx context.Context, id interface{}) (*entities.Order, error)
	FindByStatus(ctx context.Context, status entities.OrderStatus) ([]*entities.Order, error)
	FindBySymbol(ctx context.Context, symbol string) ([]*entities.Order, error)
	Update(ctx context.Context, order *entities.Order) error
}

// SignalServicePort is the outbound port for fetching trading signals.
type SignalServicePort interface {
	GetSignal(ctx context.Context, symbol, strategy string) (*Signal, error)
}

// EventPublisherPort is the outbound port for publishing domain events.
type EventPublisherPort interface {
	Publish(ctx context.Context, event DomainEvent) error
}
