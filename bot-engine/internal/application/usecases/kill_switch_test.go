package usecases_test

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/trading-bot/bot-engine/internal/application/usecases"
	"github.com/trading-bot/bot-engine/internal/domain/entities"
	"github.com/trading-bot/bot-engine/internal/domain/ports"
	"github.com/trading-bot/bot-engine/internal/infrastructure/auditlog"
	"github.com/trading-bot/bot-engine/internal/infrastructure/broker/fakebroker"
)

// flakyStore is a kill-switch store that can fail.
type flakyStore struct {
	auditlog.MemoryKillSwitch
	loadErr, saveErr error
}

func (f *flakyStore) Load(ctx context.Context) (ports.KillSwitchState, error) {
	if f.loadErr != nil {
		return ports.KillSwitchState{}, f.loadErr
	}
	return f.MemoryKillSwitch.Load(ctx)
}

func (f *flakyStore) Save(ctx context.Context, s ports.KillSwitchState) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	return f.MemoryKillSwitch.Save(ctx, s)
}

type ksEnv struct {
	svc    *usecases.KillSwitchService
	store  *flakyStore
	broker *fakebroker.Broker
	audit  *recordingAudit
	pub    *mockPublisher
}

func newKS(t *testing.T, forced bool) *ksEnv {
	t.Helper()
	e := &ksEnv{store: &flakyStore{}, broker: fakebroker.New(decimal.NewFromInt(100_000)), audit: &recordingAudit{}, pub: &mockPublisher{}}
	e.svc = usecases.NewKillSwitchService(e.store, e.broker, e.audit, e.pub, forced, zap.NewNop())
	return e
}

var owner = ports.WithActor(context.Background(), ports.Actor{ID: "owner", Type: "human"})

// workingOrder leaves one open order at the fake broker.
func (e *ksEnv) workingOrder(t *testing.T) {
	t.Helper()
	_, err := e.broker.SubmitOrder(context.Background(), ports.SubmitOrderRequest{
		ClientOrderID: "c-open", Symbol: "MSFT", Side: entities.OrderSideBuy, Type: entities.OrderTypeMarket, Qty: decimal.NewFromInt(1),
	})
	require.NoError(t, err)
}

func TestKillSwitch_EngageHaltsCancelsAuditsAndPublishes(t *testing.T) {
	e := newKS(t, false)
	require.NoError(t, e.svc.Init(context.Background()))
	e.workingOrder(t)

	res, err := e.svc.Engage(owner, "runaway strategy")
	require.NoError(t, err)
	assert.True(t, res.Persisted)
	assert.NoError(t, res.CancelErr)

	engaged, err := e.svc.Engaged(context.Background())
	assert.True(t, engaged)
	assert.NoError(t, err)
	stored, _ := e.store.Load(context.Background())
	assert.True(t, stored.Engaged)
	assert.Equal(t, "owner", stored.UpdatedBy)
	assert.Equal(t, entities.OrderStatusCanceled, e.broker.Orders()[0].Status)
	require.Equal(t, []string{"kill_switch.engaged"}, e.audit.actions())
	assert.Equal(t, "runaway strategy", e.audit.events[0].Payload["reason"])
	assert.Len(t, e.pub.ofType(usecases.EventKillSwitchChanged), 1)
}

func TestKillSwitch_EngageHoldsWhenStoreAndBrokerFail(t *testing.T) {
	e := newKS(t, false)
	require.NoError(t, e.svc.Init(context.Background()))
	e.store.saveErr = errors.New("db down")
	e.broker.FailCancel = errors.New("broker down")

	res, err := e.svc.Engage(owner, "incident")
	require.NoError(t, err, "engaging never fails")
	assert.False(t, res.Persisted)
	assert.Error(t, res.CancelErr)
	engaged, _ := e.svc.Engaged(context.Background())
	assert.True(t, engaged, "in-memory latch holds")
	assert.Equal(t, "failed", e.audit.events[0].Payload["cancel_all"])
}

