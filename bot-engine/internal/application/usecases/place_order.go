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

// Domain event types published on the event bus (and relayed to WebSocket
// clients as the versioned events in contracts/, BL-16).
const (
	EventSignalReceived    = "SignalReceived"
	EventOrderPlaced       = "OrderPlaced"
	EventKillSwitchChanged = "KillSwitchChanged"
)

// KillSwitchChecker reports whether trading is halted (BL-19).
type KillSwitchChecker interface {
	Engaged(ctx context.Context) (bool, error)
}

// PlaceOrderUseCase orchestrates the signal-to-order pipeline:
//
//  0. Refuse while the kill switch is engaged (fail closed)
//  1. Fetch the signal from signal-service
//  2. Risk checks: actionable, confidence, account state, daily loss, sizing
//  3. Persist the pending order, audit the submission, submit to the broker
//  4. Record the broker's view of the order, audit it, publish OrderPlaced
//
// Every decision (skip, submission, rejection, unknown outcome) is written to
// the audit log with the inputs it used (ADR-005 §5.8).
type PlaceOrderUseCase struct {
	broker     ports.BrokerPort
	orderRepo  ports.OrderRepositoryPort
	signals    ports.SignalServicePort
	publisher  ports.EventPublisherPort
	riskRules  entities.RiskRules
	audit      ports.AuditLogPort
	killSwitch KillSwitchChecker
	logger     *zap.Logger
}

// NewPlaceOrderUseCase constructs the use case with all dependencies injected.
// Audit defaults to a no-op and there is no kill switch until the With*
// options are applied (main always applies both).
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
		audit:     nopAudit{},
		logger:    logger,
	}
}

// exec is the shared submission path (also used by order intents).
func (uc *PlaceOrderUseCase) exec() *orderExecutor {
	return &orderExecutor{broker: uc.broker, orders: uc.orderRepo, audit: uc.audit}
}

// WithAudit sets the audit log.
func (uc *PlaceOrderUseCase) WithAudit(a ports.AuditLogPort) *PlaceOrderUseCase {
	uc.audit = a
	return uc
}

