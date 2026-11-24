package config

import "testing"

func TestValidatePaperGuardMatrix(t *testing.T) {
	cases := []struct {
		name    string
		paper   bool
		url     string
		liveEnv string
		ok      bool
	}{
		{"paper host + paper flag", true, "https://paper-api.alpaca.markets", "", true},
		{"paper host with path", true, "https://paper-api.alpaca.markets/v2", "", true},
		{"paper host uppercase", true, "https://PAPER-API.alpaca.markets", "", true},
		{"live host", true, "https://api.alpaca.markets", "", false},
		{"paper flag false", false, "https://paper-api.alpaca.markets", "", false},
		{"lookalike suffix", true, "https://paper-api.alpaca.markets.evil.com", "", false},
		{"lookalike prefix", true, "https://evilpaper-api.alpaca.markets", "", false},
		{"http scheme", true, "http://paper-api.alpaca.markets", "", false},
		{"explicit port", true, "https://paper-api.alpaca.markets:8443", "", false},
		{"userinfo", true, "https://u:p@paper-api.alpaca.markets", "", false},
		{"garbage url", true, "::not a url", "", false},
		{"TRADING_MODE=live", true, "https://paper-api.alpaca.markets", "live", false},
		{"TRADING_MODE=paper", true, "https://paper-api.alpaca.markets", "paper", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TRADING_MODE", tc.liveEnv)
			c := &Config{PaperTrading: tc.paper, AlpacaBaseURL: tc.url}
			err := c.Validate()
			if (err == nil) != tc.ok {
				t.Fatalf("Validate() err=%v, want ok=%v", err, tc.ok)
			}
			mode, verified := c.TradingMode()
			if tc.ok && (mode != TradingModePaper || !verified) {
				t.Fatalf("mode=%q verified=%v, want paper/true", mode, verified)
			}
			if !tc.ok && verified {
				t.Fatalf("mode must not be verified after a failed Validate")
			}
		})
	}
}

func setBaseEnv(t *testing.T) {
	t.Setenv("ALPACA_API_KEY", "test-key")
	t.Setenv("ALPACA_SECRET_KEY", "test-secret")
	t.Setenv("ALPACA_BASE_URL", "")
	t.Setenv("PAPER_TRADING", "")
	t.Setenv("ORDER_EXECUTION_ENABLED", "")
	t.Setenv("TRADING_MODE", "")
}

func TestLoadDefaultsAreSafe(t *testing.T) {
	setBaseEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.OrderExecutionEnabled {
		t.Fatal("order execution must be disabled by default")
	}
	if !cfg.PaperTrading || cfg.BrokerHost() != PaperBrokerHost {
		t.Fatalf("defaults must be paper: paper=%v host=%q", cfg.PaperTrading, cfg.BrokerHost())
	}
}

func TestLoadRejectsLiveHost(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("ALPACA_BASE_URL", "https://api.alpaca.markets")
	if _, err := Load(); err == nil {
		t.Fatal("Load must fail on the live host")
	}
}

func TestLoadRejectsMalformedBooleans(t *testing.T) {
	for _, key := range []string{"PAPER_TRADING", "ORDER_EXECUTION_ENABLED"} {
		t.Run(key, func(t *testing.T) {
			setBaseEnv(t)
			t.Setenv(key, "ture")
			if _, err := Load(); err == nil {
				t.Fatalf("Load must fail on malformed %s", key)
			}
		})
	}
}

func TestLoadExecutionFlag(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("ORDER_EXECUTION_ENABLED", "true")
	cfg, err := Load()
	if err != nil || !cfg.OrderExecutionEnabled {
		t.Fatalf("explicit opt-in must enable execution: err=%v", err)
	}
}
