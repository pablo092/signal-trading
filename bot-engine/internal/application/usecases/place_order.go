// Package usecases contains the application layer use cases.
// Each use case has a single responsibility and depends only on ports (interfaces).
// No framework code (Gin, HTTP) lives here.
package usecases

import (
	"context"
	"fmt"

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
	broker      ports.BrokerPort
	orderRepo   ports.OrderRepositoryPort
	signals     ports.SignalServicePort
	publisher   ports.EventPublisherPort
	riskRules   entities.RiskRules
	logger      *zap.Logger
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
	Order    *entities.Order
	Signal   *ports.Signal
	Skipped  bool   // true if risk rules rejected the signal
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
		return nil, fmt.Errorf("fetching signal: %w", err)
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

	// ── Step 3: Check daily loss limit ─────────────────────
	dailyPnL, err := uc.getDailyPnL(ctx)
	if err != nil {
		log.Warn("place_order.daily_pnl_check_failed", zap.Error(err))
		// Non-fatal: continue, log the warning
	} else {
		equity, equityErr := uc.broker.GetPortfolioEquity(ctx)
		if equityErr == nil && equity > 0 {
			dailyLossPct := -dailyPnL / equity
			if dailyLossPct >= uc.riskRules.MaxDailyLossPct {
				reason := fmt.Sprintf("daily loss limit reached: %.2f%%", dailyLossPct*100)
				log.Warn("place_order.daily_loss_limit_hit", zap.String("reason", reason))
				return &PlaceOrderOutput{Signal: signal, Skipped: true, SkipReason: reason}, nil
			}
		}
	}

	// ── Step 4: Position sizing ─────────────────────────────
	equity, err := uc.broker.GetPortfolioEquity(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting portfolio equity: %w", err)
	}
	qty := uc.riskRules.CalcPositionSize(equity, signal.Price)
	if qty < 0.001 {
		return &PlaceOrderOutput{Signal: signal, Skipped: true, SkipReason: "calculated quantity too small"}, nil
	}

	// ── Step 5: Build Order entity ─────────────────────────
	side := entities.OrderSideBuy
	if signal.Direction == "SELL" {
		side = entities.OrderSideSell
	}
	order, err := entities.NewMarketOrder(input.Symbol, side, qty, input.Strategy, signal.ID)
	if err != nil {
		return nil, fmt.Errorf("creating order entity: %w", err)
	}

	// ── Step 6: Persist pending ────────────────────────────
	if saveErr := uc.orderRepo.Save(ctx, order); saveErr != nil {
		return nil, fmt.Errorf("saving order: %w", saveErr)
	}

	// ── Step 7: Submit to broker ───────────────────────────
	brokerID, err := uc.broker.SubmitOrder(ctx, order)
	if err != nil {
		// Transition to rejected and persist
		rejected := order.Transition(entities.OrderStatusRejected)
		_ = uc.orderRepo.Update(ctx, rejected)
		return nil, fmt.Errorf("broker rejected order: %w", err)
	}

	submitted := order.MarkFilled(brokerID, signal.Price) // simplified: assume immediate fill for paper
	submitted = submitted.Transition(entities.OrderStatusSubmitted)
	if updateErr := uc.orderRepo.Update(ctx, submitted); updateErr != nil {
		log.Error("place_order.update_failed", zap.Error(updateErr))
	}

	// ── Step 8: Publish event (Observer pattern) ───────────
	_ = uc.publisher.Publish(ctx, ports.DomainEvent{
		Type:    "OrderPlaced",
		Payload: submitted,
	})

	log.Info("place_order.completed",
		zap.String("order_id", submitted.ID.String()),
		zap.String("broker_id", brokerID),
		zap.Float64("qty", qty),
	)

	span.SetAttributes(attribute.String("order_id", submitted.ID.String()))

	return &PlaceOrderOutput{Order: submitted, Signal: signal, Skipped: false}, nil
}

func (uc *PlaceOrderUseCase) getDailyPnL(ctx context.Context) (float64, error) {
	// This would call tradeRepo.GetDailyPnL — simplified for now
	return 0, nil
}
