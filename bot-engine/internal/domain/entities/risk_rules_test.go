package entities

import (
	"math"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

func TestDefaultRiskRules_AreValidAndDocumented(t *testing.T) {
	r := DefaultRiskRules()
	if err := r.Validate(); err != nil {
		t.Fatalf("defaults must validate: %v", err)
	}
	// Values documented in the README risk table.
	want := RiskRules{
		MaxPositionSizePct: 0.05, MaxDailyLossPct: 0.02, StopLossPct: 0.03,
		TakeProfitPct: 0.06, MaxOpenPositions: 5, MinConfidence: 0.60,
	}
	if r != want {
		t.Fatalf("defaults changed: got %+v, want %+v", r, want)
	}
}

func TestValidate(t *testing.T) {
	base := DefaultRiskRules()
	nan, inf := math.NaN(), math.Inf(1)
	cases := []struct {
		name    string
		mutate  func(*RiskRules)
		wantErr string // substring; "" = valid
	}{
		{"defaults", func(*RiskRules) {}, ""},
		{"position size at upper bound", func(r *RiskRules) { r.MaxPositionSizePct = 0.25 }, ""},
		{"position size zero", func(r *RiskRules) { r.MaxPositionSizePct = 0 }, "MaxPositionSizePct"},
		{"position size negative", func(r *RiskRules) { r.MaxPositionSizePct = -0.01 }, "MaxPositionSizePct"},
		{"position size above 25%", func(r *RiskRules) { r.MaxPositionSizePct = 0.2501 }, "MaxPositionSizePct"},
		{"daily loss at upper bound", func(r *RiskRules) { r.MaxDailyLossPct = 0.10 }, ""},
		{"daily loss zero", func(r *RiskRules) { r.MaxDailyLossPct = 0 }, "MaxDailyLossPct"},
		{"daily loss above 10%", func(r *RiskRules) { r.MaxDailyLossPct = 0.11 }, "MaxDailyLossPct"},
		{"stop loss zero", func(r *RiskRules) { r.StopLossPct = 0 }, "StopLossPct"},
		{"stop loss at 20% (exclusive)", func(r *RiskRules) { r.StopLossPct = 0.20 }, "StopLossPct"},
		{"stop loss just under 20%", func(r *RiskRules) { r.StopLossPct = 0.1999 }, ""},
		{"take profit zero", func(r *RiskRules) { r.TakeProfitPct = 0 }, "TakeProfitPct"},
		{"take profit negative", func(r *RiskRules) { r.TakeProfitPct = -0.06 }, "TakeProfitPct"},
		{"take profit at 50% (exclusive)", func(r *RiskRules) { r.TakeProfitPct = 0.50 }, "TakeProfitPct"},
		{"take profit 49%", func(r *RiskRules) { r.TakeProfitPct = 0.49 }, ""},
		{"max positions zero", func(r *RiskRules) { r.MaxOpenPositions = 0 }, "MaxOpenPositions"},
		{"max positions negative", func(r *RiskRules) { r.MaxOpenPositions = -1 }, "MaxOpenPositions"},
		{"confidence zero allowed", func(r *RiskRules) { r.MinConfidence = 0 }, ""},
		{"confidence one allowed", func(r *RiskRules) { r.MinConfidence = 1 }, ""},
		{"confidence above one", func(r *RiskRules) { r.MinConfidence = 1.01 }, "MinConfidence"},
		{"confidence negative", func(r *RiskRules) { r.MinConfidence = -0.1 }, "MinConfidence"},
		{"NaN position size", func(r *RiskRules) { r.MaxPositionSizePct = nan }, "finite"},
		{"NaN daily loss", func(r *RiskRules) { r.MaxDailyLossPct = nan }, "finite"},
		{"NaN stop loss", func(r *RiskRules) { r.StopLossPct = nan }, "finite"},
		{"NaN take profit", func(r *RiskRules) { r.TakeProfitPct = nan }, "finite"},
		{"NaN confidence", func(r *RiskRules) { r.MinConfidence = nan }, "finite"},
		{"Inf position size", func(r *RiskRules) { r.MaxPositionSizePct = inf }, "finite"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := base
			tc.mutate(&r)
			err := r.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("want valid, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func TestCalcPositionSize(t *testing.T) {
	r := DefaultRiskRules() // 5% per position
	cases := []struct {
		name          string
		equity, price string
		want          string
	}{
		{"5% of 100k at $100", "100000", "100", "50"},
		{"fractional result is truncated to 6 dp", "100000", "354.24", "14.114724"}, // 5000/354.24 = 14.1147244...
		{"price above budget gives < 1 share", "1000", "100", "0.5"},
		{"never rounds up", "100", "3", "1.666666"},
		{"zero equity", "0", "100", "0"},
		{"negative equity", "-5000", "100", "0"},
		{"zero price", "100000", "0", "0"},
		{"negative price", "100000", "-1", "0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := r.CalcPositionSize(d(tc.equity), d(tc.price))
			if !got.Equal(d(tc.want)) {
				t.Fatalf("CalcPositionSize(%s, %s) = %s, want %s", tc.equity, tc.price, got, tc.want)
			}
		})
	}
}

func TestPositionValueNeverExceedsCap(t *testing.T) {
	equity := d("123456.78")
	for _, p := range []float64{0.01, 0.05, 0.10, 0.25} {
		r := DefaultRiskRules()
		r.MaxPositionSizePct = p
		limit := equity.Mul(decimal.NewFromFloat(p))
		for _, price := range []string{"0.5", "1", "17.3", "354.24", "4999.99"} {
			value := r.CalcPositionSize(equity, d(price)).Mul(d(price))
			if value.GreaterThan(limit) {
				t.Fatalf("pct=%v price=%s: position value %s exceeds cap %s", p, price, value, limit)
			}
		}
	}
}

func TestStopLossAndTakeProfitPrices(t *testing.T) {
	r := DefaultRiskRules() // SL 3%, TP 6%
	cases := []struct {
		name          string
		side          OrderSide
		entry, sl, tp string
	}{
		{"buy: SL below, TP above", OrderSideBuy, "100", "97", "106"},
		{"sell: SL above, TP below", OrderSideSell, "100", "103", "94"},
		{"buy at fractional price is exact", OrderSideBuy, "354.24", "343.6128", "375.4944"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := r.StopLossPrice(d(tc.entry), tc.side); !got.Equal(d(tc.sl)) {
				t.Fatalf("StopLossPrice = %s, want %s", got, tc.sl)
			}
			if got := r.TakeProfitPrice(d(tc.entry), tc.side); !got.Equal(d(tc.tp)) {
				t.Fatalf("TakeProfitPrice = %s, want %s", got, tc.tp)
			}
		})
	}
}

func TestProtectivePricesStayPositiveForAnyValidRules(t *testing.T) {
	// The validated upper bounds (SL < 20%, TP < 50%) keep every derived price > 0
	// and on the protective side of the entry.
	r := DefaultRiskRules()
	r.StopLossPct, r.TakeProfitPct = 0.1999, 0.4999
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	entry := d("10")
	for _, side := range []OrderSide{OrderSideBuy, OrderSideSell} {
		sl, tp := r.StopLossPrice(entry, side), r.TakeProfitPrice(entry, side)
		if !sl.IsPositive() || !tp.IsPositive() {
			t.Fatalf("%s: non-positive price sl=%s tp=%s", side, sl, tp)
		}
		if side == OrderSideBuy && !(sl.LessThan(entry) && tp.GreaterThan(entry)) {
			t.Fatalf("buy: want sl < entry < tp, got sl=%s tp=%s", sl, tp)
		}
		if side == OrderSideSell && !(sl.GreaterThan(entry) && tp.LessThan(entry)) {
			t.Fatalf("sell: want tp < entry < sl, got sl=%s tp=%s", sl, tp)
		}
	}
}
