package usecases_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/trading-bot/bot-engine/internal/application/usecases"
	"github.com/trading-bot/bot-engine/internal/domain/entities"
	"github.com/trading-bot/bot-engine/internal/domain/ports"
	"github.com/trading-bot/bot-engine/internal/infrastructure/broker/fakebroker"
	"github.com/trading-bot/bot-engine/internal/infrastructure/repository"
)

// freshSignals returns a fresh BUY signal with a new ID on every call.
type freshSignals struct {
	n         atomic.Int64
	direction string
	err       error
}

func (f *freshSignals) GetSignal(_ context.Context, symbol, _ string) (*ports.Signal, error) {
	if f.err != nil {
		return nil, f.err
	}
	dir := f.direction
	if dir == "" {
		dir = "BUY"
	}
	return &ports.Signal{
		ID: fmt.Sprintf("sig-%d", f.n.Add(1)), Symbol: symbol, Direction: dir, Price: decimal.NewFromInt(150),
		Confidence: 0.85, IsActionable: dir != "HOLD", GeneratedAt: time.Now(),
	}, nil
}

// sameSignal always returns the same signal ID.
type sameSignal struct{ freshSignals }

func (s *sameSignal) GetSignal(ctx context.Context, symbol, strategy string) (*ports.Signal, error) {
	sig, err := s.freshSignals.GetSignal(ctx, symbol, strategy)
	if sig != nil {
		sig.ID = "sig-fixed"
	}
	return sig, err
}

type toggle struct {
	mu      sync.Mutex
	engaged bool
}

func (t *toggle) Engaged(context.Context) (bool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.engaged, nil
}

func (t *toggle) set(v bool) { t.mu.Lock(); t.engaged = v; t.mu.Unlock() }

type intentEnv struct {
	svc     *usecases.IntentService
	broker  *fakebroker.Broker
	intents *repository.InMemoryIntentRepo
	orders  *repository.InMemoryOrderRepo
	audit   *recordingAudit
	pub     *mockPublisher
	ks      *toggle
	signals ports.SignalServicePort
}

var (
	agent = ports.Actor{ID: "mcp-gateway", Type: "agent"}
	human = ports.Actor{ID: "owner", Type: "human"}
)

func newIntentEnv(t *testing.T, mutate ...func(*usecases.IntentDeps)) *intentEnv {
	t.Helper()
	e := &intentEnv{
		broker: fakebroker.New(decimal.NewFromInt(100_000)), intents: repository.NewInMemoryIntentRepo(),
		orders: repository.NewInMemoryOrderRepo(), audit: &recordingAudit{}, pub: &mockPublisher{}, ks: &toggle{},
		signals: &freshSignals{},
	}
	e.broker.SetFillPrice("AAPL", decimal.NewFromInt(150))
	d := usecases.IntentDeps{
		Intents: e.intents, Orders: e.orders, Broker: e.broker, KillSwitch: e.ks, Audit: e.audit, Publisher: e.pub,
		Logger: zap.NewNop(),
		Policy: usecases.IntentPolicy{TTL: 5 * time.Minute, RateLimitPerHour: 100, ExecutionEnabled: true},
	}
	for _, m := range mutate {
		m(&d)
	}
	if d.Signals == nil {
		d.Signals = e.signals
	} else {
		e.signals = d.Signals
	}
	d.Risk = usecases.NewRiskEngine(testPolicy(), e.broker, e.ks)
	e.svc = usecases.NewIntentService(d)
	return e
}

func (e *intentEnv) create(t *testing.T, key string) *entities.OrderIntent {
	t.Helper()
	res, err := e.svc.Create(context.Background(), usecases.CreateIntentInput{Actor: agent, Symbol: "aapl", Strategy: "rsi", IdempotencyKey: key})
	require.NoError(t, err)
	return res.View.Intent
}

