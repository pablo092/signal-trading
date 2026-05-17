// Package entities defines the core domain objects of the bot engine.
// These are pure Go structs with no external dependencies — the innermost
// layer of Clean Architecture. Infra never leaks in here.
package entities

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// OrderSide represents the direction of a trade.
type OrderSide string

const (
	OrderSideBuy  OrderSide = "buy"
	OrderSideSell OrderSide = "sell"
)

// OrderType represents the execution type of an order.
type OrderType string

const (
	OrderTypeMarket OrderType = "market"
	OrderTypeLimit  OrderType = "limit"
)

// OrderStatus represents the lifecycle state of an order.
type OrderStatus string

const (
	OrderStatusPending   OrderStatus = "pending"
	OrderStatusSubmitted OrderStatus = "submitted"
	OrderStatusFilled    OrderStatus = "filled"
	OrderStatusCanceled  OrderStatus = "canceled"
	OrderStatusRejected  OrderStatus = "rejected"
)

// Order is the central domain entity. Represents a trade instruction
// sent to the broker. Immutable after creation — transitions produce new states.
type Order struct {
	ID          uuid.UUID
	Symbol      string
	Side        OrderSide
	Type        OrderType
	Quantity    float64
	LimitPrice  *float64 // nil for market orders
	Status      OrderStatus
	BrokerID    string     // filled by broker after submission
	FilledAt    *time.Time // nil until filled
	FilledPrice *float64   // nil until filled
	CreatedAt   time.Time
	UpdatedAt   time.Time
	StrategyID  string
	SignalID    string
}

// NewMarketOrder creates a validated market order.
func NewMarketOrder(symbol string, side OrderSide, qty float64, strategyID, signalID string) (*Order, error) {
	if symbol == "" {
		return nil, fmt.Errorf("symbol cannot be empty")
	}
	if qty <= 0 {
		return nil, fmt.Errorf("quantity must be positive, got %.4f", qty)
	}
	if side != OrderSideBuy && side != OrderSideSell {
		return nil, fmt.Errorf("invalid order side: %q", side)
	}
	now := time.Now().UTC()
	return &Order{
		ID:         uuid.New(),
		Symbol:     symbol,
		Side:       side,
		Type:       OrderTypeMarket,
		Quantity:   qty,
		Status:     OrderStatusPending,
		CreatedAt:  now,
		UpdatedAt:  now,
		StrategyID: strategyID,
		SignalID:   signalID,
	}, nil
}

// NewLimitOrder creates a validated limit order.
func NewLimitOrder(symbol string, side OrderSide, qty, limitPrice float64, strategyID, signalID string) (*Order, error) {
	order, err := NewMarketOrder(symbol, side, qty, strategyID, signalID)
	if err != nil {
		return nil, err
	}
	if limitPrice <= 0 {
		return nil, fmt.Errorf("limit price must be positive, got %.4f", limitPrice)
	}
	order.Type = OrderTypeLimit
	order.LimitPrice = &limitPrice
	return order, nil
}

// Transition creates a new Order in the given status — orders are immutable.
func (o *Order) Transition(newStatus OrderStatus) *Order {
	updated := *o
	updated.Status = newStatus
	updated.UpdatedAt = time.Now().UTC()
	return &updated
}

// MarkFilled transitions to filled with broker-provided data.
func (o *Order) MarkFilled(brokerID string, filledPrice float64) *Order {
	updated := o.Transition(OrderStatusFilled)
	updated.BrokerID = brokerID
	now := time.Now().UTC()
	updated.FilledAt = &now
	updated.FilledPrice = &filledPrice
	return updated
}

// IsTerminal returns true if the order has reached a final state.
func (o *Order) IsTerminal() bool {
	return o.Status == OrderStatusFilled ||
		o.Status == OrderStatusCanceled ||
		o.Status == OrderStatusRejected
}

// Position represents the current holding of an asset.
type Position struct {
	Symbol           string
	Quantity         float64
	AverageEntryPrice float64
	CurrentPrice     float64
	UnrealizedPnL    float64
	UpdatedAt        time.Time
}

// UpdatePrice recalculates unrealized P&L given a new market price.
func (p *Position) UpdatePrice(currentPrice float64) Position {
	updated := *p
	updated.CurrentPrice = currentPrice
	updated.UnrealizedPnL = (currentPrice - p.AverageEntryPrice) * p.Quantity
	updated.UpdatedAt = time.Now().UTC()
	return updated
}

// Trade is the immutable record of a completed transaction.
type Trade struct {
	ID          uuid.UUID
	OrderID     uuid.UUID
	Symbol      string
	Side        OrderSide
	Quantity    float64
	Price       float64
	Commission  float64
	RealizedPnL float64
	ExecutedAt  time.Time
}