func TestKillSwitch_ReleaseNeedsSuccessfulWrite(t *testing.T) {
	e := newKS(t, false)
	require.NoError(t, e.svc.Init(context.Background()))
	_, _ = e.svc.Engage(owner, "incident")

	e.store.saveErr = errors.New("db down")
	_, err := e.svc.Release(owner, "resolved")
	require.Error(t, err)
	engaged, _ := e.svc.Engaged(context.Background())
	assert.True(t, engaged, "a failed release keeps trading halted")

	e.store.saveErr = nil
	st, err := e.svc.Release(owner, "resolved")
	require.NoError(t, err)
	assert.False(t, st.Engaged)
	engaged, _ = e.svc.Engaged(context.Background())
	assert.False(t, engaged)
	assert.Equal(t, []string{"kill_switch.engaged", "kill_switch.released"}, e.audit.actions())
}

func TestKillSwitch_ForcedByEnvEngagesAtStartupAndCannotBeReleased(t *testing.T) {
	e := newKS(t, true)
	e.workingOrder(t)
	require.NoError(t, e.svc.Init(context.Background()))

	stored, _ := e.store.Load(context.Background())
	assert.True(t, stored.Engaged)
	assert.Equal(t, "system", stored.UpdatedBy)
	assert.Equal(t, entities.OrderStatusCanceled, e.broker.Orders()[0].Status, "startup override cancels open orders")

	_, err := e.svc.Release(owner, "try")
	assert.ErrorIs(t, err, usecases.ErrKillSwitchForced)
	status, _ := e.svc.Status(context.Background())
	assert.True(t, status.Engaged)
	assert.True(t, status.Forced)
}

func TestKillSwitch_PersistedStateSurvivesRestart(t *testing.T) {
	e := newKS(t, false)
	_ = e.store.Save(context.Background(), ports.KillSwitchState{Engaged: true, Reason: "before restart", UpdatedBy: "owner"})
	restarted := usecases.NewKillSwitchService(e.store, e.broker, e.audit, e.pub, false, zap.NewNop())
	require.NoError(t, restarted.Init(context.Background()))
	engaged, _ := restarted.Engaged(context.Background())
	assert.True(t, engaged)
}

func TestKillSwitch_FailsClosedWhenStateUnreadable(t *testing.T) {
	e := newKS(t, false)
	e.store.loadErr = errors.New("db down")
	assert.Error(t, e.svc.Init(context.Background()))
	engaged, _ := e.svc.Engaged(context.Background())
	assert.True(t, engaged, "unknown state reads as engaged")
}

func TestKillSwitch_BlocksIntentsEndToEnd(t *testing.T) {
	var ks *usecases.KillSwitchService
	e := newIntentEnv(t, func(d *usecases.IntentDeps) {
		ks = usecases.NewKillSwitchService(&flakyStore{}, d.Broker, d.Audit, d.Publisher, false, zap.NewNop())
		d.KillSwitch = ks
	})
	require.NoError(t, ks.Init(context.Background()))

	before := e.create(t, "key-00000001")
	_, err := e.svc.Approve(context.Background(), before.ID, human, "")
	require.NoError(t, err)
	pending := e.create(t, "key-00000002")

	_, err = ks.Engage(owner, "halt")
	require.NoError(t, err)
	_, err = e.svc.Create(context.Background(), usecases.CreateIntentInput{Actor: agent, Symbol: "AAPL", Strategy: "rsi", IdempotencyKey: "key-00000003"})
	assert.ErrorIs(t, err, ports.ErrKillSwitchEngaged, "no new intents while halted")
	v, err := e.svc.Approve(context.Background(), pending.ID, human, "")
	require.NoError(t, err)
	assert.Equal(t, entities.RejectionKillSwitch, v.Intent.RejectionCode, "a pending intent cannot be approved while halted")
	assert.Len(t, e.broker.Submitted, 1, "only the order before the halt")
}
