// Package broker provides the Alpaca Markets adapter for ports.BrokerPort (ADR-004 v2).
package broker

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/alpacahq/alpaca-trade-api-go/v3/alpaca"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/trading-bot/bot-engine/internal/domain/entities"
	"github.com/trading-bot/bot-engine/internal/domain/ports"
	"github.com/trading-bot/bot-engine/internal/infrastructure/config"
)

// RequestTimeout bounds every HTTP request to Alpaca, whatever the caller's ctx.
const RequestTimeout = 10 * time.Second

// paperAccountPrefix: Alpaca paper account numbers start with "PA".
const paperAccountPrefix = "PA"

// orderIDPattern guards IDs that the SDK interpolates into URL paths.
var orderIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// AlpacaBroker adapts the Alpaca Markets REST API to ports.BrokerPort.
type AlpacaBroker struct {
	client *alpaca.Client
	mode   ports.TradingMode
	logger *zap.Logger
}

var _ ports.BrokerPort = (*AlpacaBroker)(nil)

// NewAlpacaBroker builds the adapter and verifies the trading mode against the
// broker itself: config must be paper (paper host + PAPER_TRADING), and the
// account Alpaca returns for these keys must be a paper account. Any mismatch
// or failure to verify refuses construction (fail closed).
func NewAlpacaBroker(ctx context.Context, cfg *config.Config, logger *zap.Logger) (*AlpacaBroker, error) {
	if mode, verified := cfg.TradingMode(); !verified || mode != config.TradingModePaper {
		return nil, errors.New("alpaca: config is not a verified paper deployment")
	}
	client := alpaca.NewClient(alpaca.ClientOpts{
		APIKey:     cfg.AlpacaAPIKey,
		APISecret:  cfg.AlpacaSecretKey,
		BaseURL:    cfg.AlpacaBaseURL,
		HTTPClient: &http.Client{Timeout: RequestTimeout},
	})
	return connect(ctx, client, logger)
}

// connect verifies the account behind client is a paper account.
func connect(ctx context.Context, client *alpaca.Client, logger *zap.Logger) (*AlpacaBroker, error) {
	acct, err := call(ctx, "verify account", client.GetAccount)
	if err != nil {
		return nil, fmt.Errorf("alpaca: cannot verify trading mode: %w", err)
	}
	if !strings.HasPrefix(acct.AccountNumber, paperAccountPrefix) {
		return nil, errors.New("alpaca: the account for these keys is not a paper account: refusing to start")
	}
	logger.Info("alpaca.mode_verified",
		zap.String("mode", string(ports.TradingModePaper)),
		zap.String("account_status", acct.Status),
	)
	return &AlpacaBroker{client: client, mode: ports.TradingModePaper, logger: logger}, nil
}

// Capabilities reports what this adapter supports.
func (b *AlpacaBroker) Capabilities() ports.BrokerCapabilities {
	return ports.BrokerCapabilities{
		Broker:           "alpaca",
		FractionalShares: true,
		ShortSelling:     true,
		LimitOrders:      true,
		TradeStreaming:   false,
	}
}

// Mode is the mode verified at construction (always paper).
func (b *AlpacaBroker) Mode() ports.TradingMode { return b.mode }

// SubmitOrder places a day order. req.ClientOrderID is required and is sent to
// Alpaca, which refuses a second order with the same ID.
func (b *AlpacaBroker) SubmitOrder(ctx context.Context, req ports.SubmitOrderRequest) (ports.BrokerOrder, error) {
	if err := req.Validate(); err != nil {
		return ports.BrokerOrder{}, err
	}
	qty := req.Qty
	place := alpaca.PlaceOrderRequest{
		ClientOrderID: req.ClientOrderID,
		Symbol:        req.Symbol,
		Qty:           &qty,
		Side:          alpaca.Side(req.Side),
		Type:          alpaca.OrderType(req.Type),
		TimeInForce:   alpaca.Day,
		LimitPrice:    req.LimitPrice,
	}
	b.logger.Info("alpaca.submit_order",
		zap.String("client_order_id", req.ClientOrderID),
		zap.String("symbol", req.Symbol),
		zap.String("side", string(req.Side)),
		zap.String("type", string(req.Type)),
		zap.String("qty", req.Qty.String()),
	)
	o, err := call(ctx, "place order", func() (*alpaca.Order, error) { return b.client.PlaceOrder(place) })
	if err != nil {
		return ports.BrokerOrder{}, err
	}
	b.logger.Info("alpaca.order_placed", zap.String("client_order_id", o.ClientOrderID),
		zap.String("broker_order_id", o.ID), zap.String("status", o.Status))
	return toBrokerOrder(o), nil
}

