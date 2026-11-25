package usecases_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/trading-bot/bot-engine/internal/application/usecases"
	"github.com/trading-bot/bot-engine/internal/domain/entities"
	"github.com/trading-bot/bot-engine/internal/domain/ports"
	"github.com/trading-bot/bot-engine/internal/infrastructure/broker/fakebroker"
)

// ── Manual mocks (no generated code needed for unit tests) ──

type mockOrderRepo struct {
	savedOrders   []*entities.Order
	updatedOrders []*entities.Order
	saveErr       error
}

func (m *mockOrderRepo) Save(_ context.Context, o *entities.Order) error {
	m.savedOrders = append(m.savedOrders, o)
	return m.saveErr
}
func (m *mockOrderRepo) FindByID(_ context.Context, _ interface{}) (*entities.Order, error) {
	return nil, nil
}
func (m *mockOrderRepo) FindByStatus(_ context.Context, _ entities.OrderStatus) ([]*entities.Order, error) {
	return nil, nil
}
func (m *mockOrderRepo) FindBySymbol(_ context.Context, _ string) ([]*entities.Order, error) {
	return nil, nil
}
func (m *mockOrderRepo) Update(_ context.Context, o *entities.Order) error {
	m.updatedOrders = append(m.updatedOrders, o)
	return nil
}

type mockSignalService struct {
	signal *ports.Signal
	err    error
}

func (m *mockSignalService) GetSignal(_ context.Context, _, _ string) (*ports.Signal, error) {
	return m.signal, m.err
}

type mockPublisher struct {
	published []ports.DomainEvent
}

func (m *mockPublisher) Publish(_ context.Context, e ports.DomainEvent) error {
	m.published = append(m.published, e)
	return nil
}

func (m *mockPublisher) ofType(eventType string) []ports.DomainEvent {
	var out []ports.DomainEvent
	for _, e := range m.published {
		if e.Type == eventType {
			out = append(out, e)
		}
	}
	return out
}

// recordingAudit keeps events and can be made to fail.
type recordingAudit struct {
	events []ports.AuditEvent
	fail   error
}

func (a *recordingAudit) Record(_ context.Context, e ports.AuditEvent) error {
	if a.fail != nil {
		return a.fail
	}
	a.events = append(a.events, e)
	return nil
}

func (a *recordingAudit) actions() []string {
	out := make([]string, len(a.events))
	for i, e := range a.events {
		out[i] = e.Action
	}
	return out
}

type switchState struct {
	engaged bool
	err     error
}

func (s switchState) Engaged(context.Context) (bool, error) { return s.engaged, s.err }

// ── Helpers ──────────────────────────────────────────────────

func buySignal() *ports.Signal {
	return &ports.Signal{
		ID:           "sig-001",
		Symbol:       "AAPL",
		Direction:    "BUY",
		Strength:     "STRONG",
		Price:        decimal.NewFromInt(150),
		Confidence:   0.85,
		Reason:       "RSI oversold",
		StrategyName: "RSIStrategy(period=14)",
		IsActionable: true,
	}
}

func holdSignal() *ports.Signal {
	s := buySignal()
	s.Direction = "HOLD"
	s.IsActionable = false
	return s
}

func defaultRules() entities.RiskRules {
	return entities.DefaultRiskRules()
}

func buildUseCase(broker ports.BrokerPort, repo ports.OrderRepositoryPort, sig ports.SignalServicePort, pub ports.EventPublisherPort, rules entities.RiskRules) *usecases.PlaceOrderUseCase {
	return usecases.NewPlaceOrderUseCase(broker, repo, sig, pub, rules, zap.NewNop())
}

// paperBroker returns a fake broker with the given equity that fills AAPL at $150.
func paperBroker(equity int64) *fakebroker.Broker {
	b := fakebroker.New(decimal.NewFromInt(equity))
	b.SetFillPrice("AAPL", decimal.NewFromInt(150))
	return b
}

