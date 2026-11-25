// Package fakebroker is an in-memory ports.BrokerPort for tests (ADR-004).
// It behaves like a paper broker: it validates requests, refuses a duplicate
// client order ID, and fills market orders at a configured price (or leaves
// them working). Every method can be made to fail.
package fakebroker

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/trading-bot/bot-engine/internal/domain/entities"
	"github.com/trading-bot/bot-engine/internal/domain/ports"
)

// Broker is a concurrency-safe fake broker. The zero value is not usable; call New.
type Broker struct {
	mu sync.Mutex

	account   ports.Account
	positions []entities.Position
	clock     ports.MarketClock
	prices    map[string]decimal.Decimal // fill price per symbol; missing = order stays working

	orders   map[string]*ports.BrokerOrder // by broker ID
	byClient map[string]string             // client order ID -> broker ID
	seq      int

	// Errors returned by the matching method when set (FailAll applies to every method).
	FailAll, FailSubmit, FailAccount, FailPositions, FailClock, FailGet, FailCancel error
	// SubmitThenFail records the order and then returns this error, simulating a
	// submission whose outcome the caller cannot know (e.g. a timeout).
	SubmitThenFail error

	// Submitted records every accepted request, in order.
	Submitted []ports.SubmitOrderRequest

	// FailStream makes StreamTradeUpdates return this error immediately.
	FailStream error
	streams    []chan ports.TradeUpdate
}

var _ ports.BrokerPort = (*Broker)(nil)

// New returns a fake paper broker with the given equity and an open market.
func New(equity decimal.Decimal) *Broker {
	return &Broker{
		account: ports.Account{
			Status: "ACTIVE", Currency: "USD",
			Equity: equity, LastEquity: equity, Cash: equity, BuyingPower: equity,
		},
		clock:    ports.MarketClock{IsOpen: true, Timestamp: time.Now().UTC()},
		prices:   map[string]decimal.Decimal{},
		orders:   map[string]*ports.BrokerOrder{},
		byClient: map[string]string{},
	}
}

// SetAccount replaces the account summary.
func (b *Broker) SetAccount(a ports.Account) { b.mu.Lock(); b.account = a; b.mu.Unlock() }

// SetPositions replaces the open positions.
func (b *Broker) SetPositions(p []entities.Position) { b.mu.Lock(); b.positions = p; b.mu.Unlock() }

// SetMarketOpen opens or closes the market.
func (b *Broker) SetMarketOpen(open bool) { b.mu.Lock(); b.clock.IsOpen = open; b.mu.Unlock() }

// SetFillPrice makes market orders for symbol fill immediately at price.
func (b *Broker) SetFillPrice(symbol string, price decimal.Decimal) {
	b.mu.Lock()
	b.prices[symbol] = price
	b.mu.Unlock()
}

// ClearFillPrice leaves market orders for symbol working until Fill or Cancel.
func (b *Broker) ClearFillPrice(symbol string) {
	b.mu.Lock()
	delete(b.prices, symbol)
	b.mu.Unlock()
}

// Orders returns a copy of every order the fake holds.
func (b *Broker) Orders() []ports.BrokerOrder {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]ports.BrokerOrder, 0, len(b.orders))
	for _, o := range b.orders {
		out = append(out, *o)
	}
	return out
}

func (b *Broker) fail(specific error) error {
	if b.FailAll != nil {
		return b.FailAll
	}
	return specific
}

// Capabilities implements ports.BrokerPort.
func (b *Broker) Capabilities() ports.BrokerCapabilities {
	return ports.BrokerCapabilities{Broker: "fake", FractionalShares: true, ShortSelling: true, LimitOrders: true}
}

// Mode implements ports.BrokerPort; the fake is always paper.
func (b *Broker) Mode() ports.TradingMode { return ports.TradingModePaper }

// SubmitOrder implements ports.BrokerPort.
func (b *Broker) SubmitOrder(ctx context.Context, req ports.SubmitOrderRequest) (ports.BrokerOrder, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ports.BrokerOrder{}, fmt.Errorf("fake submit: %w: %w", ports.ErrUnavailable, err)
	}
	if err := b.fail(b.FailSubmit); err != nil {
		return ports.BrokerOrder{}, err
	}
	if err := req.Validate(); err != nil {
		return ports.BrokerOrder{}, err
	}
	if _, dup := b.byClient[req.ClientOrderID]; dup {
		return ports.BrokerOrder{}, fmt.Errorf("fake submit: %w: client order ID already used", ports.ErrRejected)
	}
	if req.Side == entities.OrderSideBuy && b.prices[req.Symbol].Mul(req.Qty).GreaterThan(b.account.BuyingPower) {
		return ports.BrokerOrder{}, fmt.Errorf("fake submit: %w", ports.ErrInsufficientFunds)
	}

	b.seq++
	o := &ports.BrokerOrder{
		ID: fmt.Sprintf("fake-%d", b.seq), ClientOrderID: req.ClientOrderID,
		Symbol: req.Symbol, Side: req.Side, Type: req.Type, Qty: req.Qty,
		LimitPrice: req.LimitPrice, Status: entities.OrderStatusSubmitted,
		SubmittedAt: time.Now().UTC(),
	}
	if price, ok := b.prices[req.Symbol]; ok && req.Type == entities.OrderTypeMarket {
		now := time.Now().UTC()
		p := price
		o.Status, o.FilledQty, o.FilledAvgPrice, o.FilledAt = entities.OrderStatusFilled, req.Qty, &p, &now
	}
	b.orders[o.ID] = o
	b.byClient[req.ClientOrderID] = o.ID
	b.Submitted = append(b.Submitted, req)
	if b.SubmitThenFail != nil {
		return ports.BrokerOrder{}, b.SubmitThenFail
	}
	return *o, nil
}

