package usecases

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/trading-bot/bot-engine/internal/domain/entities"
	"github.com/trading-bot/bot-engine/internal/domain/ports"
)

// Risk check names (contracts: order-intent risk.checks[].name).
const (
	CheckKillSwitch        = "kill_switch"
	CheckPaperMode         = "paper_mode"
	CheckMarketOpen        = "market_open"
	CheckSignalActionable  = "signal_actionable"
	CheckSignalFresh       = "signal_fresh"
	CheckConfidence        = "confidence"
	CheckSymbolAllowlist   = "symbol_allowlist"
	CheckStrategyAllowlist = "strategy_allowlist"
	CheckAccountActive     = "account_active"
	CheckDailyLoss         = "daily_loss"
	CheckOpenPositions     = "open_positions"
	CheckNotionalCap       = "notional_cap"
	CheckBuyingPower       = "buying_power"
	CheckSellCovered       = "sell_covered"
	CheckQuantity          = "quantity"
)

// riskInputTimeout bounds fetching every broker input for one evaluation.
const riskInputTimeout = 20 * time.Second

// RiskPolicy is the configuration the risk engine enforces (ADR-005 §5.5).
type RiskPolicy struct {
	Rules              entities.RiskRules
	SignalMaxAge       time.Duration
	MaxOrderNotional   decimal.Decimal
	SymbolAllowlist    []string // empty = every symbol is refused
	StrategyAllowlist  []string // empty = every strategy is refused
	AllowExtendedHours bool
}

// Validate refuses a policy the engine must not start with.
func (p RiskPolicy) Validate() error {
	if err := p.Rules.Validate(); err != nil {
		return err
	}
	if p.SignalMaxAge <= 0 {
		return fmt.Errorf("signal max age must be positive")
	}
	if !p.MaxOrderNotional.IsPositive() {
		return fmt.Errorf("max order notional must be positive")
	}
	return nil
}

// Sizing is the side and quantity an intent would trade.
type Sizing struct {
	Side     entities.OrderSide // "" when not actionable
	Qty      decimal.Decimal
	Notional decimal.Decimal
}

// RiskInput is what is being evaluated. Fixed is nil at creation (the engine
// sizes the order) and set before submission (the approved size is re-checked,
// never silently changed).
type RiskInput struct {
	Symbol   string
	Strategy string
	Signal   entities.SignalSnapshot
	Fixed    *Sizing
}

// RiskEngine evaluates the full checklist. It is fail-closed: an input that
// cannot be read fails every check that depends on it.
type RiskEngine struct {
	policy     RiskPolicy
	broker     ports.BrokerPort
	killSwitch KillSwitchChecker
	now        func() time.Time
}

// NewRiskEngine builds the engine; killSwitch may be nil only in tests.
func NewRiskEngine(policy RiskPolicy, broker ports.BrokerPort, killSwitch KillSwitchChecker) *RiskEngine {
	return &RiskEngine{policy: policy, broker: broker, killSwitch: killSwitch, now: time.Now}
}

// inputs is every external fact one evaluation uses, read once.
type riskInputs struct {
	engaged      bool
	engagedErr   error
	clock        ports.MarketClock
	clockErr     error
	account      ports.Account
	accountErr   error
	positions    []entities.Position
	positionsErr error
}

func (e *RiskEngine) fetch(ctx context.Context) riskInputs {
	ctx, cancel := context.WithTimeout(ctx, riskInputTimeout)
	defer cancel()
	var in riskInputs
	if e.killSwitch == nil {
		in.engagedErr = fmt.Errorf("no kill switch configured")
	} else {
		in.engaged, in.engagedErr = e.killSwitch.Engaged(ctx)
	}
	if !e.policy.AllowExtendedHours {
		in.clock, in.clockErr = e.broker.GetClock(ctx)
	}
	in.account, in.accountErr = e.broker.GetAccount(ctx)
	in.positions, in.positionsErr = e.broker.GetPositions(ctx)
	return in
}

