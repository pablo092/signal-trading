package usecases_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/trading-bot/bot-engine/internal/application/usecases"
	"github.com/trading-bot/bot-engine/internal/domain/entities"
	"github.com/trading-bot/bot-engine/internal/domain/ports"
)

// ── Manual mocks (no generated code needed for unit tests) ──

type mockBroker struct {
	equity    float64
	equityErr error
	brokerID  string
	submitErr error
}

func (m *mockBroker) SubmitOrder(_ context.Context, _ *entities.Order) (string, error) {
	return m.brokerID, m.submitErr
}
func (m *mockBroker) CancelOrder(_ context.Context, _ string) error        { return nil }
func (m *mockBroker) GetOrderStatus(_ context.Context, _ string) (entities.OrderStatus, float64, error) {
	return entities.OrderStatusFilled, 150.0, nil
}
func (m *mockBroker) GetPortfolioEquity(_ context.Context) (float64, error) {
	return m.equity, m.equityErr
}
func (m *mockBroker) GetPositions(_ context.Context) ([]entities.Position, error) {
	return nil, nil
}
func (m *mockBroker) IsMarketOpen(_ context.Context) (bool, error) { return true, nil }

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

// ── Helpers ──────────────────────────────────────────────────

func buySignal() *ports.Signal {
	return &ports.Signal{
		ID:           "sig-001",
		Symbol:       "AAPL",
		Direction:    "BUY",
		Strength:     "STRONG",
		Price:        150.0,
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

// ── Tests ─────────────────────────────────────────────────────

func TestPlaceOrder_SuccessfulBuy(t *testing.T) {
	broker := &mockBroker{equity: 100_000, brokerID: "alpaca-123"}
	repo := &mockOrderRepo{}
	signals := &mockSignalService{signal: buySignal()}
	publisher := &mockPublisher{}

	uc := buildUseCase(broker, repo, signals, publisher, defaultRules())
	out, err := uc.Execute(context.Background(), usecases.PlaceOrderInput{
		Symbol: "AAPL", Strategy: "rsi",
	})

	require.NoError(t, err)
	assert.False(t, out.Skipped)
	assert.NotNil(t, out.Order)
	assert.Equal(t, "AAPL", out.Order.Symbol)
	assert.Equal(t, entities.OrderSideBuy, out.Order.Side)
	assert.Len(t, repo.savedOrders, 1)
	assert.Len(t, publisher.published, 1)
	assert.Equal(t, "OrderPlaced", publisher.published[0].Type)
}

func TestPlaceOrder_SkipsHoldSignal(t *testing.T) {
	broker := &mockBroker{equity: 100_000}
	repo := &mockOrderRepo{}
	signals := &mockSignalService{signal: holdSignal()}
	publisher := &mockPublisher{}

	uc := buildUseCase(broker, repo, signals, publisher, defaultRules())
	out, err := uc.Execute(context.Background(), usecases.PlaceOrderInput{Symbol: "AAPL", Strategy: "rsi"})

	require.NoError(t, err)
	assert.True(t, out.Skipped)
	assert.Contains(t, out.SkipReason, "HOLD")
	assert.Nil(t, out.Order)
	assert.Empty(t, repo.savedOrders)
	assert.Empty(t, publisher.published)
}

func TestPlaceOrder_SkipsLowConfidence(t *testing.T) {
	broker := &mockBroker{equity: 100_000}
	sig := buySignal()
	sig.Confidence = 0.30 // below default 0.60

	uc := buildUseCase(broker, &mockOrderRepo{}, &mockSignalService{signal: sig}, &mockPublisher{}, defaultRules())
	out, err := uc.Execute(context.Background(), usecases.PlaceOrderInput{Symbol: "AAPL", Strategy: "rsi"})

	require.NoError(t, err)
	assert.True(t, out.Skipped)
	assert.Contains(t, out.SkipReason, "confidence")
}

func TestPlaceOrder_ErrorFetchingSignal(t *testing.T) {
	signals := &mockSignalService{err: errors.New("signal service down")}
	uc := buildUseCase(&mockBroker{equity: 100_000}, &mockOrderRepo{}, signals, &mockPublisher{}, defaultRules())

	_, err := uc.Execute(context.Background(), usecases.PlaceOrderInput{Symbol: "AAPL", Strategy: "rsi"})
	assert.ErrorContains(t, err, "fetching signal")
}

func TestPlaceOrder_BrokerRejection(t *testing.T) {
	broker := &mockBroker{equity: 100_000, submitErr: errors.New("insufficient funds")}
	repo := &mockOrderRepo{}
	signals := &mockSignalService{signal: buySignal()}

	uc := buildUseCase(broker, repo, signals, &mockPublisher{}, defaultRules())
	_, err := uc.Execute(context.Background(), usecases.PlaceOrderInput{Symbol: "AAPL", Strategy: "rsi"})

	assert.ErrorContains(t, err, "broker rejected")
	// Should have saved then updated to rejected
	require.Len(t, repo.savedOrders, 1)
	require.Len(t, repo.updatedOrders, 1)
	assert.Equal(t, entities.OrderStatusRejected, repo.updatedOrders[0].Status)
}

func TestPlaceOrder_RepoSaveFailure(t *testing.T) {
	repo := &mockOrderRepo{saveErr: errors.New("db connection lost")}
	uc := buildUseCase(&mockBroker{equity: 100_000}, repo, &mockSignalService{signal: buySignal()}, &mockPublisher{}, defaultRules())

	_, err := uc.Execute(context.Background(), usecases.PlaceOrderInput{Symbol: "AAPL", Strategy: "rsi"})
	assert.ErrorContains(t, err, "saving order")
}

func TestPlaceOrder_EquityFetchFailure(t *testing.T) {
	broker := &mockBroker{equityErr: errors.New("account unavailable")}
	uc := buildUseCase(broker, &mockOrderRepo{}, &mockSignalService{signal: buySignal()}, &mockPublisher{}, defaultRules())

	_, err := uc.Execute(context.Background(), usecases.PlaceOrderInput{Symbol: "AAPL", Strategy: "rsi"})
	assert.ErrorContains(t, err, "portfolio equity")
}

func TestPlaceOrder_SellSignal(t *testing.T) {
	sig := buySignal()
	sig.Direction = "SELL"
	broker := &mockBroker{equity: 100_000, brokerID: "alpaca-456"}
	repo := &mockOrderRepo{}

	uc := buildUseCase(broker, repo, &mockSignalService{signal: sig}, &mockPublisher{}, defaultRules())
	out, err := uc.Execute(context.Background(), usecases.PlaceOrderInput{Symbol: "AAPL", Strategy: "rsi"})

	require.NoError(t, err)
	assert.False(t, out.Skipped)
	assert.Equal(t, entities.OrderSideSell, out.Order.Side)
}

func TestPlaceOrder_PositionSizingUsesRiskRules(t *testing.T) {
	broker := &mockBroker{equity: 10_000, brokerID: "b-1"} // small account
	repo := &mockOrderRepo{}
	rules := defaultRules()
	rules.MaxPositionSizePct = 0.01 // only 1% = $100 / $150 = 0.66 shares

	uc := buildUseCase(broker, repo, &mockSignalService{signal: buySignal()}, &mockPublisher{}, rules)
	out, err := uc.Execute(context.Background(), usecases.PlaceOrderInput{Symbol: "AAPL", Strategy: "rsi"})

	require.NoError(t, err)
	// qty ≈ 0.66 — above minimum 0.001 so it should execute
	assert.False(t, out.Skipped)
	if out.Order != nil {
		assert.Less(t, out.Order.Quantity, 1.0)
	}
}
