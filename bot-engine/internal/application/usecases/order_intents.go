package usecases

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/trading-bot/bot-engine/internal/domain/entities"
	"github.com/trading-bot/bot-engine/internal/domain/ports"
)

// Domain events for intents and fills (relayed on /ws as intent.updated and order.filled).
const (
	EventIntentUpdated = "IntentUpdated"
	EventOrderFilled   = "OrderFilled"
)

// Errors the intent service returns; the HTTP layer maps each to a status.
var (
	ErrIntentNotPending     = errors.New("order intent is not pending approval")
	ErrIntentExpired        = errors.New("order intent expired")
	ErrNotIntentOwner       = errors.New("only the actor that created the intent may cancel it")
	ErrExecutionDisabled    = errors.New("order execution is disabled")
	ErrIdempotencyKeyReused = errors.New("idempotency key reused with a different request")
	ErrIntentRateLimited    = errors.New("order intent rate limit exceeded")
)

// approveTimeout bounds one approval: risk inputs, submission and lookup.
const approveTimeout = 40 * time.Second

// IntentPolicy configures the intent lifecycle.
type IntentPolicy struct {
	TTL              time.Duration
	RateLimitPerHour int
	// ExecutionEnabled gates approval (ADR-005 §5.2): without it nothing is submitted.
	ExecutionEnabled bool
}

// IntentView is an intent and, once submitted, its order.
type IntentView struct {
	Intent *entities.OrderIntent
	Order  *entities.Order
}

// IntentDeps are the IntentService dependencies.
type IntentDeps struct {
	Intents    ports.IntentRepositoryPort
	Orders     ports.OrderRepositoryPort
	Signals    ports.SignalServicePort
	Broker     ports.BrokerPort
	Risk       *RiskEngine
	KillSwitch KillSwitchChecker
	Audit      ports.AuditLogPort
	Publisher  ports.EventPublisherPort
	Policy     IntentPolicy
	Logger     *zap.Logger
}

// IntentService runs the order-intent pipeline (ADR-005 §5.3–5.5): create
// (idempotent, risk-checked), human approval, single-writer submission,
// rejection, cancellation and expiry. Every transition is audited and published.
type IntentService struct {
	d       IntentDeps
	exec    *orderExecutor
	limiter *actorLimiter
	now     func() time.Time
	// execMu serializes approvals: one submission at a time in this process.
	// Across processes the status compare-and-set is the guard (ADR-005 §5.6).
	execMu sync.Mutex
}

// NewIntentService builds the service. Audit and Publisher are required.
func NewIntentService(d IntentDeps) *IntentService {
	return &IntentService{
		d:       d,
		exec:    &orderExecutor{broker: d.Broker, orders: d.Orders, audit: d.Audit},
		limiter: newActorLimiter(d.Policy.RateLimitPerHour, time.Hour),
		now:     time.Now,
	}
}

// CreateIntentInput is a proposal. The caller never chooses side or quantity.
type CreateIntentInput struct {
	Actor          ports.Actor
	Symbol         string
	Strategy       string
	IdempotencyKey string
}

// CreateResult is the created intent, or the original one on a replay.
type CreateResult struct {
	View     IntentView
	Replayed bool
}

// RequestHash identifies a creation request, to detect a reused idempotency key.
func RequestHash(symbol, strategy string) string {
	sum := sha256.Sum256([]byte(symbol + "\n" + strategy))
	return hex.EncodeToString(sum[:])
}