func run(t *testing.T, uc *usecases.PlaceOrderUseCase) (*usecases.PlaceOrderOutput, error) {
	t.Helper()
	return uc.Execute(context.Background(), usecases.PlaceOrderInput{Symbol: "AAPL", Strategy: "rsi"})
}

// ── Tests ─────────────────────────────────────────────────────

func TestPlaceOrder_SuccessfulBuy(t *testing.T) {
	broker := paperBroker(100_000)
	repo := &mockOrderRepo{}
	publisher := &mockPublisher{}

	out, err := run(t, buildUseCase(broker, repo, &mockSignalService{signal: buySignal()}, publisher, defaultRules()))

	require.NoError(t, err)
	assert.False(t, out.Skipped)
	require.NotNil(t, out.Order)
	assert.Equal(t, "AAPL", out.Order.Symbol)
	assert.Equal(t, entities.OrderSideBuy, out.Order.Side)
	assert.Equal(t, entities.OrderStatusFilled, out.Order.Status)
	assert.Equal(t, "fake-1", out.Order.BrokerID)
	// 5% of 100k at $150 = 33.333333 (truncated), filled at the broker's price.
	assert.True(t, out.Order.Quantity.Equal(decimal.RequireFromString("33.333333")), out.Order.Quantity.String())
	assert.True(t, out.Order.FilledQty.Equal(out.Order.Quantity))
	require.NotNil(t, out.Order.FilledPrice)
	assert.True(t, out.Order.FilledPrice.Equal(decimal.NewFromInt(150)))
	assert.Len(t, repo.savedOrders, 1)
	require.Len(t, publisher.ofType(usecases.EventOrderPlaced), 1)
	assert.Len(t, publisher.ofType(usecases.EventSignalReceived), 1)
}

func TestPlaceOrder_SendsOrderIDAsClientOrderID(t *testing.T) {
	broker := paperBroker(100_000)
	out, err := run(t, buildUseCase(broker, &mockOrderRepo{}, &mockSignalService{signal: buySignal()}, &mockPublisher{}, defaultRules()))

	require.NoError(t, err)
	require.Len(t, broker.Submitted, 1)
	assert.Equal(t, out.Order.ID.String(), broker.Submitted[0].ClientOrderID)
	assert.Equal(t, entities.OrderTypeMarket, broker.Submitted[0].Type)
}

func TestPlaceOrder_WorkingOrderIsNotReportedAsFilled(t *testing.T) {
	broker := fakebroker.New(decimal.NewFromInt(100_000)) // no fill price: order stays working
	out, err := run(t, buildUseCase(broker, &mockOrderRepo{}, &mockSignalService{signal: buySignal()}, &mockPublisher{}, defaultRules()))

	require.NoError(t, err)
	assert.Equal(t, entities.OrderStatusSubmitted, out.Order.Status)
	assert.Nil(t, out.Order.FilledPrice)
	assert.Nil(t, out.Order.FilledAt)
	assert.True(t, out.Order.FilledQty.IsZero())
}

func TestPlaceOrder_SkipsHoldSignal(t *testing.T) {
	broker := paperBroker(100_000)
	repo := &mockOrderRepo{}
	publisher := &mockPublisher{}

	out, err := run(t, buildUseCase(broker, repo, &mockSignalService{signal: holdSignal()}, publisher, defaultRules()))

	require.NoError(t, err)
	assert.True(t, out.Skipped)
	assert.Contains(t, out.SkipReason, "HOLD")
	assert.Nil(t, out.Order)
	assert.Empty(t, repo.savedOrders)
	assert.Empty(t, publisher.ofType(usecases.EventOrderPlaced))
	assert.Empty(t, broker.Submitted)
}

func TestPlaceOrder_SkipsLowConfidence(t *testing.T) {
	sig := buySignal()
	sig.Confidence = 0.30 // below default 0.60

	out, err := run(t, buildUseCase(paperBroker(100_000), &mockOrderRepo{}, &mockSignalService{signal: sig}, &mockPublisher{}, defaultRules()))

	require.NoError(t, err)
	assert.True(t, out.Skipped)
	assert.Contains(t, out.SkipReason, "confidence")
}