func TestCreateIntentPassesRiskAndWaitsForApproval(t *testing.T) {
	e := newIntentEnv(t)
	it := e.create(t, "key-00000001")
	assert.Equal(t, entities.IntentPendingApproval, it.Status)
	assert.Equal(t, "AAPL", it.Symbol, "symbol is normalized")
	assert.Equal(t, entities.OrderSideBuy, it.Side)
	assert.Equal(t, "6.666666", it.Qty.String(), "the absolute cap (1000) wins over 5% of equity")
	assert.True(t, it.Risk.Passed)
	assert.Equal(t, agent, it.CreatedBy)
	assert.WithinDuration(t, time.Now().Add(5*time.Minute), it.ExpiresAt, 5*time.Second)
	assert.Contains(t, e.audit.actions(), "intent.created")
	assert.Len(t, e.pub.ofType(usecases.EventIntentUpdated), 1)
	assert.Empty(t, e.broker.Submitted, "creating an intent never submits")
}

func TestCreateIntentThatFailsRiskIsStoredRejected(t *testing.T) {
	e := newIntentEnv(t, func(d *usecases.IntentDeps) { d.Signals = &freshSignals{direction: "HOLD"} })
	it := e.create(t, "key-00000001")
	assert.Equal(t, entities.IntentRejected, it.Status)
	assert.Equal(t, entities.RejectionRiskFailed, it.RejectionCode)
	assert.Contains(t, it.Risk.Failed(), usecases.CheckSignalActionable)
	stored, err := e.intents.Get(context.Background(), it.ID)
	require.NoError(t, err)
	assert.Equal(t, entities.IntentRejected, stored.Status)
}

func TestCreateIntentIsIdempotent(t *testing.T) {
	e := newIntentEnv(t)
	first := e.create(t, "key-00000001")

	res, err := e.svc.Create(context.Background(), usecases.CreateIntentInput{Actor: agent, Symbol: "AAPL", Strategy: "rsi", IdempotencyKey: "key-00000001"})
	require.NoError(t, err)
	assert.True(t, res.Replayed)
	assert.Equal(t, first.ID, res.View.Intent.ID)

	_, err = e.svc.Create(context.Background(), usecases.CreateIntentInput{Actor: agent, Symbol: "SPY", Strategy: "rsi", IdempotencyKey: "key-00000001"})
	assert.ErrorIs(t, err, usecases.ErrIdempotencyKeyReused)

	// The key is per actor: another actor may use it.
	res, err = e.svc.Create(context.Background(), usecases.CreateIntentInput{Actor: human, Symbol: "AAPL", Strategy: "rsi", IdempotencyKey: "key-00000001"})
	require.NoError(t, err)
	assert.NotEqual(t, first.ID, res.View.Intent.ID)
}

func TestReplayReturnsTheOriginalNotTheCurrentState(t *testing.T) {
	e := newIntentEnv(t)
	first := e.create(t, "key-00000001")
	_, err := e.svc.Reject(context.Background(), first.ID, human, "no")
	require.NoError(t, err)
	res, err := e.svc.Create(context.Background(), usecases.CreateIntentInput{Actor: agent, Symbol: "AAPL", Strategy: "rsi", IdempotencyKey: "key-00000001"})
	require.NoError(t, err)
	assert.Equal(t, entities.IntentPendingApproval, res.View.Intent.Status, "replay is the original response")
}

func TestOneActiveIntentPerSignal(t *testing.T) {
	e := newIntentEnv(t, func(d *usecases.IntentDeps) { d.Signals = &sameSignal{} })
	e.create(t, "key-00000001")
	_, err := e.svc.Create(context.Background(), usecases.CreateIntentInput{Actor: agent, Symbol: "AAPL", Strategy: "rsi", IdempotencyKey: "key-00000002"})
	assert.ErrorIs(t, err, ports.ErrActiveIntentForSignal)
}

func TestCreateRefusedByKillSwitchRateLimitAndSignalOutage(t *testing.T) {
	e := newIntentEnv(t, func(d *usecases.IntentDeps) { d.Policy.RateLimitPerHour = 1 })
	e.ks.set(true)
	_, err := e.svc.Create(context.Background(), usecases.CreateIntentInput{Actor: agent, Symbol: "AAPL", Strategy: "rsi", IdempotencyKey: "key-00000001"})
	assert.ErrorIs(t, err, ports.ErrKillSwitchEngaged)
	e.ks.set(false)

	e.create(t, "key-00000002")
	_, err = e.svc.Create(context.Background(), usecases.CreateIntentInput{Actor: agent, Symbol: "AAPL", Strategy: "rsi", IdempotencyKey: "key-00000003"})
	assert.ErrorIs(t, err, usecases.ErrIntentRateLimited)

	down := newIntentEnv(t, func(d *usecases.IntentDeps) { d.Signals = &freshSignals{err: errors.New("boom")} })
	_, err = down.svc.Create(context.Background(), usecases.CreateIntentInput{Actor: agent, Symbol: "AAPL", Strategy: "rsi", IdempotencyKey: "key-00000001"})
	assert.ErrorIs(t, err, usecases.ErrSignalUnavailable)
	list, _ := down.intents.List(context.Background(), ports.IntentFilter{})
	assert.Empty(t, list)
}

