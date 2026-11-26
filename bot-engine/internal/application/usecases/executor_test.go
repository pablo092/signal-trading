package usecases_test

// Submission-path tests (orderExecutor) driven through approved order intents,
// the only way an order reaches the broker since the legacy route was removed
// in M4. Ported from the former PlaceOrderUseCase tests.

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/trading-bot/bot-engine/internal/application/usecases"
	"github.com/trading-bot/bot-engine/internal/domain/entities"
	"github.com/trading-bot/bot-engine/internal/domain/ports"
)

// failingSave is an order repository whose Save fails.
type failingSave struct {
	ports.OrderRepositoryPort
	err error
}

func (f failingSave) Save(context.Context, *entities.Order) error { return f.err }

func (e *intentEnv) storedOrder(t *testing.T, it *entities.OrderIntent) *entities.Order {
	t.Helper()
	o, err := e.orders.FindByID(context.Background(), it.ID)
	require.NoError(t, err)
	return o
}

func TestApproveWorkingOrderIsNotReportedAsFilled(t *testing.T) {
	e := newIntentEnv(t)
	e.broker.ClearFillPrice("AAPL") // no fill price: the order stays working
	it := e.create(t, "key-00000001")

	v, err := e.svc.Approve(context.Background(), it.ID, human, "")
	require.NoError(t, err)
	assert.Equal(t, entities.IntentSubmitted, v.Intent.Status)
	require.NotNil(t, v.Order)
	assert.Equal(t, entities.OrderStatusSubmitted, v.Order.Status)
	assert.Nil(t, v.Order.FilledPrice)
	assert.Nil(t, v.Order.FilledAt)
	assert.True(t, v.Order.FilledQty.IsZero())
	assert.Equal(t, entities.OrderTypeMarket, e.broker.Submitted[0].Type)
}

func TestApproveUnknownOutcomeButBrokerHasOrderIsAdopted(t *testing.T) {
	e := newIntentEnv(t)
	it := e.create(t, "key-00000001")
	e.broker.SubmitThenFail = fmt.Errorf("alpaca place order: %w: %w", ports.ErrUnavailable, context.DeadlineExceeded)

	v, err := e.svc.Approve(context.Background(), it.ID, human, "")
	require.NoError(t, err, "the broker has the order, so the submission succeeded")
	assert.Equal(t, entities.IntentFilled, v.Intent.Status)
	assert.Equal(t, "fake-1", v.Order.BrokerID)
	assert.Equal(t, entities.OrderStatusFilled, e.storedOrder(t, it).Status)
	assert.Len(t, e.broker.Submitted, 1, "never resubmitted")
}

func TestApproveUnknownOutcomeNotAtBrokerIsRejected(t *testing.T) {
	e := newIntentEnv(t)
	it := e.create(t, "key-00000001")
	e.broker.FailSubmit = fmt.Errorf("alpaca place order: %w", ports.ErrUnavailable) // never recorded

	v, err := e.svc.Approve(context.Background(), it.ID, human, "")
	require.NoError(t, err)
	assert.Equal(t, entities.IntentRejected, v.Intent.Status)
	assert.Equal(t, entities.RejectionBrokerRejected, v.Intent.RejectionCode)
	assert.Equal(t, entities.OrderStatusRejected, e.storedOrder(t, it).Status)
	assert.Contains(t, e.audit.actions(), "order.rejected")
}

func TestApproveBrokerRejectionAuditsClassNotText(t *testing.T) {
	e := newIntentEnv(t)
	it := e.create(t, "key-00000001")
	e.broker.FailSubmit = fmt.Errorf("alpaca: %w: account PA3SECRET", ports.ErrInsufficientFunds)

	v, err := e.svc.Approve(context.Background(), it.ID, human, "")
	require.NoError(t, err)
	assert.Equal(t, entities.RejectionBrokerRejected, v.Intent.RejectionCode)
	assert.Equal(t, entities.OrderStatusRejected, e.storedOrder(t, it).Status)

	var rejected *ports.AuditEvent
	for i := range e.audit.events {
		if e.audit.events[i].Action == "order.rejected" {
			rejected = &e.audit.events[i]
		}
	}
	require.NotNil(t, rejected)
	assert.Equal(t, "insufficient_funds", rejected.Payload["error_class"])
	assert.NotContains(t, fmt.Sprint(e.audit.events), "PA3SECRET")
	assert.NotContains(t, v.Intent.DecisionReason, "PA3SECRET")
}

func TestApproveOrderStoreFailureSubmitsNothing(t *testing.T) {
	e := newIntentEnv(t, func(d *usecases.IntentDeps) {
		d.Orders = failingSave{OrderRepositoryPort: d.Orders, err: errors.New("db connection lost")}
	})
	it := e.create(t, "key-00000001")

	_, err := e.svc.Approve(context.Background(), it.ID, human, "")
	assert.ErrorContains(t, err, "saving order")
	assert.Empty(t, e.broker.Submitted)
	got, _ := e.svc.Get(context.Background(), it.ID)
	assert.Equal(t, entities.IntentRejected, got.Intent.Status)
}

func TestApproveSubmissionAuditFailureSubmitsNothing(t *testing.T) {
	e := newIntentEnv(t)
	it := e.create(t, "key-00000001")
	e.audit.fail, e.audit.failOn = errors.New("db down"), "order.submit_requested"

	_, err := e.svc.Approve(context.Background(), it.ID, human, "")
	assert.ErrorIs(t, err, usecases.ErrAuditUnavailable)
	assert.Empty(t, e.broker.Submitted, "fail closed: no audit record, no order")
	assert.Equal(t, entities.OrderStatusRejected, e.storedOrder(t, it).Status)
	got, _ := e.svc.Get(context.Background(), it.ID)
	assert.Equal(t, entities.IntentRejected, got.Intent.Status)
}