func TestPlaceOrder_ErrorFetchingSignal(t *testing.T) {
	signals := &mockSignalService{err: errors.New("signal service down")}
	_, err := run(t, buildUseCase(paperBroker(100_000), &mockOrderRepo{}, signals, &mockPublisher{}, defaultRules()))
	assert.ErrorContains(t, err, "fetching signal")
}

func TestPlaceOrder_BrokerRejection(t *testing.T) {
	broker := paperBroker(100_000)
	broker.FailSubmit = fmt.Errorf("alpaca place order: %w", ports.ErrInsufficientFunds)
	repo := &mockOrderRepo{}

	_, err := run(t, buildUseCase(broker, repo, &mockSignalService{signal: buySignal()}, &mockPublisher{}, defaultRules()))

	assert.ErrorContains(t, err, "broker rejected")
	assert.ErrorIs(t, err, ports.ErrInsufficientFunds)
	require.Len(t, repo.savedOrders, 1)
	require.Len(t, repo.updatedOrders, 1)
	assert.Equal(t, entities.OrderStatusRejected, repo.updatedOrders[0].Status)
}

func TestPlaceOrder_UnknownOutcomeIsReconciledByClientID(t *testing.T) {
	broker := paperBroker(100_000)
	broker.SubmitThenFail = fmt.Errorf("alpaca place order: %w: %w", ports.ErrUnavailable, context.DeadlineExceeded)
	repo := &mockOrderRepo{}

	out, err := run(t, buildUseCase(broker, repo, &mockSignalService{signal: buySignal()}, &mockPublisher{}, defaultRules()))

	require.NoError(t, err, "the broker has the order, so the submission succeeded")
	assert.Equal(t, "fake-1", out.Order.BrokerID)
	assert.Equal(t, entities.OrderStatusFilled, out.Order.Status)
	assert.Len(t, broker.Submitted, 1, "never resubmitted")
}

func TestPlaceOrder_UnknownOutcomeNotAtBrokerIsRejected(t *testing.T) {
	broker := paperBroker(100_000)
	broker.FailSubmit = fmt.Errorf("alpaca place order: %w", ports.ErrUnavailable) // never recorded
	repo := &mockOrderRepo{}

	_, err := run(t, buildUseCase(broker, repo, &mockSignalService{signal: buySignal()}, &mockPublisher{}, defaultRules()))

	assert.ErrorContains(t, err, "did not accept")
	require.Len(t, repo.updatedOrders, 1)
	assert.Equal(t, entities.OrderStatusRejected, repo.updatedOrders[0].Status)
}

func TestPlaceOrder_UnknownOutcomeAndFailedLookupStaysPending(t *testing.T) {
	broker := paperBroker(100_000)
	broker.FailSubmit = fmt.Errorf("alpaca place order: %w", ports.ErrUnavailable)
	broker.FailGet = fmt.Errorf("alpaca get order by client ID: %w", ports.ErrUnavailable)
	repo := &mockOrderRepo{}

	_, err := run(t, buildUseCase(broker, repo, &mockSignalService{signal: buySignal()}, &mockPublisher{}, defaultRules()))

	assert.ErrorContains(t, err, "outcome unknown")
	require.Len(t, repo.savedOrders, 1)
	assert.Empty(t, repo.updatedOrders, "not marked rejected: the order may exist at the broker")
}

func TestPlaceOrder_RepoSaveFailure(t *testing.T) {
	repo := &mockOrderRepo{saveErr: errors.New("db connection lost")}
	broker := paperBroker(100_000)
	_, err := run(t, buildUseCase(broker, repo, &mockSignalService{signal: buySignal()}, &mockPublisher{}, defaultRules()))
	assert.ErrorContains(t, err, "saving order")
	assert.Empty(t, broker.Submitted)
}

