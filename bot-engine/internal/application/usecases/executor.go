package usecases

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"

	"github.com/trading-bot/bot-engine/internal/domain/entities"
	"github.com/trading-bot/bot-engine/internal/domain/ports"
)

// orderExecutor is the single submission path to the broker: it never
// resubmits blindly, and an unknown outcome is resolved by client order ID
// (ADR-004, ADR-005 §5.4). Both the legacy order route and approved intents
// use it.
type orderExecutor struct {
	broker ports.BrokerPort
	orders ports.OrderRepositoryPort
	audit  ports.AuditLogPort
}

// submit sends the order. When the outcome is unknown (broker unavailable or
// timed out) it looks the order up by client ID instead of guessing:
//   - found: the broker has it — adopt the broker's state;
//   - not found: it was never placed — mark it rejected;
//   - lookup failed: leave it pending and return an error (needs reconciliation).
func (x *orderExecutor) submit(ctx context.Context, order *entities.Order, log *zap.Logger) (ports.BrokerOrder, error) {
	target := order.ID.String()
	placed, err := x.broker.SubmitOrder(ctx, ports.SubmitRequestFor(order))
	if err == nil {
		return placed, nil
	}
	if !errors.Is(err, ports.ErrUnavailable) {
		x.markRejected(ctx, order, log)
		x.record(ctx, log, "order.rejected", target, "rejected", map[string]any{"error_class": errorClass(err)})
		return ports.BrokerOrder{}, fmt.Errorf("broker rejected order: %w", err)
	}

	log.Warn("place_order.submit_outcome_unknown", zap.Error(err), zap.String("client_order_id", order.ClientOrderID()))
	lookupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), lookupTimeout)
	defer cancel()
	found, lookupErr := x.broker.GetOrderByClientID(lookupCtx, order.ClientOrderID())
	switch {
	case lookupErr == nil:
		log.Info("place_order.submit_reconciled", zap.String("broker_id", found.ID))
		return found, nil
	case errors.Is(lookupErr, ports.ErrNotFound):
		x.markRejected(ctx, order, log)
		x.record(ctx, log, "order.rejected", target, "not_placed", map[string]any{"error_class": errorClass(err)})
		return ports.BrokerOrder{}, fmt.Errorf("broker did not accept order: %w", err)
	default:
		x.record(ctx, log, "order.outcome_unknown", target, "pending", map[string]any{"error_class": errorClass(err)})
		return ports.BrokerOrder{}, fmt.Errorf("%w, left pending for reconciliation: %w", ErrOutcomeUnknown, err)
	}
}

func (x *orderExecutor) markRejected(ctx context.Context, order *entities.Order, log *zap.Logger) {
	if err := x.orders.Update(context.WithoutCancel(ctx), order.Transition(entities.OrderStatusRejected)); err != nil {
		log.Error("place_order.update_failed", zap.Error(err))
	}
}

// record writes a best-effort audit event; a failure is logged, not returned.
func (x *orderExecutor) record(ctx context.Context, log *zap.Logger, action, target, result string, payload map[string]any) {
	if err := x.audit.Record(context.WithoutCancel(ctx), ports.AuditEvent{Action: action, Target: target, Result: result, Payload: payload}); err != nil {
		log.Error("place_order.audit_failed", zap.String("action", action), zap.Error(err))
	}
}

// errorClass names a typed broker error for the audit log without its text.
func errorClass(err error) string {
	for _, c := range []struct {
		err  error
		name string
	}{
		{ports.ErrInsufficientFunds, "insufficient_funds"}, {ports.ErrRejected, "rejected"},
		{ports.ErrInvalidOrder, "invalid_order"}, {ports.ErrRateLimited, "rate_limited"},
		{ports.ErrUnavailable, "unavailable"}, {ports.ErrNotFound, "not_found"},
	} {
		if errors.Is(err, c.err) {
			return c.name
		}
	}
	return "other"
}
