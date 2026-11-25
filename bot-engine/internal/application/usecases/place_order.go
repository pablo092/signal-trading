// Package usecases contains the application layer use cases.
// Each use case has a single responsibility and depends only on ports (interfaces).
// No framework code (Gin, HTTP) lives here.
package usecases

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"

	"github.com/trading-bot/bot-engine/internal/domain/entities"
	"github.com/trading-bot/bot-engine/internal/domain/ports"
)

// PlaceOrderUseCase orchestrates the full signal-to-order pipeline:
//
//  1. Fetch signal from signal service
//  2. Apply risk rules (confidence threshold, position sizing)
//  3. Create domain Order entity
//  4. Submit to broker
//  5. Persist to repository
//  6. Publish OrderPlaced event
//
// Follows the Command pattern — PlaceOrderInput is the command,
// PlaceOrderOutput is the result. Supports undo via CancelOrderUseCase.
type PlaceOrderUseCase struct {
	broker    ports.BrokerPort
	orderRepo ports.OrderRepositoryPort
	signals   ports.SignalServicePort
	publisher ports.EventPublisherPort
	riskRules entities.RiskRules
	logger    *zap.Logger
}

// NewPlaceOrderUseCase constructs the use case with all dependencies injected.
func NewPlaceOrderUseCase(
	broker ports.BrokerPort,
	orderRepo ports.OrderRepositoryPort,
	signals ports.SignalServicePort,
	publisher ports.EventPublisherPort,
	riskRules entities.RiskRules,
	logger *zap.Logger,
) *PlaceOrderUseCase {
	return &PlaceOrderUseCase{
		broker:    broker,
		orderRepo: orderRepo,
		signals:   signals,
		publisher: publisher,
		riskRules: riskRules,
		logger:    logger,
	}
}

// PlaceOrderInput is the command DTO.
type PlaceOrderInput struct {
	Symbol   string
	Strategy string
}

// PlaceOrderOutput is the result DTO.
type PlaceOrderOutput struct {
	Order      *entities.Order
	Signal     *ports.Signal
	Skipped    bool // true if risk rules rejected the signal
	SkipReason string
}

// Execute runs the use case. Idempotent — safe to retry on transient errors.
func (uc *PlaceOrderUseCase) Execute(ctx context.Context, input PlaceOrderInput) (*PlaceOrderOutput, error) {
	tracer := otel.Tracer("bot-engine")
	ctx, span := tracer.Start(ctx, "PlaceOrderUseCase.Execute")
	defer span.End()

	span.SetAttributes(
		attribute.String("symbol", input.Symbol),
		attribute.String("strategy", input.Strategy),
	)

	log := uc.logger.With(
		zap.String("symbol", input.Symbol),
		zap.String("strategy", input.Strategy),
	)

	log.Info("place_order.started")

	// ── Step 1: Fetch signal ────────────────────────────────
	signal, err := uc.signals.GetSignal(ctx, input.Symbol, input.Strategy)
	if err != nil {
		return nil, fmt.Errorf("fetching signal: %w: %w", ErrSignalUnavailable, err)
	}

	log.Info("place_order.signal_received",
		zap.String("direction", signal.Direction),
		zap.Float64("confidence", signal.Confidence),
	)

	// ── Step 2: Risk checks ─────────────────────────────────
	if !signal.IsActionable {
		log.Info("place_order.skipped", zap.String("reason", "HOLD signal"))
		return &PlaceOrderOutput{Signal: signal, Skipped: true, SkipReason: "HOLD signal"}, nil
	}
	if signal.Confidence < uc.riskRules.MinConfidence {
		reason := fmt.Sprintf("confidence %.2f < minimum %.2f", signal.Confidence, uc.riskRules.MinConfidence)
		log.Info("place_order.skipped", zap.String("reason", reason))
		return &PlaceOrderOutput{Signal: signal, Skipped: true, SkipReason: reason}, nil
	}

	// ── Step 3: Account state (one read, fail closed) ──────
	acct, err := uc.broker.GetAccount(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting portfolio equity: %w", err)
	}
	if acct.TradingBlocked {
		return &PlaceOrderOutput{Signal: signal, Skipped: true, SkipReason: "broker account is blocked from trading"}, nil
	}
	if !acct.Equity.IsPositive() {
		return &PlaceOrderOutput{Signal: signal, Skipped: true, SkipReason: "account equity is not positive"}, nil
	}

	// ── Step 4: Daily loss limit (equity vs previous close) ─
	if acct.LastEquity.IsPositive() {
		dailyLossPct := acct.DayPnL().Neg().Div(acct.LastEquity)
		if dailyLossPct.GreaterThanOrEqual(decimal.NewFromFloat(uc.riskRules.MaxDailyLossPct)) {
			reason := fmt.Sprintf("daily loss limit reached: %s%%", dailyLossPct.Mul(decimal.NewFromInt(100)).StringFixed(2))
			log.Warn("place_order.daily_loss_limit_hit", zap.String("reason", reason))
			return &PlaceOrderOutput{Signal: signal, Skipped: true, SkipReason: reason}, nil
		}
	}

	// ── Step 5: Position sizing ─────────────────────────────
	qty := uc.riskRules.CalcPositionSize(acct.Equity, signal.Price)
	if qty.LessThan(minOrderQty) {
		return &PlaceOrderOutput{Signal: signal, Skipped: true, SkipReason: "calculated quantity too small"}, nil
	}

	// ── Step 6: Build Order entity ─────────────────────────
	side := entities.OrderSideBuy
	if signal.Direction == "SELL" {
		side = entities.OrderSideSell
	}
	order, err := entities.NewMarketOrder(input.Symbol, side, qty, input.Strategy, signal.ID)
	if err != nil {
		return nil, fmt.Errorf("creating order entity: %w", err)
	}

	// ── Step 7: Persist pending ────────────────────────────
	if saveErr := uc.orderRepo.Save(ctx, order); saveErr != nil {
		return nil, fmt.Errorf("saving order: %w", saveErr)
	}

	// ── Step 8: Submit to broker (order ID = client order ID) ─
	placed, err := uc.submit(ctx, order, log)
	if err != nil {
		return nil, err
	}
	submitted := order.ApplyBrokerState(placed.State())
	if updateErr := uc.orderRepo.Update(ctx, submitted); updateErr != nil {
		log.Error("place_order.update_failed", zap.Error(updateErr))
	}

	// ── Step 9: Publish event (Observer pattern) ───────────
	_ = uc.publisher.Publish(ctx, ports.DomainEvent{
		Type:    "OrderPlaced",
		Payload: submitted,
	})

	log.Info("place_order.completed",
		zap.String("order_id", submitted.ID.String()),
		zap.String("broker_id", submitted.BrokerID),
		zap.String("status", string(submitted.Status)),
		zap.String("qty", qty.String()),
	)

	span.SetAttributes(attribute.String("order_id", submitted.ID.String()))

	return &PlaceOrderOutput{Order: submitted, Signal: signal, Skipped: false}, nil
}

