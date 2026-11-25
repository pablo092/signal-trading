package config

import (
	"os"
	"reflect"
	"testing"
	"time"
)

var riskVars = []string{
	"INTENT_TTL", "RISK_SIGNAL_MAX_AGE", "RISK_MAX_ORDER_NOTIONAL", "RISK_SYMBOL_ALLOWLIST",
	"RISK_STRATEGY_ALLOWLIST", "RISK_ALLOW_EXTENDED_HOURS", "INTENT_RATE_LIMIT_PER_HOUR",
}

// clearRisk unsets every risk variable for the test (restored afterwards).
func clearRisk(t *testing.T) {
	for _, k := range riskVars {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
}

func TestRiskDefaultsAreConservative(t *testing.T) {
	clearRisk(t)
	r, err := loadRisk()
	if err != nil {
		t.Fatal(err)
	}
	if r.IntentTTL != 5*time.Minute || r.SignalMaxAge != 5*time.Minute || r.MaxOrderNotional.String() != "1000" {
		t.Fatalf("unexpected defaults: %+v", r)
	}
	if !reflect.DeepEqual(r.SymbolAllowlist, []string{"SPY", "QQQ", "AAPL", "MSFT"}) || r.AllowExtendedHours || r.IntentRateLimitPerHour != 10 {
		t.Fatalf("unexpected defaults: %+v", r)
	}
}

func TestRiskOverrides(t *testing.T) {
	clearRisk(t)
	t.Setenv("INTENT_TTL", "10m")
	t.Setenv("RISK_MAX_ORDER_NOTIONAL", "250.50")
	t.Setenv("RISK_SYMBOL_ALLOWLIST", " spy, tsla ")
	t.Setenv("INTENT_RATE_LIMIT_PER_HOUR", "3")
	r, err := loadRisk()
	if err != nil {
		t.Fatal(err)
	}
	if r.IntentTTL != 10*time.Minute || r.MaxOrderNotional.String() != "250.5" || r.IntentRateLimitPerHour != 3 {
		t.Fatalf("overrides not applied: %+v", r)
	}
	if !reflect.DeepEqual(r.SymbolAllowlist, []string{"SPY", "TSLA"}) {
		t.Fatalf("allowlist = %v", r.SymbolAllowlist)
	}
}

func TestEmptySymbolAllowlistMeansNothingIsAllowed(t *testing.T) {
	clearRisk(t)
	t.Setenv("RISK_SYMBOL_ALLOWLIST", "")
	r, err := loadRisk()
	if err != nil {
		t.Fatal(err)
	}
	if len(r.SymbolAllowlist) != 0 {
		t.Fatalf("set-but-empty allowlist must be empty, got %v", r.SymbolAllowlist)
	}
}

func TestRiskRejectsOutOfRangeValues(t *testing.T) {
	bad := map[string]string{
		"INTENT_TTL":                 "10s",
		"RISK_SIGNAL_MAX_AGE":        "2h",
		"RISK_MAX_ORDER_NOTIONAL":    "-5",
		"RISK_ALLOW_EXTENDED_HOURS":  "maybe",
		"INTENT_RATE_LIMIT_PER_HOUR": "0",
	}
	for k, v := range bad {
		t.Run(k, func(t *testing.T) {
			clearRisk(t)
			t.Setenv(k, v)
			if _, err := loadRisk(); err == nil {
				t.Fatalf("%s=%q accepted", k, v)
			}
		})
	}
}
