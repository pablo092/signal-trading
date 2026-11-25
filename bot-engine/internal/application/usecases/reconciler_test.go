package usecases_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/trading-bot/bot-engine/internal/application/usecases"
	"github.com/trading-bot/bot-engine/internal/domain/entities"
	"github.com/trading-bot/bot-engine/internal/domain/ports"
)

// workingIntent creates and approves an intent whose order stays working.
func workingIntent(t *testing.T, e *intentEnv) (*entities.OrderIntent, ports.BrokerOrder) {
	t.Helper()
	e.broker.ClearFillPrice("AAPL")
	it := e.create(t, fmt.Sprintf("key-%08d", time.Now().UnixNano()%1e8))
	v, err := e.svc.Approve(context.Background(), it.ID, human, "")
	require.NoError(t, err)
	require.Equal(t, entities.IntentSubmitted, v.Intent.Status)
	bo, err := e.broker.GetOrderByClientID(context.Background(), it.ID.String())
	require.NoError(t, err)
	return v.Intent, bo
}

func newReconciler(e *intentEnv, grace time.Duration) *usecases.Reconciler {
	return usecases.NewReconciler(usecases.ReconcilerDeps{
		Broker: e.broker, Orders: e.orders, Intents: e.intents, Audit: e.audit, Publisher: e.pub,
		Logger: zap.NewNop(), PollEvery: 20 * time.Millisecond, UnknownGrace: grace,
	})
}

func TestReconcilerFollowsTheTradeStream(t *testing.T) {
	e := newIntentEnv(t)
	it, bo := workingIntent(t, e)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go newReconciler(e, time.Minute).Run(ctx)
	require.Eventually(t, func() bool { return e.broker.Streaming() == 1 }, 2*time.Second, 5*time.Millisecond)

	require.NoError(t, e.broker.Fill(bo.ID, decimalFrom("151.25")))
	require.Eventually(t, func() bool {
		got, _ := e.svc.Get(context.Background(), it.ID)
		return got.Intent.Status == entities.IntentFilled
	}, 2*time.Second, 5*time.Millisecond)
	got, _ := e.svc.Get(context.Background(), it.ID)
	require.NotNil(t, got.Order)
	assert.Equal(t, "151.25", got.Order.FilledPrice.String())
	assert.NotEmpty(t, e.pub.ofType(usecases.EventOrderFilled))
	assert.Contains(t, e.audit.actions(), "order.reconciled")
}

func TestReconcilerPollRecoversMissedEvents(t *testing.T) {
	e := newIntentEnv(t)
	it, bo := workingIntent(t, e)
	require.NoError(t, e.broker.SetOrderSilently(bo.ID, entities.OrderStatusCanceled))

	rec := newReconciler(e, time.Minute)
	assert.Equal(t, 1, rec.Poll(context.Background()))
	got, _ := e.svc.Get(context.Background(), it.ID)
	assert.Equal(t, entities.IntentCanceled, got.Intent.Status)
	assert.Equal(t, 0, rec.Poll(context.Background()), "applying the same state again is a no-op")
}

func TestReconcilerClosesOrdersThatNeverReachedTheBroker(t *testing.T) {
	e := newIntentEnv(t)
	it := e.create(t, "key-00000001")
	// Submission outcome unknown, and the broker never got it.
	e.broker.FailSubmit = fmt.Errorf("fake: %w", ports.ErrUnavailable)
	e.broker.FailGet = fmt.Errorf("fake: %w", ports.ErrUnavailable)
	_, err := e.svc.Approve(context.Background(), it.ID, human, "")
	require.ErrorIs(t, err, usecases.ErrOutcomeUnknown)
	e.broker.FailSubmit, e.broker.FailGet = nil, nil

	assert.Equal(t, 0, newReconciler(e, time.Hour).Poll(context.Background()), "inside the grace period it waits")
	time.Sleep(30 * time.Millisecond) // Windows clocks tick coarsely
	assert.Equal(t, 1, newReconciler(e, time.Millisecond).Poll(context.Background()))
	got, _ := e.svc.Get(context.Background(), it.ID)
	assert.Equal(t, entities.IntentRejected, got.Intent.Status)
	assert.Equal(t, entities.RejectionBrokerRejected, got.Intent.RejectionCode)
	assert.Empty(t, e.broker.Submitted, "the reconciler never submits")
}

func TestReconcilerIgnoresForeignOrders(t *testing.T) {
	e := newIntentEnv(t)
	assert.False(t, newReconciler(e, time.Minute).Apply(context.Background(), ports.BrokerOrder{ClientOrderID: "placed-in-the-dashboard"}))
}

func decimalFrom(s string) decimal.Decimal { return decimal.RequireFromString(s) }
