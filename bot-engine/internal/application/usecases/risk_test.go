package usecases_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/trading-bot/bot-engine/internal/application/usecases"
	"github.com/trading-bot/bot-engine/internal/domain/entities"
	"github.com/trading-bot/bot-engine/internal/domain/ports"
	"github.com/trading-bot/bot-engine/internal/infrastructure/broker/fakebroker"
)

func testPolicy() usecases.RiskPolicy {
	return usecases.RiskPolicy{
		Rules:             entities.DefaultRiskRules(),
		SignalMaxAge:      5 * time.Minute,
		MaxOrderNotional:  decimal.NewFromInt(1000),
		SymbolAllowlist:   []string{"SPY", "AAPL"},
		StrategyAllowlist: []string{"rsi"},
	}
}

func buySnapshot(price string) entities.SignalSnapshot {
	return entities.SignalSnapshot{ID: "sig-1", Direction: "BUY", Confidence: 0.8, Price: decimal.RequireFromString(price), GeneratedAt: time.Now()}
}

func evaluate(t *testing.T, b *fakebroker.Broker, ks usecases.KillSwitchChecker, p usecases.RiskPolicy, in usecases.RiskInput) (entities.RiskReport, usecases.Sizing, map[string]entities.RiskCheck) {
	t.Helper()
	rep, size := usecases.NewRiskEngine(p, b, ks).Evaluate(context.Background(), entities.RiskStageCreation, in)
	byName := map[string]entities.RiskCheck{}
	for _, c := range rep.Checks {
		byName[c.Name] = c
	}
	return rep, size, byName
}

func TestRiskPassesAndSizesByTheSmallerCap(t *testing.T) {
	b := fakebroker.New(decimal.NewFromInt(100_000)) // 5% = 5000, absolute cap 1000 wins
	rep, size, _ := evaluate(t, b, switchState{}, testPolicy(), usecases.RiskInput{Symbol: "AAPL", Strategy: "rsi", Signal: buySnapshot("200")})
	require.True(t, rep.Passed, "failed: %v", rep.Failed())
	assert.Equal(t, entities.OrderSideBuy, size.Side)
	assert.Equal(t, "5", size.Qty.String())
	assert.Equal(t, "1000", size.Notional.String())
	assert.Len(t, rep.Checks, 15, "every check is always reported")
}

func TestRiskPercentCapWinsOnSmallAccounts(t *testing.T) {
	b := fakebroker.New(decimal.NewFromInt(10_000)) // 5% = 500 < 1000
	_, size, _ := evaluate(t, b, switchState{}, testPolicy(), usecases.RiskInput{Symbol: "AAPL", Strategy: "rsi", Signal: buySnapshot("200")})
	assert.Equal(t, "2.5", size.Qty.String())
}

func TestRiskFailsClosedWhenInputsAreUnavailable(t *testing.T) {
	b := fakebroker.New(decimal.NewFromInt(100_000))
	b.FailAll = ports.ErrUnavailable
	rep, size, c := evaluate(t, b, switchState{err: errors.New("db down")}, testPolicy(),
		usecases.RiskInput{Symbol: "AAPL", Strategy: "rsi", Signal: buySnapshot("200")})
	require.False(t, rep.Passed)
	for _, name := range []string{usecases.CheckKillSwitch, usecases.CheckMarketOpen, usecases.CheckAccountActive,
		usecases.CheckDailyLoss, usecases.CheckOpenPositions, usecases.CheckNotionalCap, usecases.CheckBuyingPower, usecases.CheckQuantity} {
		assert.False(t, c[name].Passed, "%s must fail when its input is unavailable", name)
	}
	assert.True(t, size.Qty.IsZero(), "no size without an account")
}