// CancelOrder implements ports.BrokerPort.
func (b *Broker) CancelOrder(_ context.Context, brokerOrderID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.fail(b.FailCancel); err != nil {
		return err
	}
	o, ok := b.orders[brokerOrderID]
	if !ok {
		return fmt.Errorf("fake cancel: %w", ports.ErrNotFound)
	}
	if o.Status == entities.OrderStatusFilled || o.Status == entities.OrderStatusCanceled {
		return fmt.Errorf("fake cancel: %w: order is %s", ports.ErrRejected, o.Status)
	}
	o.Status = entities.OrderStatusCanceled
	return nil
}

// CancelAllOrders implements ports.BrokerPort.
func (b *Broker) CancelAllOrders(context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.fail(b.FailCancel); err != nil {
		return err
	}
	for _, o := range b.orders {
		if o.Status == entities.OrderStatusSubmitted || o.Status == entities.OrderStatusPartiallyFilled {
			o.Status = entities.OrderStatusCanceled
		}
	}
	return nil
}

// GetOrder implements ports.BrokerPort.
func (b *Broker) GetOrder(_ context.Context, brokerOrderID string) (ports.BrokerOrder, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.fail(b.FailGet); err != nil {
		return ports.BrokerOrder{}, err
	}
	o, ok := b.orders[brokerOrderID]
	if !ok {
		return ports.BrokerOrder{}, fmt.Errorf("fake get order: %w", ports.ErrNotFound)
	}
	return *o, nil
}

// GetOrderByClientID implements ports.BrokerPort.
func (b *Broker) GetOrderByClientID(ctx context.Context, clientOrderID string) (ports.BrokerOrder, error) {
	b.mu.Lock()
	id, ok := b.byClient[clientOrderID]
	b.mu.Unlock()
	if !ok {
		if err := b.fail(b.FailGet); err != nil {
			return ports.BrokerOrder{}, err
		}
		return ports.BrokerOrder{}, fmt.Errorf("fake get order by client ID: %w", ports.ErrNotFound)
	}
	return b.GetOrder(ctx, id)
}

// GetAccount implements ports.BrokerPort.
func (b *Broker) GetAccount(context.Context) (ports.Account, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.fail(b.FailAccount); err != nil {
		return ports.Account{}, err
	}
	return b.account, nil
}

// GetPositions implements ports.BrokerPort.
func (b *Broker) GetPositions(context.Context) ([]entities.Position, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.fail(b.FailPositions); err != nil {
		return nil, err
	}
	return append([]entities.Position(nil), b.positions...), nil
}

// StreamTradeUpdates implements ports.BrokerPort: it delivers what Fill and
// Cancel emit until ctx ends.
func (b *Broker) StreamTradeUpdates(ctx context.Context, handler func(ports.TradeUpdate)) error {
	b.mu.Lock()
	if err := b.fail(b.FailStream); err != nil {
		b.mu.Unlock()
		return err
	}
	ch := make(chan ports.TradeUpdate, 64)
	b.streams = append(b.streams, ch)
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		for i, c := range b.streams {
			if c == ch {
				b.streams = append(b.streams[:i], b.streams[i+1:]...)
				break
			}
		}
		b.mu.Unlock()
	}()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case u := <-ch:
			handler(u)
		}
	}
}

// Streaming reports how many StreamTradeUpdates calls are listening.
func (b *Broker) Streaming() int { b.mu.Lock(); defer b.mu.Unlock(); return len(b.streams) }

// Fill fills a working order at price and emits a "fill" trade update.
func (b *Broker) Fill(brokerOrderID string, price decimal.Decimal) error {
	return b.change(brokerOrderID, "fill", func(o *ports.BrokerOrder) {
		now := time.Now().UTC()
		p := price
		o.Status, o.FilledQty, o.FilledAvgPrice, o.FilledAt = entities.OrderStatusFilled, o.Qty, &p, &now
	})
}

// Cancel cancels a working order and emits a "canceled" trade update.
func (b *Broker) Cancel(brokerOrderID string) error {
	return b.change(brokerOrderID, "canceled", func(o *ports.BrokerOrder) { o.Status = entities.OrderStatusCanceled })
}

// SetOrderSilently changes an order without emitting an update, simulating a
// missed stream event that only polling can recover.
func (b *Broker) SetOrderSilently(brokerOrderID string, status entities.OrderStatus) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	o, ok := b.orders[brokerOrderID]
	if !ok {
		return fmt.Errorf("fake: %w", ports.ErrNotFound)
	}
	o.Status = status
	if status == entities.OrderStatusFilled {
		o.FilledQty = o.Qty
	}
	return nil
}

func (b *Broker) change(brokerOrderID, event string, apply func(*ports.BrokerOrder)) error {
	b.mu.Lock()
	o, ok := b.orders[brokerOrderID]
	if !ok {
		b.mu.Unlock()
		return fmt.Errorf("fake: %w", ports.ErrNotFound)
	}
	apply(o)
	u := ports.TradeUpdate{Event: event, At: time.Now().UTC(), Order: *o}
	streams := append([]chan ports.TradeUpdate(nil), b.streams...)
	b.mu.Unlock()
	for _, ch := range streams {
		ch <- u
	}
	return nil
}

// GetClock implements ports.BrokerPort.
func (b *Broker) GetClock(context.Context) (ports.MarketClock, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.fail(b.FailClock); err != nil {
		return ports.MarketClock{}, err
	}
	return b.clock, nil
}
