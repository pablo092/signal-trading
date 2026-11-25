// Package entities — RiskRules defines position sizing and risk constraints.
package entities

import (
	"fmt"
	"math"

	"github.com/shopspring/decimal"
)

// QtyScale is the number of decimal places kept in a computed share quantity.
// Sizing truncates (never rounds up), so a position never exceeds its cap.
const QtyScale = 6

// RiskRules encapsulates all risk management parameters for a strategy.
// Applied before every order is submitted to enforce capital protection.
type RiskRules struct {
	MaxPositionSizePct float64 // max % of portfolio per position (e.g. 0.05 = 5%)
	MaxDailyLossPct    float64 // stop trading if daily loss exceeds this (e.g. 0.02 = 2%)
	StopLossPct        float64 // auto stop-loss distance from entry (e.g. 0.03 = 3%)
	TakeProfitPct      float64 // auto take-profit target (e.g. 0.06 = 6%)
	MaxOpenPositions   int     // max concurrent open positions
	MinConfidence      float64 // minimum signal confidence to act (e.g. 0.60)
}

// DefaultRiskRules returns conservative defaults suitable for paper trading.
func DefaultRiskRules() RiskRules {
	return RiskRules{
		MaxPositionSizePct: 0.05, // 5% of portfolio per trade
		MaxDailyLossPct:    0.02, // stop day if down 2%
		StopLossPct:        0.03, // 3% stop loss
		TakeProfitPct:      0.06, // 6% take profit
		MaxOpenPositions:   5,
		MinConfidence:      0.60,
	}
}

// Validate checks that all risk rules are within acceptable bounds.
// NaN and ±Inf are rejected explicitly: every comparison with NaN is false, so
// range checks alone would let it through.
func (r RiskRules) Validate() error {
	for name, v := range map[string]float64{
		"MaxPositionSizePct": r.MaxPositionSizePct,
		"MaxDailyLossPct":    r.MaxDailyLossPct,
		"StopLossPct":        r.StopLossPct,
		"TakeProfitPct":      r.TakeProfitPct,
		"MinConfidence":      r.MinConfidence,
	} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("%s must be a finite number", name)
		}
	}
	if r.MaxPositionSizePct <= 0 || r.MaxPositionSizePct > 0.25 {
		return fmt.Errorf("MaxPositionSizePct must be in (0, 0.25], got %.4f", r.MaxPositionSizePct)
	}
	if r.MaxDailyLossPct <= 0 || r.MaxDailyLossPct > 0.10 {
		return fmt.Errorf("MaxDailyLossPct must be in (0, 0.10], got %.4f", r.MaxDailyLossPct)
	}
	if r.StopLossPct <= 0 || r.StopLossPct >= 0.20 {
		return fmt.Errorf("StopLossPct must be in (0, 0.20), got %.4f", r.StopLossPct)
	}
	// Upper bound keeps the sell-side take-profit price positive (entry * (1 - pct)).
	if r.TakeProfitPct <= 0 || r.TakeProfitPct >= 0.50 {
		return fmt.Errorf("TakeProfitPct must be in (0, 0.50), got %.4f", r.TakeProfitPct)
	}
	if r.MaxOpenPositions <= 0 {
		return fmt.Errorf("MaxOpenPositions must be > 0, got %d", r.MaxOpenPositions)
	}
	if r.MinConfidence < 0 || r.MinConfidence > 1 {
		return fmt.Errorf("MinConfidence must be in [0, 1], got %.4f", r.MinConfidence)
	}
	return nil
}

// CalcPositionSize returns the number of shares to buy given portfolio equity
// and entry price, respecting MaxPositionSizePct. The result is truncated to
// QtyScale decimals; non-positive inputs give zero.
func (r RiskRules) CalcPositionSize(portfolioEquity, entryPrice decimal.Decimal) decimal.Decimal {
	if !entryPrice.IsPositive() || !portfolioEquity.IsPositive() {
		return decimal.Zero
	}
	maxDollars := portfolioEquity.Mul(pct(r.MaxPositionSizePct))
	return maxDollars.DivRound(entryPrice, QtyScale+2).Truncate(QtyScale)
}

// StopLossPrice returns the stop-loss price given an entry price and side.
func (r RiskRules) StopLossPrice(entryPrice decimal.Decimal, side OrderSide) decimal.Decimal {
	if side == OrderSideBuy {
		return entryPrice.Mul(decimal.NewFromInt(1).Sub(pct(r.StopLossPct)))
	}
	return entryPrice.Mul(decimal.NewFromInt(1).Add(pct(r.StopLossPct)))
}

// TakeProfitPrice returns the take-profit price given an entry price and side.
func (r RiskRules) TakeProfitPrice(entryPrice decimal.Decimal, side OrderSide) decimal.Decimal {
	if side == OrderSideBuy {
		return entryPrice.Mul(decimal.NewFromInt(1).Add(pct(r.TakeProfitPct)))
	}
	return entryPrice.Mul(decimal.NewFromInt(1).Sub(pct(r.TakeProfitPct)))
}

// pct converts a validated (finite) percentage parameter to decimal.
func pct(v float64) decimal.Decimal { return decimal.NewFromFloat(v) }