func TestRiskRules(t *testing.T) {
	full := []entities.Position{
		{Symbol: "A", Quantity: decimal.NewFromInt(1)}, {Symbol: "B", Quantity: decimal.NewFromInt(1)},
		{Symbol: "C", Quantity: decimal.NewFromInt(1)}, {Symbol: "D", Quantity: decimal.NewFromInt(1)},
		{Symbol: "E", Quantity: decimal.NewFromInt(1)},
	}
	cases := []struct {
		name   string
		setup  func(*fakebroker.Broker, *usecases.RiskPolicy, *usecases.RiskInput)
		failed string
	}{
		{"kill switch", nil, usecases.CheckKillSwitch},
		{"market closed", func(b *fakebroker.Broker, _ *usecases.RiskPolicy, _ *usecases.RiskInput) { b.SetMarketOpen(false) }, usecases.CheckMarketOpen},
		{"hold", func(_ *fakebroker.Broker, _ *usecases.RiskPolicy, in *usecases.RiskInput) {
			in.Signal.Direction = "HOLD"
		}, usecases.CheckSignalActionable},
		{"stale signal", func(_ *fakebroker.Broker, _ *usecases.RiskPolicy, in *usecases.RiskInput) {
			in.Signal.GeneratedAt = time.Now().Add(-6 * time.Minute)
		}, usecases.CheckSignalFresh},
		{"low confidence", func(_ *fakebroker.Broker, _ *usecases.RiskPolicy, in *usecases.RiskInput) { in.Signal.Confidence = 0.5 }, usecases.CheckConfidence},
		{"symbol not allowed", func(_ *fakebroker.Broker, _ *usecases.RiskPolicy, in *usecases.RiskInput) { in.Symbol = "TSLA" }, usecases.CheckSymbolAllowlist},
		{"empty allowlist refuses all", func(_ *fakebroker.Broker, p *usecases.RiskPolicy, _ *usecases.RiskInput) { p.SymbolAllowlist = nil }, usecases.CheckSymbolAllowlist},
		{"strategy not allowed", func(_ *fakebroker.Broker, _ *usecases.RiskPolicy, in *usecases.RiskInput) { in.Strategy = "ma_cross" }, usecases.CheckStrategyAllowlist},
		{"account blocked", func(b *fakebroker.Broker, _ *usecases.RiskPolicy, _ *usecases.RiskInput) {
			b.SetAccount(ports.Account{Equity: decimal.NewFromInt(1000), LastEquity: decimal.NewFromInt(1000), BuyingPower: decimal.NewFromInt(1000), TradingBlocked: true})
		}, usecases.CheckAccountActive},
		{"daily loss", func(b *fakebroker.Broker, _ *usecases.RiskPolicy, _ *usecases.RiskInput) {
			b.SetAccount(ports.Account{Equity: decimal.NewFromInt(97_000), LastEquity: decimal.NewFromInt(100_000), BuyingPower: decimal.NewFromInt(97_000)})
		}, usecases.CheckDailyLoss},
		{"no previous close", func(b *fakebroker.Broker, _ *usecases.RiskPolicy, _ *usecases.RiskInput) {
			b.SetAccount(ports.Account{Equity: decimal.NewFromInt(100_000), BuyingPower: decimal.NewFromInt(100_000)})
		}, usecases.CheckDailyLoss},
		{"max open positions", func(b *fakebroker.Broker, _ *usecases.RiskPolicy, _ *usecases.RiskInput) { b.SetPositions(full) }, usecases.CheckOpenPositions},
		{"buying power", func(b *fakebroker.Broker, _ *usecases.RiskPolicy, _ *usecases.RiskInput) {
			b.SetAccount(ports.Account{Equity: decimal.NewFromInt(100_000), LastEquity: decimal.NewFromInt(100_000), BuyingPower: decimal.NewFromInt(10)})
		}, usecases.CheckBuyingPower},
		{"approved size above cap at submission", func(_ *fakebroker.Broker, _ *usecases.RiskPolicy, in *usecases.RiskInput) {
			in.Fixed = &usecases.Sizing{Side: entities.OrderSideBuy, Qty: decimal.NewFromInt(10), Notional: decimal.NewFromInt(2000)}
		}, usecases.CheckNotionalCap},
		{"uncovered sell", func(_ *fakebroker.Broker, _ *usecases.RiskPolicy, in *usecases.RiskInput) {
			in.Signal.Direction = "SELL"
		}, usecases.CheckSellCovered},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := fakebroker.New(decimal.NewFromInt(100_000))
			p := testPolicy()
			in := usecases.RiskInput{Symbol: "AAPL", Strategy: "rsi", Signal: buySnapshot("200")}
			ks := switchState{}
			if tc.setup == nil {
				ks.engaged = true
			} else {
				tc.setup(b, &p, &in)
			}
			rep, _, c := evaluate(t, b, ks, p, in)
			assert.False(t, rep.Passed)
			assert.False(t, c[tc.failed].Passed, "%s should fail: %+v", tc.failed, c[tc.failed])
		})
	}
}

func TestRiskSellClosesTheHeldPosition(t *testing.T) {
	b := fakebroker.New(decimal.NewFromInt(100_000))
	b.SetPositions([]entities.Position{{Symbol: "AAPL", Quantity: decimal.RequireFromString("3.5")}})
	sig := buySnapshot("200")
	sig.Direction = "SELL"
	rep, size, _ := evaluate(t, b, switchState{}, testPolicy(), usecases.RiskInput{Symbol: "AAPL", Strategy: "rsi", Signal: sig})
	require.True(t, rep.Passed, "failed: %v", rep.Failed())
	assert.Equal(t, entities.OrderSideSell, size.Side)
	assert.Equal(t, "3.5", size.Qty.String())
}

func TestRiskAddingToAHeldSymbolIgnoresThePositionLimit(t *testing.T) {
	b := fakebroker.New(decimal.NewFromInt(100_000))
	var pos []entities.Position
	for _, s := range []string{"AAPL", "B", "C", "D", "E"} {
		pos = append(pos, entities.Position{Symbol: s, Quantity: decimal.NewFromInt(1)})
	}
	b.SetPositions(pos)
	_, _, c := evaluate(t, b, switchState{}, testPolicy(), usecases.RiskInput{Symbol: "AAPL", Strategy: "rsi", Signal: buySnapshot("200")})
	assert.True(t, c[usecases.CheckOpenPositions].Passed)
}

func TestRiskPolicyValidate(t *testing.T) {
	p := testPolicy()
	require.NoError(t, p.Validate())
	p.MaxOrderNotional = decimal.Zero
	assert.Error(t, p.Validate())
}
