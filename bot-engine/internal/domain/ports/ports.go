// Package ports defines the interfaces (ports) for the bot engine.
// These are the boundaries between the application core and the infrastructure.
// Nothing in the domain or application layers depends on concrete implementations.
package ports

import (
	"context"
	"time"

	"github.com/trading-bot/bot-engine/internal/domain/entities"
)

// Signal is the DTO received from the signal service.
type Signal struct {
	ID           string
	Symbol       string
	Direction    string // "BUY", "SELL", "HOLD"
	Strength     string // "STRONG", "MODERATE", "WEAK"
	Price        float64
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

// BrokerPort is the outbound port for interacting with a trading broker.
//
//go:generate mockgen -destination=../../../infrastructure/broker/mock_broker.go -package=broker . BrokerPort
type BrokerPort interface {
	// SubmitOrder sends an order to the broker and returns its broker-assigned ID.
	SubmitOrder(ctx context.Context, order *entities.Order) (brokerID string, err error)
	// CancelOrder cancels a submitted order by its broker-assigned ID.
	CancelOrder(ctx context.Context, brokerID string) error
	// GetOrderStatus polls the broker for the current order status and fill price.
	GetOrderStatus(ctx context.Context, brokerID string) (entities.OrderStatus, float64, error)
	// GetPortfolioEquity returns the current total equity of the account.
	GetPortfolioEquity(ctx context.Context) (float64, error)
	// GetPositions returns all currently open positions.
	GetPositions(ctx context.Context) ([]entities.Position, error)
	// IsMarketOpen returns true when the market is open for trading.
	IsMarketOpen(ctx context.Context) (bool, error)
}

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