func TestApproveSubmitsWithIntentIDAsClientOrderID(t *testing.T) {
	e := newIntentEnv(t)
	it := e.create(t, "key-00000001")

	v, err := e.svc.Approve(context.Background(), it.ID, human, "looks good")
	require.NoError(t, err)
	assert.Equal(t, entities.IntentFilled, v.Intent.Status)
	assert.Equal(t, entities.RiskStageSubmission, v.Intent.Risk.Stage, "risk is evaluated again before submission")
	require.NotNil(t, v.Intent.DecidedBy)
	assert.Equal(t, human, *v.Intent.DecidedBy)
	require.Len(t, e.broker.Submitted, 1)
	assert.Equal(t, it.ID.String(), e.broker.Submitted[0].ClientOrderID)
	assert.Equal(t, it.Qty.String(), e.broker.Submitted[0].Qty.String(), "the approved size is what is sent")
	require.NotNil(t, v.Order)
	assert.Equal(t, it.ID, v.Order.ID)
	assert.Len(t, e.pub.ofType(usecases.EventOrderFilled), 1)
	assert.Subset(t, e.audit.actions(), []string{"intent.approve_requested", "intent.approved", "order.submit_requested", "order.submitted"})
}

func TestApproveNeedsExecutionEnabled(t *testing.T) {
	e := newIntentEnv(t, func(d *usecases.IntentDeps) { d.Policy.ExecutionEnabled = false })
	it := e.create(t, "key-00000001")
	_, err := e.svc.Approve(context.Background(), it.ID, human, "")
	assert.ErrorIs(t, err, usecases.ErrExecutionDisabled)
	got, _ := e.svc.Get(context.Background(), it.ID)
	assert.Equal(t, entities.IntentPendingApproval, got.Intent.Status)
	assert.Empty(t, e.broker.Submitted)
}

func TestConcurrentApprovalsSubmitOnce(t *testing.T) {
	e := newIntentEnv(t)
	it := e.create(t, "key-00000001")
	var wg sync.WaitGroup
	var ok, notPending atomic.Int32
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := e.svc.Approve(context.Background(), it.ID, human, "")
			switch {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, usecases.ErrIntentNotPending):
				notPending.Add(1)
			}
		}()
	}
	wg.Wait()
	assert.Equal(t, int32(1), ok.Load())
	assert.Equal(t, int32(9), notPending.Load())
	assert.Len(t, e.broker.Submitted, 1, "a double approval never submits twice")
}

func TestApproveRechecksRisk(t *testing.T) {
	e := newIntentEnv(t)
	it := e.create(t, "key-00000001")
	e.ks.set(true) // engaged between proposal and approval
	v, err := e.svc.Approve(context.Background(), it.ID, human, "")
	require.NoError(t, err)
	assert.Equal(t, entities.IntentRejected, v.Intent.Status)
	assert.Equal(t, entities.RejectionKillSwitch, v.Intent.RejectionCode)
	assert.Empty(t, e.broker.Submitted)

	e2 := newIntentEnv(t)
	it2 := e2.create(t, "key-00000001")
	e2.broker.SetMarketOpen(false)
	v, err = e2.svc.Approve(context.Background(), it2.ID, human, "")
	require.NoError(t, err)
	assert.Equal(t, entities.RejectionRiskFailed, v.Intent.RejectionCode)
	assert.Contains(t, v.Intent.Risk.Failed(), usecases.CheckMarketOpen)
}