// Evaluate runs every check and returns the report and the sizing it used.
func (e *RiskEngine) Evaluate(ctx context.Context, stage entities.RiskStage, in RiskInput) (entities.RiskReport, Sizing) {
	now := e.now().UTC()
	facts := e.fetch(ctx)
	p := e.policy
	var checks []entities.RiskCheck
	add := func(name string, passed bool, format string, args ...any) {
		checks = append(checks, entities.RiskCheck{Name: name, Passed: passed, Detail: fmt.Sprintf(format, args...)})
	}
	unavailable := func(name string, err error) {
		add(name, false, "input unavailable (%s): refusing", errorClass(err))
	}

	// Trading halts and mode.
	switch {
	case facts.engagedErr != nil:
		unavailable(CheckKillSwitch, facts.engagedErr)
	case facts.engaged:
		add(CheckKillSwitch, false, "kill switch is engaged")
	default:
		add(CheckKillSwitch, true, "not engaged")
	}
	add(CheckPaperMode, e.broker.Mode() == ports.TradingModePaper, "broker mode %q", e.broker.Mode())
	switch {
	case p.AllowExtendedHours:
		add(CheckMarketOpen, true, "extended hours allowed by RISK_ALLOW_EXTENDED_HOURS")
	case facts.clockErr != nil:
		unavailable(CheckMarketOpen, facts.clockErr)
	default:
		add(CheckMarketOpen, facts.clock.IsOpen, "market open: %t", facts.clock.IsOpen)
	}

	// Signal.
	sig := in.Signal
	actionable := sig.Direction == "BUY" || sig.Direction == "SELL"
	add(CheckSignalActionable, actionable, "direction %s", sig.Direction)
	age := now.Sub(sig.GeneratedAt)
	add(CheckSignalFresh, !sig.GeneratedAt.IsZero() && age >= -time.Minute && age <= p.SignalMaxAge,
		"age %s, max %s", age.Round(time.Second), p.SignalMaxAge)
	add(CheckConfidence, sig.Confidence >= p.Rules.MinConfidence, "%.2f, minimum %.2f", sig.Confidence, p.Rules.MinConfidence)

	// Allowlists.
	add(CheckSymbolAllowlist, slices.Contains(p.SymbolAllowlist, strings.ToUpper(in.Symbol)), "%s in [%s]", in.Symbol, strings.Join(p.SymbolAllowlist, ","))
	add(CheckStrategyAllowlist, slices.Contains(p.StrategyAllowlist, in.Strategy), "%s in [%s]", in.Strategy, strings.Join(p.StrategyAllowlist, ","))

	// Account.
	acct := facts.account
	accountOK := facts.accountErr == nil
	switch {
	case !accountOK:
		unavailable(CheckAccountActive, facts.accountErr)
	case acct.TradingBlocked:
		add(CheckAccountActive, false, "broker account is blocked from trading")
	case !acct.Equity.IsPositive():
		add(CheckAccountActive, false, "equity %s is not positive", acct.Equity)
	default:
		add(CheckAccountActive, true, "active, equity %s", acct.Equity.StringFixed(2))
	}
	switch {
	case !accountOK:
		unavailable(CheckDailyLoss, facts.accountErr)
	case !acct.LastEquity.IsPositive():
		add(CheckDailyLoss, false, "previous-close equity unavailable: refusing")
	default:
		loss := acct.DayPnL().Neg().Div(acct.LastEquity)
		limit := decimal.NewFromFloat(p.Rules.MaxDailyLossPct)
		add(CheckDailyLoss, loss.LessThan(limit), "loss %s%%, limit %s%%",
			loss.Mul(decimal.NewFromInt(100)).StringFixed(2), limit.Mul(decimal.NewFromInt(100)).StringFixed(2))
	}

	// Sizing (only at creation) and position checks.
	held := decimal.Zero
	open := 0
	if facts.positionsErr == nil {
		for _, pos := range facts.positions {
			if !pos.Quantity.IsZero() {
				open++
			}
			if strings.EqualFold(pos.Symbol, in.Symbol) {
				held = pos.Quantity
			}
		}
	}
	size := e.size(in, sig, acct, accountOK, held, facts.positionsErr == nil)

	switch {
	case size.Side != entities.OrderSideBuy:
		add(CheckOpenPositions, true, "not a buy")
	case facts.positionsErr != nil:
		unavailable(CheckOpenPositions, facts.positionsErr)
	case held.IsPositive():
		add(CheckOpenPositions, true, "adds to an existing %s position", in.Symbol)
	default:
		add(CheckOpenPositions, open < p.Rules.MaxOpenPositions, "%d open, maximum %d", open, p.Rules.MaxOpenPositions)
	}

	switch {
	case size.Side != entities.OrderSideBuy:
		add(CheckNotionalCap, true, "sells only reduce exposure")
	case !accountOK:
		unavailable(CheckNotionalCap, facts.accountErr)
	default:
		pctCap := acct.Equity.Mul(decimal.NewFromFloat(p.Rules.MaxPositionSizePct))
		add(CheckNotionalCap, size.Notional.LessThanOrEqual(pctCap) && size.Notional.LessThanOrEqual(p.MaxOrderNotional),
			"%s <= min(%s%% of equity %s, cap %s)", size.Notional.StringFixed(2),
			decimal.NewFromFloat(p.Rules.MaxPositionSizePct*100).String(), pctCap.StringFixed(2), p.MaxOrderNotional.String())
	}

	switch {
	case size.Side != entities.OrderSideBuy:
		add(CheckBuyingPower, true, "not a buy")
	case !accountOK:
		unavailable(CheckBuyingPower, facts.accountErr)
	default:
		add(CheckBuyingPower, size.Notional.LessThanOrEqual(acct.BuyingPower), "%s <= buying power %s",
			size.Notional.StringFixed(2), acct.BuyingPower.StringFixed(2))
	}

	switch {
	case size.Side != entities.OrderSideSell:
		add(CheckSellCovered, true, "not a sell")
	case facts.positionsErr != nil:
		unavailable(CheckSellCovered, facts.positionsErr)
	default:
		// No shorting: a sell must be covered by a held long position.
		add(CheckSellCovered, held.IsPositive() && size.Qty.LessThanOrEqual(held), "sell %s, held %s", size.Qty, held)
	}

	add(CheckQuantity, size.Side != "" && size.Qty.GreaterThanOrEqual(minOrderQty), "qty %s, minimum %s", size.Qty, minOrderQty)

	report := entities.RiskReport{Passed: true, Stage: stage, EvaluatedAt: now, Checks: checks}
	for _, c := range checks {
		if !c.Passed {
			report.Passed = false
			break
		}
	}
	return report, size
}

// size picks side and quantity. A BUY is the smaller of the percentage-of-
// equity size and the absolute notional cap; a SELL closes the held position.
func (e *RiskEngine) size(in RiskInput, sig entities.SignalSnapshot, acct ports.Account, accountOK bool, held decimal.Decimal, positionsOK bool) Sizing {
	if in.Fixed != nil {
		return *in.Fixed
	}
	var s Sizing
	switch sig.Direction {
	case "BUY":
		s.Side = entities.OrderSideBuy
		if accountOK && sig.Price.IsPositive() {
			byPct := e.policy.Rules.CalcPositionSize(acct.Equity, sig.Price)
			byCap := e.policy.MaxOrderNotional.DivRound(sig.Price, entities.QtyScale+2).Truncate(entities.QtyScale)
			s.Qty = decimal.Min(byPct, byCap)
		}
	case "SELL":
		s.Side = entities.OrderSideSell
		if positionsOK && held.IsPositive() {
			s.Qty = held
		}
	default:
		return s
	}
	s.Notional = s.Qty.Mul(sig.Price).Round(2)
	return s
}
