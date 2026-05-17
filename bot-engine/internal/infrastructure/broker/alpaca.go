// Package broker provides an Alpaca Markets adapter implementing ports.BrokerPort.
package broker

import (
	"context"
	"fmt"

	"github.com/alpacahq/alpaca-trade-api-go/v3/alpaca"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/trading-bot/bot-engine/internal/domain/entities"
	"github.com/trading-bot/bot-engine/internal/infrastructure/config"
)

// AlpacaBroker adapts the Alpaca Markets REST API to the BrokerPort interface.
type AlpacaBroker struct {
	client *alpaca.Client
	logger *zap.Logger
}

// NewAlpacaBroker constructs an AlpacaBroker from the given config.
func NewAlpacaBroker(cfg *config.Config, logger *zap.Logger) *AlpacaBroker {
	client := alpaca.NewClient(alpaca.ClientOpts{
		APIKey:    cfg.AlpacaAPIKey,
		APISecret: cfg.AlpacaSecretKey,
		BaseURL:   cfg.AlpacaBaseURL,
	})
	return &AlpacaBroker{client: client, logger: logger}
}

// SubmitOrder places an order with the Alpaca API and returns the Alpaca order ID.
func (b *AlpacaBroker) SubmitOrder(ctx context.Context, order *entities.Order) (string, error) {
	qty := decimal.NewFromFloat(order.Quantity)

	req := alpaca.PlaceOrderRequest{
		Symbol:      order.Symbol,
		Qty:         &qty,
		Side:        toAlpacaSide(order.Side),
		Type:        toAlpacaType(order.Type),
		TimeInForce: alpaca.Day,
	}

	if order.Type == entities.OrderTypeLimit && order.LimitPrice != nil && *order.LimitPrice > 0 {
		lp := decimal.NewFromFloat(*order.LimitPrice)
		req.LimitPrice = &lp
	}

	b.logger.Info("alpaca.submit_order",
		zap.String("symbol", order.Symbol),
		zap.String("side", string(order.Side)),
		zap.String("type", string(order.Type)),
		zap.Float64("qty", order.Quantity),
	)

	placed, err := b.client.PlaceOrder(req)
	if err != nil {
		return "", fmt.Errorf("alpaca place order: %w", err)
	}

	b.logger.Info("alpaca.order_placed", zap.String("alpaca_id", placed.ID))
	return placed.ID, nil
}

// CancelOrder cancels an open order by its Alpaca-assigned ID.
func (b *AlpacaBroker) CancelOrder(ctx context.Context, brokerID string) error {
	if err := b.client.CancelOrder(brokerID); err != nil {
		return fmt.Errorf("alpaca cancel order %s: %w", brokerID, err)
	}
	b.logger.Info("alpaca.order_cancelled", zap.String("alpaca_id", brokerID))
	return nil
}

// GetOrderStatus polls Alpaca for the current fill status of an order.
func (b *AlpacaBroker) GetOrderStatus(ctx context.Context, brokerID string) (entities.OrderStatus, float64, error) {
	o, err := b.client.GetOrder(brokerID)
	if err != nil {
		return entities.OrderStatusPending, 0, fmt.Errorf("alpaca get order %s: %w", brokerID, err)
	}

	filledQty := 0.0
	if o.FilledQty != nil {
		f, _ := o.FilledQty.Float64()
		filledQty = f
	}

	return fromAlpacaStatus(string(o.Status)), filledQty, nil
}

// GetPortfolioEquity returns the current account equity in USD.
func (b *AlpacaBroker) GetPortfolioEquity(ctx context.Context) (float64, error) {
	acct, err := b.client.GetAccount()
	if err != nil {
		return 0, fmt.Errorf("alpaca get account: %w", err)
	}
	eq, _ := acct.Equity.Float64()
	return eq, nil
}

// GetPositions returns all currently open positions.
func (b *AlpacaBroker) GetPositions(ctx context.Context) ([]entities.Position, error) {
	raw, err := b.client.GetPositions()
	if err != nil {
		return nil, fmt.Errorf("alpaca get positions: %w", err)
	}

	positions := make([]entities.Position, 0, len(raw))
	for _, p := range raw {
		qty, _ := p.Qty.Float64()
		avgEntry, _ := p.AvgEntryPrice.Float64()
		mktVal, _ := p.MarketValue.Float64()
		unrealizedPnL, _ := p.UnrealizedPL.Float64()

		positions = append(positions, entities.Position{
			Symbol:            p.Symbol,
			Quantity:          qty,
			AverageEntryPrice: avgEntry,
			CurrentPrice:      mktVal / qty,
			UnrealizedPnL:     unrealizedPnL,
		})
	}
	return positions, nil
}

// IsMarketOpen returns true if the US equities market is currently open.
func (b *AlpacaBroker) IsMarketOpen(ctx context.Context) (bool, error) {
	clock, err := b.client.GetClock()
	if err != nil {
		return false, fmt.Errorf("alpaca get clock: %w", err)
	}
	return clock.IsOpen, nil
}

// ── helpers ────────────────────────────────────────────────────────────────

func toAlpacaSide(side entities.OrderSide) alpaca.Side {
	if side == entities.OrderSideSell {
		return alpaca.Sell
	}
	return alpaca.Buy
}

func toAlpacaType(t entities.OrderType) alpaca.OrderType {
	if t == entities.OrderTypeLimit {
		return alpaca.Limit
	}
	return alpaca.Market
}

func fromAlpacaStatus(s string) entities.OrderStatus {
	switch s {
	case "filled":
		return entities.OrderStatusFilled
	case "canceled", "cancelled":
		return entities.OrderStatusCanceled
	case "partially_filled":
		return entities.OrderStatusPartiallyFilled
	case "new", "accepted", "pending_new":
		return entities.OrderStatusPending
	default:
		return entities.OrderStatusPending
	}
}