// Create proposes an order. It returns the intent in pending_approval (risk
// passed) or rejected (risk failed); both are persisted and audited.
func (s *IntentService) Create(ctx context.Context, in CreateIntentInput) (CreateResult, error) {
	log := s.d.Logger.With(zap.String("actor_id", in.Actor.ID))
	symbol, strategy := strings.ToUpper(strings.TrimSpace(in.Symbol)), strings.TrimSpace(in.Strategy)
	hash := RequestHash(symbol, strategy)

	// Idempotency (ADR-005 §5.4): (actor, key) is unique; a replay returns the original.
	if prev, err := s.d.Intents.FindByIdempotencyKey(ctx, in.Actor.ID, in.IdempotencyKey); err == nil {
		return s.replay(prev, hash)
	} else if !errors.Is(err, ports.ErrIntentNotFound) {
		return CreateResult{}, fmt.Errorf("idempotency lookup: %w", err)
	}

	request := map[string]any{"symbol": symbol, "strategy": strategy, "idempotency_key": in.IdempotencyKey}
	if engaged, err := s.d.KillSwitch.Engaged(ctx); err != nil || engaged {
		s.exec.record(ctx, log, "intent.blocked", "", "kill_switch", request)
		if err != nil {
			return CreateResult{}, fmt.Errorf("%w: state unavailable, refusing: %w", ports.ErrKillSwitchEngaged, err)
		}
		return CreateResult{}, ports.ErrKillSwitchEngaged
	}
	if !s.limiter.Allow(in.Actor.ID, s.now()) {
		s.exec.record(ctx, log, "intent.rate_limited", "", "rate_limited", request)
		return CreateResult{}, ErrIntentRateLimited
	}

	signal, err := s.d.Signals.GetSignal(ctx, symbol, strategy)
	if err != nil {
		return CreateResult{}, fmt.Errorf("fetching signal: %w: %w", ErrSignalUnavailable, err)
	}
	_ = s.d.Publisher.Publish(ctx, ports.DomainEvent{Type: EventSignalReceived, Payload: signal})

	snap := entities.SignalSnapshot{
		ID: signal.ID, Direction: signal.Direction, Confidence: signal.Confidence,
		Price: signal.Price, GeneratedAt: signal.GeneratedAt.UTC(), Reason: signal.Reason,
	}
	intent, err := entities.NewOrderIntent(symbol, strategy, snap, in.Actor, in.IdempotencyKey, hash, s.now(), s.d.Policy.TTL)
	if err != nil {
		return CreateResult{}, fmt.Errorf("%w: %w", ErrInvalidIntent, err)
	}

	report, size := s.d.Risk.Evaluate(ctx, entities.RiskStageCreation, RiskInput{Symbol: symbol, Strategy: strategy, Signal: snap})
	intent.Side, intent.Qty, intent.EstimatedNotional, intent.Risk = size.Side, size.Qty, size.Notional, report
	if report.Passed {
		intent, err = intent.Transition(entities.IntentPendingApproval, s.now())
	} else {
		intent, err = intent.Reject(rejectionFor(report), "risk checks failed: "+strings.Join(report.Failed(), ", "), s.now())
	}
	if err != nil {
		return CreateResult{}, err
	}

	switch err := s.d.Intents.Create(ctx, intent); {
	case errors.Is(err, ports.ErrIdempotencyKeyExists):
		// A concurrent request with the same key won the insert: replay it.
		prev, ferr := s.d.Intents.FindByIdempotencyKey(ctx, in.Actor.ID, in.IdempotencyKey)
		if ferr != nil {
			return CreateResult{}, fmt.Errorf("idempotency lookup: %w", ferr)
		}
		return s.replay(prev, hash)
	case err != nil:
		return CreateResult{}, err
	}

	s.exec.record(ctx, log, "intent.created", intent.ID.String(), string(intent.Status), intentPayload(intent))
	s.publish(ctx, intent)
	log.Info("intent.created", zap.String("intent_id", intent.ID.String()), zap.String("status", string(intent.Status)))
	return CreateResult{View: IntentView{Intent: intent}}, nil
}

// ErrInvalidIntent: the proposal itself is malformed.
var ErrInvalidIntent = errors.New("invalid order intent")

func (s *IntentService) replay(prev *entities.OrderIntent, hash string) (CreateResult, error) {
	if prev.RequestHash != hash {
		return CreateResult{}, ErrIdempotencyKeyReused
	}
	return CreateResult{View: IntentView{Intent: prev}, Replayed: true}, nil
}

// Get returns one intent with its order.
func (s *IntentService) Get(ctx context.Context, id uuid.UUID) (IntentView, error) {
	it, err := s.d.Intents.Get(ctx, id)
	if err != nil {
		return IntentView{}, err
	}
	return s.view(ctx, it), nil
}

// List returns intents newest first.
func (s *IntentService) List(ctx context.Context, f ports.IntentFilter) ([]IntentView, error) {
	items, err := s.d.Intents.List(ctx, f)
	if err != nil {
		return nil, err
	}
	out := make([]IntentView, 0, len(items))
	for _, it := range items {
		out = append(out, s.view(ctx, it))
	}
	return out, nil
}

func (s *IntentService) view(ctx context.Context, it *entities.OrderIntent) IntentView {
	v := IntentView{Intent: it}
	// The order exists only after approval; its ID is the intent ID.
	if it.Status == entities.IntentPendingRisk || it.Status == entities.IntentPendingApproval || it.DecidedBy == nil {
		return v
	}
	if o, err := s.d.Orders.FindByID(ctx, it.ID); err == nil {
		v.Order = o
	} else if !errors.Is(err, ports.ErrOrderNotFound) {
		s.d.Logger.Warn("intent.order_lookup_failed", zap.String("intent_id", it.ID.String()), zap.Error(err))
	}
	return v
}

