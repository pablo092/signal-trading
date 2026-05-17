// Package entities — RiskRules defines position sizing and risk constraints.
package entities

import "fmt"

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
func (r RiskRules) Validate() error {
	if r.MaxPositionSizePct <= 0 || r.MaxPositionSizePct > 0.25 {
		return fmt.Errorf("MaxPositionSizePct must be in (0, 0.25], got %.4f", r.MaxPositionSizePct)
	}
	if r.MaxDailyLossPct <= 0 || r.MaxDailyLossPct > 0.10 {
		return fmt.Errorf("MaxDailyLossPct must be in (0, 0.10], got %.4f", r.MaxDailyLossPct)
	}
	if r.StopLossPct <= 0 || r.StopLossPct >= 0.20 {
		return fmt.Errorf("StopLossPct must be in (0, 0.20), got %.4f", r.StopLossPct)
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
	if entryPrice <= 0 || portfolioEquity <= 0 {
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
