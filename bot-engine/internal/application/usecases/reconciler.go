package usecases

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/trading-bot/bot-engine/internal/domain/entities"
	"github.com/trading-bot/bot-engine/internal/domain/ports"
)

// ReconcilerDeps are the Reconciler dependencies.
type ReconcilerDeps struct {
	Broker    ports.BrokerPort
	Orders    ports.OrderRepositoryPort
	Intents   ports.IntentRepositoryPort
	Audit     ports.AuditLogPort
	Publisher ports.EventPublisherPort
	Logger    *zap.Logger
	// PollEvery is how often open orders are re-read from the broker (default 30s).
	PollEvery time.Duration
	// UnknownGrace is how long an order left pending by an unknown submit
	// outcome may be missing at the broker before it counts as never placed.
	UnknownGrace time.Duration
}

// Reconciler keeps local orders and intents in step with the broker (BL-23).
// It follows the broker's trade-update stream and also polls open orders,
// because a stream can miss events while disconnected. Both paths go through
// Apply, which is idempotent.
type Reconciler struct {
	d   ReconcilerDeps
	now func() time.Time
}

// NewReconciler builds the reconciler with defaults for unset intervals.
func NewReconciler(d ReconcilerDeps) *Reconciler {
	if d.PollEvery <= 0 {
		d.PollEvery = 30 * time.Second
	}
	if d.UnknownGrace <= 0 {
		d.UnknownGrace = 2 * time.Minute
	}
	return &Reconciler{d: d, now: time.Now}
}

// openStatuses are local order states the broker may still change.
var openStatuses = []entities.OrderStatus{entities.OrderStatusPending, entities.OrderStatusSubmitted, entities.OrderStatusPartiallyFilled}

// Run follows the stream (reconnecting with backoff) and polls until ctx ends.
func (r *Reconciler) Run(ctx context.Context) {
	go r.stream(ctx)
	r.Poll(ctx) // catch up on anything that changed while the engine was down
	t := time.NewTicker(r.d.PollEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.Poll(ctx)
		}
	}
}

func (r *Reconciler) stream(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		started := r.now()
		err := r.d.Broker.StreamTradeUpdates(ctx, func(u ports.TradeUpdate) { r.Apply(ctx, u.Order) })
		if ctx.Err() != nil {
			return
		}
		if r.now().Sub(started) > time.Minute {
			backoff = time.Second // the stream was healthy for a while: reconnect quickly
		}
		r.d.Logger.Warn("reconciler.stream_disconnected", zap.String("error_class", errorClass(err)), zap.Duration("retry_in", backoff))
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, time.Minute)
	}
}

// Poll re-reads every open order from the broker; it returns how many changed.
func (r *Reconciler) Poll(ctx context.Context) int {
	changed := 0
	for _, st := range openStatuses {
		orders, err := r.d.Orders.FindByStatus(ctx, st)
		if err != nil {
			r.d.Logger.Error("reconciler.list_failed", zap.Error(err))
			continue
		}
		for _, o := range orders {
			bo, err := r.d.Broker.GetOrderByClientID(ctx, o.ClientOrderID())
			switch {
			case err == nil:
				if r.Apply(ctx, bo) {
					changed++
				}
			case errors.Is(err, ports.ErrNotFound) && o.Status == entities.OrderStatusPending && r.now().Sub(o.CreatedAt) > r.d.UnknownGrace:
				// Never reached the broker: safe to close, nothing to cancel.
				r.closeNeverPlaced(ctx, o)
				changed++
			case err != nil:
				r.d.Logger.Warn("reconciler.lookup_failed", zap.String("order_id", o.ID.String()), zap.String("error_class", errorClass(err)))
			}
		}
	}
	return changed
}

// Apply records the broker's view of one order. It reports whether anything changed.
func (r *Reconciler) Apply(ctx context.Context, bo ports.BrokerOrder) bool {
	id, err := uuid.Parse(bo.ClientOrderID)
	if err != nil {
		return false // not an order this engine placed
	}
	o, err := r.d.Orders.FindByID(ctx, id)
	if err != nil {
		if !errors.Is(err, ports.ErrOrderNotFound) {
			r.d.Logger.Error("reconciler.order_lookup_failed", zap.Error(err))
		}
		return false
	}
	state := bo.State()
	if o.Status == state.Status && o.FilledQty.Equal(state.FilledQty) && o.BrokerID == state.BrokerID {
		return false
	}
	if o.IsTerminal() {
		return false // a terminal local state is never reopened
	}
	updated := o.ApplyBrokerState(state)
	if err := r.d.Orders.Update(ctx, updated); err != nil {
		r.d.Logger.Error("reconciler.order_update_failed", zap.Error(err))
		return false
	}
	r.record(ctx, "order.reconciled", updated.ID.String(), string(updated.Status), map[string]any{
		"from": string(o.Status), "to": string(updated.Status), "broker_id": updated.BrokerID,
		"filled_qty": updated.FilledQty.String(),
	})
	if updated.Status == entities.OrderStatusFilled {
		_ = r.d.Publisher.Publish(ctx, ports.DomainEvent{Type: EventOrderFilled, Payload: updated})
	}
	r.advanceIntent(ctx, updated)
	return true
}

// advanceIntent moves the intent that produced order to match it.
func (r *Reconciler) advanceIntent(ctx context.Context, o *entities.Order) {
	if r.d.Intents == nil {
		return
	}
	it, err := r.d.Intents.Get(ctx, o.ID)
	if err != nil {
		if !errors.Is(err, ports.ErrIntentNotFound) {
			r.d.Logger.Error("reconciler.intent_lookup_failed", zap.Error(err))
		}
		return // legacy /orders/place orders have no intent
	}
	target := entities.IntentStatusForOrder(o.Status)
	if it.Status == target || !entities.CanTransition(it.Status, target) {
		return
	}
	next, err := it.Transition(target, r.now())
	if err != nil {
		return
	}
	if target == entities.IntentRejected {
		next.RejectionCode = entities.RejectionBrokerRejected
	}
	if err := r.d.Intents.Update(ctx, next, it.Status); err != nil {
		if !errors.Is(err, ports.ErrStaleIntent) {
			r.d.Logger.Error("reconciler.intent_update_failed", zap.Error(err))
		}
		return
	}
	r.record(ctx, "intent."+string(target), it.ID.String(), string(target), map[string]any{"from": string(it.Status), "order_status": string(o.Status)})
	_ = r.d.Publisher.Publish(ctx, ports.DomainEvent{Type: EventIntentUpdated, Payload: IntentView{Intent: next, Order: o}})
}

func (r *Reconciler) closeNeverPlaced(ctx context.Context, o *entities.Order) {
	rejected := o.Transition(entities.OrderStatusRejected)
	if err := r.d.Orders.Update(ctx, rejected); err != nil {
		r.d.Logger.Error("reconciler.order_update_failed", zap.Error(err))
		return
	}
	r.record(ctx, "order.reconciled", o.ID.String(), "not_placed", map[string]any{"from": string(o.Status), "to": "rejected"})
	r.advanceIntent(ctx, rejected)
}

func (r *Reconciler) record(ctx context.Context, action, target, result string, payload map[string]any) {
	if err := r.d.Audit.Record(context.WithoutCancel(ctx), ports.AuditEvent{Action: action, Target: target, Result: result, Payload: payload}); err != nil {
		r.d.Logger.Error("reconciler.audit_failed", zap.String("action", action), zap.Error(err))
	}
}