func TestPlaceOrder_EquityFetchFailure(t *testing.T) {
	broker := paperBroker(100_000)
	broker.FailAccount = fmt.Errorf("alpaca get account: %w", ports.ErrUnavailable)
	_, err := run(t, buildUseCase(broker, &mockOrderRepo{}, &mockSignalService{signal: buySignal()}, &mockPublisher{}, defaultRules()))
	assert.ErrorContains(t, err, "portfolio equity")
	assert.Empty(t, broker.Submitted)
}

func TestPlaceOrder_DailyLossLimitStopsTrading(t *testing.T) {
	broker := paperBroker(100_000)
	broker.SetAccount(ports.Account{ // down 2.5% since the previous close; limit is 2%
		Equity: decimal.NewFromInt(97_500), LastEquity: decimal.NewFromInt(100_000), BuyingPower: decimal.NewFromInt(97_500),
	})
	out, err := run(t, buildUseCase(broker, &mockOrderRepo{}, &mockSignalService{signal: buySignal()}, &mockPublisher{}, defaultRules()))

	require.NoError(t, err)
	assert.True(t, out.Skipped)
	assert.Contains(t, out.SkipReason, "daily loss limit reached: 2.50%")
	assert.Empty(t, broker.Submitted)
}

func TestPlaceOrder_DailyLossBelowLimitTrades(t *testing.T) {
	broker := paperBroker(100_000)
	broker.SetAccount(ports.Account{ // down 1%
		Equity: decimal.NewFromInt(99_000), LastEquity: decimal.NewFromInt(100_000), BuyingPower: decimal.NewFromInt(99_000),
	})
	out, err := run(t, buildUseCase(broker, &mockOrderRepo{}, &mockSignalService{signal: buySignal()}, &mockPublisher{}, defaultRules()))

	require.NoError(t, err)
	assert.False(t, out.Skipped)
}

func TestPlaceOrder_BlockedAccountSkips(t *testing.T) {
	broker := paperBroker(100_000)
	broker.SetAccount(ports.Account{Equity: decimal.NewFromInt(100_000), LastEquity: decimal.NewFromInt(100_000), TradingBlocked: true})
	out, err := run(t, buildUseCase(broker, &mockOrderRepo{}, &mockSignalService{signal: buySignal()}, &mockPublisher{}, defaultRules()))

	require.NoError(t, err)
	assert.True(t, out.Skipped)
	assert.Contains(t, out.SkipReason, "blocked")
	assert.Empty(t, broker.Submitted)
}

func TestPlaceOrder_SellSignal(t *testing.T) {
	sig := buySignal()
	sig.Direction = "SELL"
	out, err := run(t, buildUseCase(paperBroker(100_000), &mockOrderRepo{}, &mockSignalService{signal: sig}, &mockPublisher{}, defaultRules()))

	require.NoError(t, err)
	assert.False(t, out.Skipped)
	assert.Equal(t, entities.OrderSideSell, out.Order.Side)
}

func TestPlaceOrder_PositionSizingUsesRiskRules(t *testing.T) {
	rules := defaultRules()
	rules.MaxPositionSizePct = 0.01 // 1% of $10k = $100 / $150 = 0.666666 shares

	out, err := run(t, buildUseCase(paperBroker(10_000), &mockOrderRepo{}, &mockSignalService{signal: buySignal()}, &mockPublisher{}, rules))

	require.NoError(t, err)
	assert.False(t, out.Skipped)
	require.NotNil(t, out.Order)
	assert.True(t, out.Order.Quantity.Equal(decimal.RequireFromString("0.666666")), out.Order.Quantity.String())
}

// ── Audit (BL-14) and kill switch (BL-19) ─────────────────────

