package usecases

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/trading-bot/bot-engine/internal/domain/ports"
)

// ErrKillSwitchForced: KILL_SWITCH=true holds the switch engaged; it cannot be
// released through the API, only by redeploying without the override.
var ErrKillSwitchForced = errors.New("kill switch is forced on by the KILL_SWITCH override")

// cancelAllTimeout bounds the broker cancel-all call made when engaging.
const cancelAllTimeout = 15 * time.Second

// KillSwitchStatus is the externally visible kill-switch state.
type KillSwitchStatus struct {
	ports.KillSwitchState
	Forced bool // KILL_SWITCH env override is on
}

// EngageResult reports what engaging did.
type EngageResult struct {
	Status    KillSwitchStatus
	Persisted bool  // false when the store failed; the switch is still engaged in memory
	CancelErr error // nil when every open order was canceled
}

// KillSwitchService implements the emergency stop (ADR-005 §5.7).
//
// Engaging always succeeds locally: an in-memory latch is set first, so the
// switch holds even when the store is down. Then the flag is persisted, every
// open broker order is canceled, and the change is audited, alerted and
// published. Releasing needs a separate explicit call, a reason, and a
// successful write; it is refused while the env override is on.
// Engaged fails closed: a store error reads as engaged.
type KillSwitchService struct {
	mu        sync.Mutex
	store     ports.KillSwitchStore
	broker    ports.BrokerPort
	audit     ports.AuditLogPort
	publisher ports.EventPublisherPort
	forced    bool
	latch     atomic.Bool // engaged in this process (set before persisting)
	logger    *zap.Logger
}

// NewKillSwitchService builds the service. forced is the KILL_SWITCH override.
func NewKillSwitchService(store ports.KillSwitchStore, broker ports.BrokerPort, audit ports.AuditLogPort,
	publisher ports.EventPublisherPort, forced bool, logger *zap.Logger) *KillSwitchService {
	return &KillSwitchService{store: store, broker: broker, audit: audit, publisher: publisher, forced: forced, logger: logger}
}

// Init loads the persisted flag and applies the env override (engaging, which
// also cancels open orders). Call once at startup.
func (s *KillSwitchService) Init(ctx context.Context) error {
	st, err := s.store.Load(ctx)
	if err != nil {
		s.latch.Store(true) // cannot know: fail closed until the store answers
		return fmt.Errorf("kill switch: load state: %w", err)
	}
	s.latch.Store(st.Engaged)
	if s.forced && !st.Engaged {
		_, err := s.Engage(ports.WithActor(ctx, ports.SystemActor), "KILL_SWITCH environment override")
		return err
	}
	if st.Engaged {
		s.logger.Warn("kill_switch.engaged_at_startup", zap.String("reason", st.Reason), zap.String("by", st.UpdatedBy))
	}
	return nil
}

// Engaged reports whether trading is halted. Store errors read as engaged.
func (s *KillSwitchService) Engaged(ctx context.Context) (bool, error) {
	if s.forced || s.latch.Load() {
		return true, nil
	}
	st, err := s.store.Load(ctx)
	if err != nil {
		return true, fmt.Errorf("kill switch: load state: %w", err)
	}
	if st.Engaged {
		s.latch.Store(true) // engaged by another writer
	}
	return st.Engaged, nil
}

// EngagedNow is the last known state, for metrics (never blocks).
func (s *KillSwitchService) EngagedNow() bool { return s.forced || s.latch.Load() }

// Status returns the current state.
func (s *KillSwitchService) Status(ctx context.Context) (KillSwitchStatus, error) {
	st, err := s.store.Load(ctx)
	if err != nil {
		return KillSwitchStatus{KillSwitchState: ports.KillSwitchState{Engaged: true}, Forced: s.forced}, fmt.Errorf("kill switch: load state: %w", err)
	}
	st.Engaged = st.Engaged || s.forced || s.latch.Load()
	return KillSwitchStatus{KillSwitchState: st, Forced: s.forced}, nil
}

// Engage halts trading and cancels every open broker order.
func (s *KillSwitchService) Engage(ctx context.Context, reason string) (EngageResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	actor := ports.ActorFrom(ctx)
	s.latch.Store(true) // first: holds even if everything below fails
	st := ports.KillSwitchState{Engaged: true, Reason: reason, UpdatedBy: actor.ID, UpdatedAt: time.Now().UTC()}

	res := EngageResult{Status: KillSwitchStatus{KillSwitchState: st, Forced: s.forced}, Persisted: true}
	if err := s.store.Save(context.WithoutCancel(ctx), st); err != nil {
		res.Persisted = false
		s.logger.Error("kill_switch.persist_failed", zap.Error(err))
	}

	cancelCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cancelAllTimeout)
	defer cancel()
	res.CancelErr = s.broker.CancelAllOrders(cancelCtx)

	payload := map[string]any{"reason": reason, "persisted": res.Persisted, "cancel_all": cancelOutcome(res.CancelErr), "forced": s.forced}
	if err := s.audit.Record(context.WithoutCancel(ctx), ports.AuditEvent{Action: "kill_switch.engaged", Target: "kill_switch", Result: "engaged", Payload: payload}); err != nil {
		s.logger.Error("kill_switch.audit_failed", zap.Error(err))
	}
	// Alert: an error-level log line (alert rules key on it), plus a live event.
	s.logger.Error("ALERT kill_switch.engaged",
		zap.String("reason", reason), zap.String("actor_id", actor.ID), zap.String("actor_type", actor.Type),
		zap.Bool("persisted", res.Persisted), zap.String("cancel_all", cancelOutcome(res.CancelErr)), zap.NamedError("cancel_error", res.CancelErr))
	_ = s.publisher.Publish(context.WithoutCancel(ctx), ports.DomainEvent{Type: EventKillSwitchChanged, Payload: res.Status})
	return res, nil
}

// Release resumes trading. It needs a successful write and is refused while forced.
func (s *KillSwitchService) Release(ctx context.Context, reason string) (KillSwitchStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.forced {
		return KillSwitchStatus{KillSwitchState: ports.KillSwitchState{Engaged: true}, Forced: true}, ErrKillSwitchForced
	}
	actor := ports.ActorFrom(ctx)
	st := ports.KillSwitchState{Engaged: false, Reason: reason, UpdatedBy: actor.ID, UpdatedAt: time.Now().UTC()}
	if err := s.store.Save(ctx, st); err != nil {
		return KillSwitchStatus{KillSwitchState: ports.KillSwitchState{Engaged: true}}, fmt.Errorf("kill switch: save state: %w", err)
	}
	s.latch.Store(false)
	if err := s.audit.Record(context.WithoutCancel(ctx), ports.AuditEvent{Action: "kill_switch.released", Target: "kill_switch", Result: "released", Payload: map[string]any{"reason": reason}}); err != nil {
		s.logger.Error("kill_switch.audit_failed", zap.Error(err))
	}
	s.logger.Warn("kill_switch.released", zap.String("reason", reason), zap.String("actor_id", actor.ID))
	status := KillSwitchStatus{KillSwitchState: st}
	_ = s.publisher.Publish(context.WithoutCancel(ctx), ports.DomainEvent{Type: EventKillSwitchChanged, Payload: status})
	return status, nil
}

func cancelOutcome(err error) string {
	if err != nil {
		return "failed"
	}
	return "ok"
}
