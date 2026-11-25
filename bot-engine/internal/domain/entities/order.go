// Package entities defines the core domain objects of the bot engine.
// These are pure Go structs with no infrastructure dependencies — the innermost
// layer of Clean Architecture. Money and quantities are decimal.Decimal (ADR-004,
// TD-24): binary floats cannot represent cents exactly.
package entities

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
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
	OrderStatusPending         OrderStatus = "pending"   // created locally, not yet acknowledged by the broker
	OrderStatusSubmitted       OrderStatus = "submitted" // acknowledged by the broker, working
	OrderStatusPartiallyFilled OrderStatus = "partially_filled"
	OrderStatusFilled          OrderStatus = "filled"
	OrderStatusCanceled        OrderStatus = "canceled"
	OrderStatusRejected        OrderStatus = "rejected"
	OrderStatusExpired         OrderStatus = "expired"
)

// Order is the central domain entity: a trade instruction sent to the broker.
// Immutable after creation — transitions produce new values.
//
// ID doubles as the broker client order ID, so a submission whose outcome is
// unknown (timeout) can be looked up instead of resubmitted (ADR-004).
type Order struct {
	ID          uuid.UUID
	Symbol      string
	Side        OrderSide
	Type        OrderType
	Quantity    decimal.Decimal
	LimitPrice  *decimal.Decimal // nil for market orders
	Status      OrderStatus
	BrokerID    string           // assigned by the broker on acknowledgement
	FilledQty   decimal.Decimal  // zero until (partially) filled
	FilledPrice *decimal.Decimal // average fill price; nil until filled
	FilledAt    *time.Time       // nil until filled
	CreatedAt   time.Time
	UpdatedAt   time.Time
	StrategyID  string
	SignalID    string
}

// NewMarketOrder creates a validated market order.
func NewMarketOrder(symbol string, side OrderSide, qty decimal.Decimal, strategyID, signalID string) (*Order, error) {
	if symbol == "" {
		return nil, fmt.Errorf("symbol cannot be empty")
	}
	if !qty.IsPositive() {
		return nil, fmt.Errorf("quantity must be positive, got %s", qty)
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
func NewLimitOrder(symbol string, side OrderSide, qty, limitPrice decimal.Decimal, strategyID, signalID string) (*Order, error) {
	order, err := NewMarketOrder(symbol, side, qty, strategyID, signalID)
	if err != nil {
		return nil, err
	}
	if !limitPrice.IsPositive() {
		return nil, fmt.Errorf("limit price must be positive, got %s", limitPrice)
	}
	order.Type = OrderTypeLimit
	order.LimitPrice = &limitPrice
	return order, nil
}

// ClientOrderID is the idempotency key sent to the broker for this order.
func (o *Order) ClientOrderID() string { return o.ID.String() }

// Transition creates a new Order in the given status — orders are immutable.
func (o *Order) Transition(newStatus OrderStatus) *Order {
	updated := *o
	updated.Status = newStatus
	updated.UpdatedAt = time.Now().UTC()
	return &updated
}

// BrokerState is what the broker reports about an order.
type BrokerState struct {
	BrokerID       string
	Status         OrderStatus
	FilledQty      decimal.Decimal
	FilledAvgPrice *decimal.Decimal
	FilledAt       *time.Time
}

// ApplyBrokerState returns the order updated with the broker's view of it.
// Fills are only ever recorded from broker data, never assumed.
func (o *Order) ApplyBrokerState(s BrokerState) *Order {
	updated := o.Transition(s.Status)
	if s.BrokerID != "" {
		updated.BrokerID = s.BrokerID
	}
	updated.FilledQty = s.FilledQty
	if s.FilledAvgPrice != nil {
		p := *s.FilledAvgPrice
		updated.FilledPrice = &p
	}
	if s.FilledAt != nil {
		t := *s.FilledAt
		updated.FilledAt = &t
	}
	return updated
}

// IsTerminal returns true if the order has reached a final state.
func (o *Order) IsTerminal() bool {
	switch o.Status {
	case OrderStatusFilled, OrderStatusCanceled, OrderStatusRejected, OrderStatusExpired:
		return true
	}
	return false
}

// Position represents the current holding of an asset. Quantity is negative
// for a short position.
type Position struct {
	Symbol            string
	Quantity          decimal.Decimal
	AverageEntryPrice decimal.Decimal
	CurrentPrice      decimal.Decimal
	MarketValue       decimal.Decimal
	UnrealizedPnL     decimal.Decimal
	UpdatedAt         time.Time
}

// UpdatePrice recalculates market value and unrealized P&L at a new price.
func (p *Position) UpdatePrice(currentPrice decimal.Decimal) Position {
	updated := *p
	updated.CurrentPrice = currentPrice
	updated.MarketValue = currentPrice.Mul(p.Quantity)
	updated.UnrealizedPnL = currentPrice.Sub(p.AverageEntryPrice).Mul(p.Quantity)
	updated.UpdatedAt = time.Now().UTC()
	return updated
}

// Trade is the immutable record of a completed transaction.
type Trade struct {
	ID          uuid.UUID
	OrderID     uuid.UUID
	Symbol      string
	Side        OrderSide
	Quantity    decimal.Decimal
	Price       decimal.Decimal
	Commission  decimal.Decimal
	RealizedPnL decimal.Decimal
	ExecutedAt  time.Time
}