func TestPlaceOrder_AuditsSubmissionWithInputs(t *testing.T) {
	audit := &recordingAudit{}
	uc := buildUseCase(paperBroker(100_000), &mockOrderRepo{}, &mockSignalService{signal: buySignal()}, &mockPublisher{}, defaultRules()).WithAudit(audit)
	out, err := run(t, uc)

	require.NoError(t, err)
	assert.Equal(t, []string{"order.submit_requested", "order.submitted"}, audit.actions())
	req := audit.events[0]
	assert.Equal(t, out.Order.ID.String(), req.Target)
	assert.Equal(t, "100000", req.Payload["equity"])
	assert.Equal(t, "33.333333", req.Payload["qty"])
	assert.Equal(t, "sig-001", req.Payload["signal_id"])
	assert.Equal(t, "filled", audit.events[1].Result)
}

func TestPlaceOrder_AuditsSkipsWithReason(t *testing.T) {
	audit := &recordingAudit{}
	uc := buildUseCase(paperBroker(100_000), &mockOrderRepo{}, &mockSignalService{signal: holdSignal()}, &mockPublisher{}, defaultRules()).WithAudit(audit)
	_, err := run(t, uc)

	require.NoError(t, err)
	require.Equal(t, []string{"order.skipped"}, audit.actions())
	assert.Equal(t, "HOLD signal", audit.events[0].Payload["reason"])
}

func TestPlaceOrder_AuditsRejectionClassNotText(t *testing.T) {
	audit := &recordingAudit{}
	broker := paperBroker(100_000)
	broker.FailSubmit = fmt.Errorf("alpaca: %w: account PA3SECRET", ports.ErrInsufficientFunds)
	uc := buildUseCase(broker, &mockOrderRepo{}, &mockSignalService{signal: buySignal()}, &mockPublisher{}, defaultRules()).WithAudit(audit)
	_, err := run(t, uc)

	require.Error(t, err)
	assert.Equal(t, []string{"order.submit_requested", "order.rejected"}, audit.actions())
	assert.Equal(t, "insufficient_funds", audit.events[1].Payload["error_class"])
	assert.NotContains(t, fmt.Sprint(audit.events), "PA3SECRET")
}

func TestPlaceOrder_AuditFailureBlocksSubmission(t *testing.T) {
	broker := paperBroker(100_000)
	repo := &mockOrderRepo{}
	uc := buildUseCase(broker, repo, &mockSignalService{signal: buySignal()}, &mockPublisher{}, defaultRules()).
		WithAudit(&recordingAudit{fail: errors.New("db down")})
	_, err := run(t, uc)

	assert.ErrorIs(t, err, usecases.ErrAuditUnavailable)
	assert.Empty(t, broker.Submitted, "fail closed: no audit record, no order")
	require.Len(t, repo.updatedOrders, 1)
	assert.Equal(t, entities.OrderStatusRejected, repo.updatedOrders[0].Status)
}

func TestPlaceOrder_KillSwitchBlocksBeforeAnyCall(t *testing.T) {
	for name, state := range map[string]switchState{
		"engaged":           {engaged: true},
		"state unavailable": {err: errors.New("db down")},
	} {
		t.Run(name, func(t *testing.T) {
			broker := paperBroker(100_000)
			signals := &mockSignalService{signal: buySignal()}
			audit := &recordingAudit{}
			uc := buildUseCase(broker, &mockOrderRepo{}, signals, &mockPublisher{}, defaultRules()).WithAudit(audit).WithKillSwitch(state)
			_, err := run(t, uc)

			assert.ErrorIs(t, err, ports.ErrKillSwitchEngaged)
			assert.Empty(t, broker.Submitted)
			assert.Equal(t, []string{"order.blocked"}, audit.actions())
		})
	}
}

func TestPlaceOrder_KillSwitchReleasedAllowsOrders(t *testing.T) {
	uc := buildUseCase(paperBroker(100_000), &mockOrderRepo{}, &mockSignalService{signal: buySignal()}, &mockPublisher{}, defaultRules()).
		WithKillSwitch(switchState{engaged: false})
	out, err := run(t, uc)
	require.NoError(t, err)
	assert.False(t, out.Skipped)
}