// Errors the use case adds on top of the typed broker errors (ports.Err*), so
// the HTTP layer can choose a status without parsing messages.
var (
	// ErrSignalUnavailable: the signal service could not provide a signal.
	ErrSignalUnavailable = errors.New("signal unavailable")
	// ErrOutcomeUnknown: the broker may or may not have the order; it is left
	// pending until reconciled (BL-23). Never resubmit on this error.
	ErrOutcomeUnknown = errors.New("order outcome unknown")
)

// minOrderQty is the smallest quantity worth submitting.
var minOrderQty = decimal.RequireFromString("0.001")

// lookupTimeout bounds the reconciliation lookup after an unknown submit outcome.
const lookupTimeout = 5 * time.Second

// submit sends the order. When the outcome is unknown (broker unavailable or
// timed out) it looks the order up by client ID instead of guessing:
//   - found: the broker has it — adopt the broker's state;
//   - not found: it was never placed — mark it rejected;
//   - lookup failed: leave it pending and return an error (needs reconciliation).
func (uc *PlaceOrderUseCase) submit(ctx context.Context, order *entities.Order, log *zap.Logger) (ports.BrokerOrder, error) {
	placed, err := uc.broker.SubmitOrder(ctx, ports.SubmitRequestFor(order))
	if err == nil {
		return placed, nil
	}
	if !errors.Is(err, ports.ErrUnavailable) {
		uc.markRejected(ctx, order, log)
		return ports.BrokerOrder{}, fmt.Errorf("broker rejected order: %w", err)
	}

	log.Warn("place_order.submit_outcome_unknown", zap.Error(err), zap.String("client_order_id", order.ClientOrderID()))
	lookupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), lookupTimeout)
	defer cancel()
	found, lookupErr := uc.broker.GetOrderByClientID(lookupCtx, order.ClientOrderID())
	switch {
	case lookupErr == nil:
		log.Info("place_order.submit_reconciled", zap.String("broker_id", found.ID))
		return found, nil
	case errors.Is(lookupErr, ports.ErrNotFound):
		uc.markRejected(ctx, order, log)
		return ports.BrokerOrder{}, fmt.Errorf("broker did not accept order: %w", err)
	default:
		return ports.BrokerOrder{}, fmt.Errorf("%w, left pending for reconciliation: %w", ErrOutcomeUnknown, err)
	}
}

func (uc *PlaceOrderUseCase) markRejected(ctx context.Context, order *entities.Order, log *zap.Logger) {
	if err := uc.orderRepo.Update(context.WithoutCancel(ctx), order.Transition(entities.OrderStatusRejected)); err != nil {
		log.Error("place_order.update_failed", zap.Error(err))
	}
}