func TestApproveFailsClosedWithoutAudit(t *testing.T) {
	e := newIntentEnv(t)
	it := e.create(t, "key-00000001")
	e.audit.fail = errors.New("db down")
	_, err := e.svc.Approve(context.Background(), it.ID, human, "")
	assert.ErrorIs(t, err, usecases.ErrAuditUnavailable)
	got, _ := e.svc.Get(context.Background(), it.ID)
	assert.Equal(t, entities.IntentPendingApproval, got.Intent.Status, "nothing changes without the approval record")
	assert.Empty(t, e.broker.Submitted)
}

func TestApproveBrokerRejection(t *testing.T) {
	e := newIntentEnv(t)
	it := e.create(t, "key-00000001")
	e.broker.FailSubmit = fmt.Errorf("fake: %w", ports.ErrRejected)
	v, err := e.svc.Approve(context.Background(), it.ID, human, "")
	require.NoError(t, err)
	assert.Equal(t, entities.IntentRejected, v.Intent.Status)
	assert.Equal(t, entities.RejectionBrokerRejected, v.Intent.RejectionCode)
}

func TestApproveUnknownOutcomeThenReconciled(t *testing.T) {
	e := newIntentEnv(t)
	it := e.create(t, "key-00000001")
	e.broker.SubmitThenFail = fmt.Errorf("fake: %w", ports.ErrUnavailable)
	e.broker.FailGet = fmt.Errorf("fake: %w", ports.ErrUnavailable)

	v, err := e.svc.Approve(context.Background(), it.ID, human, "")
	require.ErrorIs(t, err, usecases.ErrOutcomeUnknown)
	assert.Equal(t, entities.IntentApproved, v.Intent.Status, "unknown outcome: stays approved, never resubmitted")
	assert.Len(t, e.broker.Submitted, 1)

	e.broker.SubmitThenFail, e.broker.FailGet = nil, nil
	rec := usecases.NewReconciler(usecases.ReconcilerDeps{Broker: e.broker, Orders: e.orders, Intents: e.intents, Audit: e.audit, Publisher: e.pub, Logger: zap.NewNop()})
	assert.Equal(t, 1, rec.Poll(context.Background()))
	got, _ := e.svc.Get(context.Background(), it.ID)
	assert.Equal(t, entities.IntentFilled, got.Intent.Status)
	assert.Len(t, e.broker.Submitted, 1, "reconciliation never resubmits")
}

func TestRejectAndCancel(t *testing.T) {
	e := newIntentEnv(t)
	a := e.create(t, "key-00000001")
	v, err := e.svc.Reject(context.Background(), a.ID, human, "too risky today")
	require.NoError(t, err)
	assert.Equal(t, entities.RejectionByApprover, v.Intent.RejectionCode)
	assert.Equal(t, "too risky today", v.Intent.DecisionReason)
	_, err = e.svc.Approve(context.Background(), a.ID, human, "")
	assert.ErrorIs(t, err, usecases.ErrIntentNotPending)

	b := e.create(t, "key-00000002")
	_, err = e.svc.Cancel(context.Background(), b.ID, human)
	assert.ErrorIs(t, err, usecases.ErrNotIntentOwner)
	v, err = e.svc.Cancel(context.Background(), b.ID, agent)
	require.NoError(t, err)
	assert.Equal(t, entities.IntentCanceled, v.Intent.Status)

	_, err = e.svc.Get(context.Background(), uuid.New())
	assert.ErrorIs(t, err, ports.ErrIntentNotFound)
}

func TestIntentsExpire(t *testing.T) {
	e := newIntentEnv(t, func(d *usecases.IntentDeps) { d.Policy.TTL = 20 * time.Millisecond })
	a := e.create(t, "key-00000001")
	b := e.create(t, "key-00000002")
	time.Sleep(40 * time.Millisecond)

	_, err := e.svc.Approve(context.Background(), a.ID, human, "")
	assert.ErrorIs(t, err, usecases.ErrIntentExpired)
	assert.Equal(t, 1, e.svc.ExpireDue(context.Background()), "b is expired by the sweeper; a already was")
	for _, id := range []uuid.UUID{a.ID, b.ID} {
		got, _ := e.intents.Get(context.Background(), id)
		assert.Equal(t, entities.IntentExpired, got.Status)
	}
	assert.Empty(t, e.broker.Submitted)
}
