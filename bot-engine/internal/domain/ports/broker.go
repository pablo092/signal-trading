package ports

import (
	"context"
	"errors"
	"time"

	"github.com/shopspring/decimal"

	"github.com/trading-bot/bot-engine/internal/domain/entities"
)

// TradingMode is the account mode a broker adapter has verified against the
// broker itself, not just read from config (ADR-004, ADR-005 §5.1).
type TradingMode string

const (
	TradingModePaper TradingMode = "paper"
	TradingModeLive  TradingMode = "live" // never accepted by this engine
)

// BrokerCapabilities describes what an adapter supports, so callers can refuse
// requests the broker cannot honour instead of discovering it on submission.
type BrokerCapabilities struct {
	Broker           string // e.g. "alpaca"
	FractionalShares bool
	ShortSelling     bool
	LimitOrders      bool
	TradeStreaming   bool // StreamTradeUpdates is not part of the port yet (BL-23)
}

// SubmitOrderRequest is a new order for the broker.
type SubmitOrderRequest struct {
	// ClientOrderID is required: it is the idempotency key that lets a caller
	// find an order whose submission outcome is unknown (GetOrderByClientID)
	// instead of submitting it twice.
	ClientOrderID string
	Symbol        string
	Side          entities.OrderSide
	Type          entities.OrderType
	Qty           decimal.Decimal
	LimitPrice    *decimal.Decimal // required for limit orders, nil otherwise
}

// BrokerOrder is the broker's view of an order.
type BrokerOrder struct {
	ID             string
	ClientOrderID  string
	Symbol         string
	Side           entities.OrderSide
	Type           entities.OrderType
	Status         entities.OrderStatus
	Qty            decimal.Decimal
	FilledQty      decimal.Decimal
	FilledAvgPrice *decimal.Decimal
	LimitPrice     *decimal.Decimal
	SubmittedAt    time.Time
	FilledAt       *time.Time
}

// State converts the broker view into the domain's BrokerState.
func (o BrokerOrder) State() entities.BrokerState {
	return entities.BrokerState{
		BrokerID:       o.ID,
		Status:         o.Status,
		FilledQty:      o.FilledQty,
		FilledAvgPrice: o.FilledAvgPrice,
		FilledAt:       o.FilledAt,
	}
}

// Account is the broker account summary.
type Account struct {
	Status         string
	Currency       string
	Equity         decimal.Decimal
	LastEquity     decimal.Decimal // equity at the previous market close
	Cash           decimal.Decimal
	BuyingPower    decimal.Decimal
	TradingBlocked bool // account or trading blocked by the broker
}

// DayPnL is today's equity change versus the previous close.
func (a Account) DayPnL() decimal.Decimal { return a.Equity.Sub(a.LastEquity) }

// MarketClock is the broker's market clock.
type MarketClock struct {
	IsOpen    bool
	Timestamp time.Time
	NextOpen  time.Time
	NextClose time.Time
}

// Typed broker errors. Adapters wrap them (fmt.Errorf("...: %w", ErrX)) so
// callers can branch with errors.Is without seeing broker-specific payloads.
var (
	// ErrInvalidOrder: the request failed local validation and was not sent.
	ErrInvalidOrder = errors.New("broker: invalid order request")
	// ErrRejected: the broker refused the request (it will not succeed on retry).
	ErrRejected = errors.New("broker: request rejected")
	// ErrInsufficientFunds: rejected for lack of buying power or shares.
	ErrInsufficientFunds = errors.New("broker: insufficient funds")
	// ErrNotFound: the order (or resource) does not exist at the broker.
	ErrNotFound = errors.New("broker: not found")
	// ErrRateLimited: the broker throttled the request; retry later.
	ErrRateLimited = errors.New("broker: rate limited")
	// ErrUnavailable: the broker could not be reached, timed out, or failed.
	// For SubmitOrder the outcome is unknown: look the order up by client ID.
	ErrUnavailable = errors.New("broker: unavailable")
)

// BrokerPort is the outbound port for a trading broker (ADR-004 v2).
// Every call honours ctx cancellation and deadlines.
type BrokerPort interface {
	// Capabilities reports what the adapter supports.
	Capabilities() BrokerCapabilities
	// Mode is the trading mode verified against the broker at construction.
	Mode() TradingMode

	// SubmitOrder places an order. req.ClientOrderID is required.
	SubmitOrder(ctx context.Context, req SubmitOrderRequest) (BrokerOrder, error)
	// CancelOrder cancels a working order by its broker ID.
	CancelOrder(ctx context.Context, brokerOrderID string) error
	// CancelAllOrders cancels every working order (kill switch; admin:killswitch, BL-19).
	CancelAllOrders(ctx context.Context) error
	// GetOrder returns an order by its broker ID.
	GetOrder(ctx context.Context, brokerOrderID string) (BrokerOrder, error)
	// GetOrderByClientID returns an order by the client order ID it was submitted with.
	GetOrderByClientID(ctx context.Context, clientOrderID string) (BrokerOrder, error)

	// GetAccount returns equity, buying power and day P&L inputs.
	GetAccount(ctx context.Context) (Account, error)
	// GetPositions returns all open positions.
	GetPositions(ctx context.Context) ([]entities.Position, error)
	// GetClock returns the market clock.
	GetClock(ctx context.Context) (MarketClock, error)
}

// Validate checks a SubmitOrderRequest locally; errors wrap ErrInvalidOrder.
func (r SubmitOrderRequest) Validate() error {
	switch {
	case r.ClientOrderID == "":
		return errors.Join(ErrInvalidOrder, errors.New("client order ID is required"))
	case len(r.ClientOrderID) > 128: // Alpaca's limit
		return errors.Join(ErrInvalidOrder, errors.New("client order ID is longer than 128 characters"))
	case r.Symbol == "":
		return errors.Join(ErrInvalidOrder, errors.New("symbol is required"))
	case r.Side != entities.OrderSideBuy && r.Side != entities.OrderSideSell:
		return errors.Join(ErrInvalidOrder, errors.New("side must be buy or sell"))
	case !r.Qty.IsPositive():
		return errors.Join(ErrInvalidOrder, errors.New("quantity must be positive"))
	}
	switch r.Type {
	case entities.OrderTypeMarket:
		if r.LimitPrice != nil {
			return errors.Join(ErrInvalidOrder, errors.New("market orders take no limit price"))
		}
	case entities.OrderTypeLimit:
		if r.LimitPrice == nil || !r.LimitPrice.IsPositive() {
			return errors.Join(ErrInvalidOrder, errors.New("limit orders need a positive limit price"))
		}
	default:
		return errors.Join(ErrInvalidOrder, errors.New("type must be market or limit"))
	}
	return nil
}

// SubmitRequestFor builds the broker request for a domain order.
func SubmitRequestFor(o *entities.Order) SubmitOrderRequest {
	return SubmitOrderRequest{
		ClientOrderID: o.ClientOrderID(),
		Symbol:        o.Symbol,
		Side:          o.Side,
		Type:          o.Type,
		Qty:           o.Quantity,
		LimitPrice:    o.LimitPrice,
	}
}