// Approve is the human decision that lets the executor submit (ADR-005 §5.3).
// Risk is evaluated again first. It returns ErrOutcomeUnknown (with the view)
// when the broker may or may not have the order: never retry on it.
func (s *IntentService) Approve(ctx context.Context, id uuid.UUID, by ports.Actor, reason string) (IntentView, error) {
	log := s.d.Logger.With(zap.String("intent_id", id.String()), zap.String("actor_id", by.ID))
	if !s.d.Policy.ExecutionEnabled {
		s.exec.record(ctx, log, "intent.approve_refused", id.String(), "execution_disabled", map[string]any{})
		return IntentView{}, ErrExecutionDisabled
	}

	s.execMu.Lock()
	defer s.execMu.Unlock()
	// From here on a client hang-up must not abort a submission halfway.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), approveTimeout)
	defer cancel()

	it, err := s.pending(ctx, id, log)
	if err != nil {
		return IntentView{}, err
	}
	// The approval record is written before anything changes (fail closed).
	payload := intentPayload(it)
	payload["reason"] = reason
	if err := s.d.Audit.Record(ctx, ports.AuditEvent{Action: "intent.approve_requested", Target: id.String(), Result: "requested", Payload: payload}); err != nil {
		log.Error("intent.audit_failed", zap.Error(err))
		return IntentView{}, fmt.Errorf("%w: %w", ErrAuditUnavailable, err)
	}
	approved, err := it.Decide(entities.IntentApproved, by, reason, s.now())
	if err != nil {
		return IntentView{}, fmt.Errorf("%w: %w", ErrIntentNotPending, err)
	}
	if err := s.d.Intents.Update(ctx, approved, entities.IntentPendingApproval); err != nil {
		if errors.Is(err, ports.ErrStaleIntent) {
			return IntentView{}, ErrIntentNotPending
		}
		return IntentView{}, err
	}
	s.exec.record(ctx, log, "intent.approved", id.String(), "approved", map[string]any{"reason": reason})
	s.publish(ctx, approved)

	return s.submit(ctx, approved, log)
}

// submit re-checks risk and sends an approved intent to the broker.
func (s *IntentService) submit(ctx context.Context, it *entities.OrderIntent, log *zap.Logger) (IntentView, error) {
	fixed := Sizing{Side: it.Side, Qty: it.Qty, Notional: it.EstimatedNotional}
	report, _ := s.d.Risk.Evaluate(ctx, entities.RiskStageSubmission, RiskInput{Symbol: it.Symbol, Strategy: it.Strategy, Signal: it.Signal, Fixed: &fixed})
	withRisk := *it
	withRisk.Risk = report
	if !report.Passed {
		return s.rejectApproved(ctx, &withRisk, rejectionFor(report), "risk re-check failed: "+strings.Join(report.Failed(), ", "), log), nil
	}

	order, err := entities.NewMarketOrder(it.Symbol, it.Side, it.Qty, it.Strategy, it.Signal.ID)
	if err != nil {
		return s.rejectApproved(ctx, &withRisk, entities.RejectionRiskFailed, "invalid order: "+err.Error(), log), nil
	}
	order.ID = it.ID // intent ID = order ID = broker client order ID (ADR-005 §5.4)
	if err := s.d.Orders.Save(ctx, order); err != nil {
		return s.rejectApproved(ctx, &withRisk, entities.RejectionRiskFailed, "order could not be stored", log), fmt.Errorf("saving order: %w", err)
	}
	if err := s.d.Audit.Record(ctx, ports.AuditEvent{Action: "order.submit_requested", Target: order.ID.String(), Result: "requested", Payload: intentPayload(&withRisk)}); err != nil {
		log.Error("intent.audit_failed", zap.Error(err))
		s.exec.markRejected(ctx, order, log)
		return s.rejectApproved(ctx, &withRisk, entities.RejectionRiskFailed, "audit log unavailable", log), fmt.Errorf("%w: %w", ErrAuditUnavailable, err)
	}

	placed, err := s.exec.submit(ctx, order, log)
	switch {
	case errors.Is(err, ErrOutcomeUnknown):
		// Stays approved with a pending order; the reconciler resolves it.
		if uerr := s.d.Intents.Update(ctx, &withRisk, entities.IntentApproved); uerr != nil {
			log.Error("intent.update_failed", zap.Error(uerr))
		}
		return IntentView{Intent: &withRisk, Order: order}, err
	case err != nil:
		return s.rejectApproved(ctx, &withRisk, entities.RejectionBrokerRejected, "broker refused the order ("+errorClass(err)+")", log), nil
	}

	submitted := order.ApplyBrokerState(placed.State())
	if uerr := s.d.Orders.Update(ctx, submitted); uerr != nil {
		log.Error("intent.order_update_failed", zap.Error(uerr))
	}
	s.exec.record(ctx, log, "order.submitted", submitted.ID.String(), string(submitted.Status), map[string]any{
		"broker_id": submitted.BrokerID, "status": string(submitted.Status), "qty": submitted.Quantity.String(),
	})
	next, terr := withRisk.Transition(entities.IntentStatusForOrder(submitted.Status), s.now())
	if terr != nil {
		log.Error("intent.transition_failed", zap.Error(terr))
		next = &withRisk
	}
	if next.Status == entities.IntentRejected {
		next.RejectionCode = entities.RejectionBrokerRejected
	}
	if uerr := s.d.Intents.Update(ctx, next, entities.IntentApproved); uerr != nil {
		log.Error("intent.update_failed", zap.Error(uerr))
	}
	_ = s.d.Publisher.Publish(ctx, ports.DomainEvent{Type: EventOrderPlaced, Payload: submitted})
	if submitted.Status == entities.OrderStatusFilled {
		_ = s.d.Publisher.Publish(ctx, ports.DomainEvent{Type: EventOrderFilled, Payload: submitted})
	}
	s.publish(ctx, next)
	return IntentView{Intent: next, Order: submitted}, nil
}