// CancelOrder cancels a working order by its Alpaca ID.
func (b *AlpacaBroker) CancelOrder(ctx context.Context, brokerOrderID string) error {
	if !orderIDPattern.MatchString(brokerOrderID) {
		return fmt.Errorf("alpaca cancel order: %w: malformed order ID", ports.ErrInvalidOrder)
	}
	_, err := call(ctx, "cancel order", func() (struct{}, error) { return struct{}{}, b.client.CancelOrder(brokerOrderID) })
	if err == nil {
		b.logger.Info("alpaca.order_canceled", zap.String("broker_order_id", brokerOrderID))
	}
	return err
}

// CancelAllOrders cancels every working order (kill switch, BL-19).
func (b *AlpacaBroker) CancelAllOrders(ctx context.Context) error {
	_, err := call(ctx, "cancel all orders", func() (struct{}, error) { return struct{}{}, b.client.CancelAllOrders() })
	if err == nil {
		b.logger.Warn("alpaca.all_orders_canceled")
	}
	return err
}

// GetOrder returns an order by its Alpaca ID.
func (b *AlpacaBroker) GetOrder(ctx context.Context, brokerOrderID string) (ports.BrokerOrder, error) {
	if !orderIDPattern.MatchString(brokerOrderID) {
		return ports.BrokerOrder{}, fmt.Errorf("alpaca get order: %w: malformed order ID", ports.ErrInvalidOrder)
	}
	o, err := call(ctx, "get order", func() (*alpaca.Order, error) { return b.client.GetOrder(brokerOrderID) })
	if err != nil {
		return ports.BrokerOrder{}, err
	}
	return toBrokerOrder(o), nil
}

// GetOrderByClientID returns an order by the client order ID it was submitted with.
func (b *AlpacaBroker) GetOrderByClientID(ctx context.Context, clientOrderID string) (ports.BrokerOrder, error) {
	if clientOrderID == "" || len(clientOrderID) > 128 {
		return ports.BrokerOrder{}, fmt.Errorf("alpaca get order by client ID: %w: malformed client order ID", ports.ErrInvalidOrder)
	}
	o, err := call(ctx, "get order by client ID", func() (*alpaca.Order, error) {
		return b.client.GetOrderByClientOrderID(clientOrderID) // sent as a query parameter
	})
	if err != nil {
		return ports.BrokerOrder{}, err
	}
	return toBrokerOrder(o), nil
}

// GetAccount returns the account summary.
func (b *AlpacaBroker) GetAccount(ctx context.Context) (ports.Account, error) {
	a, err := call(ctx, "get account", b.client.GetAccount)
	if err != nil {
		return ports.Account{}, err
	}
	return ports.Account{
		Status:         a.Status,
		Currency:       a.Currency,
		Equity:         a.Equity,
		LastEquity:     a.LastEquity,
		Cash:           a.Cash,
		BuyingPower:    a.BuyingPower,
		TradingBlocked: a.TradingBlocked || a.AccountBlocked || a.TradeSuspendedByUser,
	}, nil
}

// GetPositions returns all open positions. Prices come from Alpaca; nothing is
// derived by dividing by a quantity.
func (b *AlpacaBroker) GetPositions(ctx context.Context) ([]entities.Position, error) {
	raw, err := call(ctx, "get positions", b.client.GetPositions)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	positions := make([]entities.Position, 0, len(raw))
	for _, p := range raw {
		qty := p.Qty
		if strings.EqualFold(p.Side, "short") && qty.IsPositive() {
			qty = qty.Neg()
		}
		positions = append(positions, entities.Position{
			Symbol:            p.Symbol,
			Quantity:          qty,
			AverageEntryPrice: p.AvgEntryPrice,
			CurrentPrice:      orZero(p.CurrentPrice),
			MarketValue:       orZero(p.MarketValue),
			UnrealizedPnL:     orZero(p.UnrealizedPL),
			UpdatedAt:         now,
		})
	}
	return positions, nil
}