// WithKillSwitch sets the kill switch checked before every order.
func (uc *PlaceOrderUseCase) WithKillSwitch(k KillSwitchChecker) *PlaceOrderUseCase {
	uc.killSwitch = k
	return uc
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

// Errors the use case adds on top of the typed broker errors (ports.Err*), so
// the HTTP layer can choose a status without parsing messages.
var (
	// ErrSignalUnavailable: the signal service could not provide a signal.
	ErrSignalUnavailable = errors.New("signal unavailable")
	// ErrOutcomeUnknown: the broker may or may not have the order; it is left
	// pending until reconciled (BL-23). Never resubmit on this error.
	ErrOutcomeUnknown = errors.New("order outcome unknown")
	// ErrAuditUnavailable: the audit log refused the pre-submission record, so
	// the order was not sent (fail closed).
	ErrAuditUnavailable = errors.New("audit log unavailable")
)

// minOrderQty is the smallest quantity worth submitting.
var minOrderQty = decimal.RequireFromString("0.001")

// lookupTimeout bounds the reconciliation lookup after an unknown submit outcome.
const lookupTimeout = 5 * time.Second

// Execute runs the use case.
func (uc *PlaceOrderUseCase) Execute(ctx context.Context, input PlaceOrderInput) (*PlaceOrderOutput, error) {
	ctx, span := otel.Tracer("bot-engine").Start(ctx, "PlaceOrderUseCase.Execute")
	defer span.End()
	span.SetAttributes(attribute.String("symbol", input.Symbol), attribute.String("strategy", input.Strategy))

	log := uc.logger.With(zap.String("symbol", input.Symbol), zap.String("strategy", input.Strategy))
	log.Info("place_order.started")
	request := map[string]any{"symbol": input.Symbol, "strategy": input.Strategy}

	// ── Step 0: Kill switch (fail closed) ──────────────────
	if uc.killSwitch != nil {
		engaged, err := uc.killSwitch.Engaged(ctx)
		if err != nil || engaged {
			uc.exec().record(ctx, log, "order.blocked", "", "kill_switch", request)
			if err != nil {
				return nil, fmt.Errorf("%w: state unavailable, refusing: %w", ports.ErrKillSwitchEngaged, err)
			}
			return nil, ports.ErrKillSwitchEngaged
		}
	}

	// ── Step 1: Fetch signal ────────────────────────────────
	signal, err := uc.signals.GetSignal(ctx, input.Symbol, input.Strategy)
	if err != nil {
		return nil, fmt.Errorf("fetching signal: %w: %w", ErrSignalUnavailable, err)
	}
	log.Info("place_order.signal_received", zap.String("direction", signal.Direction), zap.Float64("confidence", signal.Confidence))
	_ = uc.publisher.Publish(ctx, ports.DomainEvent{Type: EventSignalReceived, Payload: signal})

	inputs := map[string]any{
		"symbol": input.Symbol, "strategy": input.Strategy, "signal_id": signal.ID,
		"direction": signal.Direction, "confidence": signal.Confidence, "price": signal.Price.String(),
	}
	skip := func(reason string) (*PlaceOrderOutput, error) {
		log.Info("place_order.skipped", zap.String("reason", reason))
		inputs["reason"] = reason
		uc.exec().record(ctx, log, "order.skipped", "", "skipped", inputs)
		return &PlaceOrderOutput{Signal: signal, Skipped: true, SkipReason: reason}, nil
	}

	// ── Step 2: Signal checks ───────────────────────────────
	if !signal.IsActionable {
		return skip("HOLD signal")
	}
	if signal.Confidence < uc.riskRules.MinConfidence {
		return skip(fmt.Sprintf("confidence %.2f < minimum %.2f", signal.Confidence, uc.riskRules.MinConfidence))
	}

	// ── Step 3: Account state (one read, fail closed) ──────
	acct, err := uc.broker.GetAccount(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting portfolio equity: %w", err)
	}
	inputs["equity"], inputs["last_equity"] = acct.Equity.String(), acct.LastEquity.String()
	if acct.TradingBlocked {
		return skip("broker account is blocked from trading")
	}
	if !acct.Equity.IsPositive() {
		return skip("account equity is not positive")
	}

	// ── Step 4: Daily loss limit (equity vs previous close) ─
	if acct.LastEquity.IsPositive() {
		dailyLossPct := acct.DayPnL().Neg().Div(acct.LastEquity)
		if dailyLossPct.GreaterThanOrEqual(decimal.NewFromFloat(uc.riskRules.MaxDailyLossPct)) {
			log.Warn("place_order.daily_loss_limit_hit")
			return skip(fmt.Sprintf("daily loss limit reached: %s%%", dailyLossPct.Mul(decimal.NewFromInt(100)).StringFixed(2)))
		}
	}

	// ── Step 5: Position sizing ─────────────────────────────
	qty := uc.riskRules.CalcPositionSize(acct.Equity, signal.Price)
	inputs["qty"] = qty.String()
	if qty.LessThan(minOrderQty) {
		return skip("calculated quantity too small")
	}

	// ── Step 6: Build and persist the pending order ─────────
	side := entities.OrderSideBuy
	if signal.Direction == "SELL" {
		side = entities.OrderSideSell
	}
	order, err := entities.NewMarketOrder(input.Symbol, side, qty, input.Strategy, signal.ID)
	if err != nil {
		return nil, fmt.Errorf("creating order entity: %w", err)
	}
	if saveErr := uc.orderRepo.Save(ctx, order); saveErr != nil {
		return nil, fmt.Errorf("saving order: %w", saveErr)
	}

	// ── Step 7: Audit, then submit (order ID = client order ID) ─
	inputs["side"] = string(side)
	if err := uc.audit.Record(ctx, ports.AuditEvent{Action: "order.submit_requested", Target: order.ID.String(), Result: "requested", Payload: inputs}); err != nil {
		log.Error("place_order.audit_failed", zap.Error(err))
		uc.exec().markRejected(ctx, order, log)
		return nil, fmt.Errorf("%w: %w", ErrAuditUnavailable, err)
	}
	placed, err := uc.exec().submit(ctx, order, log)
	if err != nil {
		return nil, err
	}
	submitted := order.ApplyBrokerState(placed.State())
	if updateErr := uc.orderRepo.Update(ctx, submitted); updateErr != nil {
		log.Error("place_order.update_failed", zap.Error(updateErr))
	}
	uc.exec().record(ctx, log, "order.submitted", submitted.ID.String(), string(submitted.Status), map[string]any{
		"broker_id": submitted.BrokerID, "status": string(submitted.Status),
		"filled_qty": submitted.FilledQty.String(), "qty": qty.String(),
	})

	// ── Step 8: Publish event (Observer pattern) ───────────
	_ = uc.publisher.Publish(ctx, ports.DomainEvent{Type: EventOrderPlaced, Payload: submitted})

	log.Info("place_order.completed",
		zap.String("order_id", submitted.ID.String()),
		zap.String("broker_id", submitted.BrokerID),
		zap.String("status", string(submitted.Status)),
		zap.String("qty", qty.String()),
	)
	span.SetAttributes(attribute.String("order_id", submitted.ID.String()))
	return &PlaceOrderOutput{Order: submitted, Signal: signal, Skipped: false}, nil
}

type nopAudit struct{}

func (nopAudit) Record(context.Context, ports.AuditEvent) error { return nil }
