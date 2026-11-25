// Package entities — RiskRules defines position sizing and risk constraints.
package entities

import (
	"fmt"
	"math"
)

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
// and entry price, respecting MaxPositionSizePct.
func (r RiskRules) CalcPositionSize(portfolioEquity, entryPrice float64) float64 {
	if !(entryPrice > 0) || !(portfolioEquity > 0) || // also false for NaN
		math.IsInf(entryPrice, 0) || math.IsInf(portfolioEquity, 0) {
		return 0
	}
	maxDollars := portfolioEquity * r.MaxPositionSizePct
	return maxDollars / entryPrice
}

// StopLossPrice returns the stop-loss price given an entry price and side.
func (r RiskRules) StopLossPrice(entryPrice float64, side OrderSide) float64 {
	if side == OrderSideBuy {
		return entryPrice * (1 - r.StopLossPct)
	}
	return entryPrice * (1 + r.StopLossPct)
}

// TakeProfitPrice returns the take-profit price given an entry price and side.
func (r RiskRules) TakeProfitPrice(entryPrice float64, side OrderSide) float64 {
	if side == OrderSideBuy {
		return entryPrice * (1 + r.TakeProfitPct)
	}
	return entryPrice * (1 - r.TakeProfitPct)
}