// GetClock returns the market clock.
func (b *AlpacaBroker) GetClock(ctx context.Context) (ports.MarketClock, error) {
	c, err := call(ctx, "get clock", b.client.GetClock)
	if err != nil {
		return ports.MarketClock{}, err
	}
	return ports.MarketClock{IsOpen: c.IsOpen, Timestamp: c.Timestamp, NextOpen: c.NextOpen, NextClose: c.NextClose}, nil
}

// ── helpers ────────────────────────────────────────────────────────────────

// call runs a (ctx-unaware) SDK call and returns early when ctx ends. The SDK
// call itself is bounded by RequestTimeout, so an abandoned goroutine always
// finishes. Errors are mapped to the typed port errors.
func call[T any](ctx context.Context, op string, fn func() (T, error)) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, fmt.Errorf("alpaca %s: %w: %w", op, ports.ErrUnavailable, err)
	}
	type result struct {
		v   T
		err error
	}
	ch := make(chan result, 1)
	go func() {
		v, err := fn()
		ch <- result{v, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			return zero, mapError(op, r.err)
		}
		return r.v, nil
	case <-ctx.Done():
		return zero, fmt.Errorf("alpaca %s: %w: %w", op, ports.ErrUnavailable, ctx.Err())
	}
}

// mapError converts an SDK error to a typed port error. The response body is
// deliberately dropped: it can carry account details.
func mapError(op string, err error) error {
	var apiErr *alpaca.APIError
	if !errors.As(err, &apiErr) {
		return fmt.Errorf("alpaca %s: %w: %v", op, ports.ErrUnavailable, err)
	}
	msg := strings.ToLower(apiErr.Message)
	var kind error
	switch s := apiErr.StatusCode; {
	case s == http.StatusNotFound:
		kind = ports.ErrNotFound
	case s == http.StatusTooManyRequests:
		kind = ports.ErrRateLimited
	case s == http.StatusUnauthorized || s >= 500:
		kind = ports.ErrUnavailable
	case strings.Contains(msg, "insufficient"):
		kind = ports.ErrInsufficientFunds
	default: // 400, 403, 422, ...
		kind = ports.ErrRejected
	}
	return fmt.Errorf("alpaca %s: %w (HTTP %d, code %d: %s)", op, kind, apiErr.StatusCode, apiErr.Code, apiErr.Message)
}

func toBrokerOrder(o *alpaca.Order) ports.BrokerOrder {
	return ports.BrokerOrder{
		ID:             o.ID,
		ClientOrderID:  o.ClientOrderID,
		Symbol:         o.Symbol,
		Side:           entities.OrderSide(o.Side),
		Type:           entities.OrderType(o.Type),
		Status:         FromAlpacaStatus(o.Status),
		Qty:            orZero(o.Qty),
		FilledQty:      o.FilledQty,
		FilledAvgPrice: o.FilledAvgPrice,
		LimitPrice:     o.LimitPrice,
		SubmittedAt:    o.SubmittedAt,
		FilledAt:       o.FilledAt,
	}
}

// FromAlpacaStatus maps an Alpaca order status to the domain status. Unknown
// statuses map to submitted (still working), never to a terminal state.
func FromAlpacaStatus(s string) entities.OrderStatus {
	switch s {
	case "filled":
		return entities.OrderStatusFilled
	case "partially_filled":
		return entities.OrderStatusPartiallyFilled
	case "canceled", "cancelled", "replaced":
		return entities.OrderStatusCanceled
	case "rejected":
		return entities.OrderStatusRejected
	case "expired":
		return entities.OrderStatusExpired
	default: // new, accepted, pending_new, pending_cancel, pending_replace, held, done_for_day, ...
		return entities.OrderStatusSubmitted
	}
}

func orZero(d *decimal.Decimal) decimal.Decimal {
	if d == nil {
		return decimal.Zero
	}
	return *d
}