func (s *IntentService) rejectApproved(ctx context.Context, it *entities.OrderIntent, code entities.RejectionCode, reason string, log *zap.Logger) IntentView {
	rejected, err := it.Reject(code, reason, s.now())
	if err != nil {
		log.Error("intent.transition_failed", zap.Error(err))
		return IntentView{Intent: it}
	}
	rejected.DecisionReason = reason // the approver's note is in the audit log
	if err := s.d.Intents.Update(ctx, rejected, entities.IntentApproved); err != nil {
		log.Error("intent.update_failed", zap.Error(err))
	}
	payload := intentPayload(rejected)
	payload["reason"] = reason
	s.exec.record(ctx, log, "intent.rejected", it.ID.String(), string(code), payload)
	s.publish(ctx, rejected)
	return s.view(ctx, rejected)
}

// Reject is a human decision not to trade.
func (s *IntentService) Reject(ctx context.Context, id uuid.UUID, by ports.Actor, reason string) (IntentView, error) {
	log := s.d.Logger.With(zap.String("intent_id", id.String()), zap.String("actor_id", by.ID))
	return s.decide(ctx, id, log, func(it *entities.OrderIntent) (*entities.OrderIntent, error) {
		next, err := it.Decide(entities.IntentRejected, by, reason, s.now())
		if err == nil {
			next.RejectionCode = entities.RejectionByApprover
		}
		return next, err
	}, "intent.rejected")
}

// Cancel withdraws a pending intent; only its creator may do so.
func (s *IntentService) Cancel(ctx context.Context, id uuid.UUID, by ports.Actor) (IntentView, error) {
	log := s.d.Logger.With(zap.String("intent_id", id.String()), zap.String("actor_id", by.ID))
	return s.decide(ctx, id, log, func(it *entities.OrderIntent) (*entities.OrderIntent, error) {
		if it.CreatedBy.ID != by.ID {
			return nil, ErrNotIntentOwner
		}
		return it.Decide(entities.IntentCanceled, by, "canceled by creator", s.now())
	}, "intent.canceled")
}

func (s *IntentService) decide(ctx context.Context, id uuid.UUID, log *zap.Logger, next func(*entities.OrderIntent) (*entities.OrderIntent, error), action string) (IntentView, error) {
	it, err := s.pending(ctx, id, log)
	if err != nil {
		return IntentView{}, err
	}
	updated, err := next(it)
	if err != nil {
		return IntentView{}, err
	}
	if err := s.d.Intents.Update(ctx, updated, entities.IntentPendingApproval); err != nil {
		if errors.Is(err, ports.ErrStaleIntent) {
			return IntentView{}, ErrIntentNotPending
		}
		return IntentView{}, err
	}
	payload := intentPayload(updated)
	payload["reason"] = updated.DecisionReason
	s.exec.record(ctx, log, action, id.String(), string(updated.Status), payload)
	s.publish(ctx, updated)
	return IntentView{Intent: updated}, nil
}

// pending loads an intent that must still be waiting for a decision. A past
// deadline expires it on the spot.
func (s *IntentService) pending(ctx context.Context, id uuid.UUID, log *zap.Logger) (*entities.OrderIntent, error) {
	it, err := s.d.Intents.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if it.Expired(s.now()) {
		s.expire(ctx, it, log)
		return nil, ErrIntentExpired
	}
	if it.Status != entities.IntentPendingApproval {
		return nil, ErrIntentNotPending
	}
	return it, nil
}

// ExpireDue expires every pending intent past its deadline; it returns how many.
func (s *IntentService) ExpireDue(ctx context.Context) int {
	due, err := s.d.Intents.ExpiredPending(ctx, s.now())
	if err != nil {
		s.d.Logger.Error("intent.expiry_scan_failed", zap.Error(err))
		return 0
	}
	n := 0
	for _, it := range due {
		if s.expire(ctx, it, s.d.Logger.With(zap.String("intent_id", it.ID.String()))) {
			n++
		}
	}
	return n
}

// RunExpiry expires due intents every interval until ctx ends.
func (s *IntentService) RunExpiry(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.ExpireDue(ctx)
		}
	}
}

func (s *IntentService) expire(ctx context.Context, it *entities.OrderIntent, log *zap.Logger) bool {
	expired, err := it.Transition(entities.IntentExpired, s.now())
	if err != nil {
		return false
	}
	if err := s.d.Intents.Update(ctx, expired, entities.IntentPendingApproval); err != nil {
		if !errors.Is(err, ports.ErrStaleIntent) {
			log.Error("intent.expire_failed", zap.Error(err))
		}
		return false
	}
	s.exec.record(ctx, log, "intent.expired", it.ID.String(), "expired", map[string]any{"expires_at": it.ExpiresAt})
	s.publish(ctx, expired)
	return true
}

func (s *IntentService) publish(ctx context.Context, it *entities.OrderIntent) {
	_ = s.d.Publisher.Publish(ctx, ports.DomainEvent{Type: EventIntentUpdated, Payload: s.view(context.WithoutCancel(ctx), it)})
}

// rejectionFor names the reason a failed report rejects an intent.
func rejectionFor(r entities.RiskReport) entities.RejectionCode {
	for _, c := range r.Checks {
		if c.Name == CheckKillSwitch && !c.Passed {
			return entities.RejectionKillSwitch
		}
	}
	return entities.RejectionRiskFailed
}

// intentPayload is the audit record of an intent decision, including the risk
// inputs used (ADR-005 §5.8).
func intentPayload(it *entities.OrderIntent) map[string]any {
	checks := make([]map[string]any, 0, len(it.Risk.Checks))
	for _, c := range it.Risk.Checks {
		checks = append(checks, map[string]any{"name": c.Name, "passed": c.Passed, "detail": c.Detail})
	}
	return map[string]any{
		"intent_id": it.ID.String(), "status": string(it.Status), "symbol": it.Symbol, "strategy": it.Strategy,
		"side": string(it.Side), "qty": it.Qty.String(), "estimated_notional": it.EstimatedNotional.String(),
		"signal_id": it.Signal.ID, "signal_direction": it.Signal.Direction, "signal_confidence": it.Signal.Confidence,
		"signal_price": it.Signal.Price.String(), "created_by": it.CreatedBy.ID,
		"risk_passed": it.Risk.Passed, "risk_stage": string(it.Risk.Stage), "risk_checks": checks,
		"rejection_code": string(it.RejectionCode),
	}
}

// actorLimiter is a per-actor sliding-window counter (process-local; the
// engine runs as a single replica, ADR-005 §5.6).
type actorLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	hits   map[string][]time.Time
}

func newActorLimiter(limit int, window time.Duration) *actorLimiter {
	return &actorLimiter{limit: limit, window: window, hits: map[string][]time.Time{}}
}

// Allow records a hit for actor if it is under the limit.
func (l *actorLimiter) Allow(actor string, now time.Time) bool {
	if l.limit <= 0 {
		return false // fail closed on a missing limit
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	kept := l.hits[actor][:0]
	for _, t := range l.hits[actor] {
		if now.Sub(t) < l.window {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.limit {
		l.hits[actor] = kept
		return false
	}
	l.hits[actor] = append(kept, now)
	return true
}
