// Package config loads and validates application configuration from environment variables.
package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// PaperBrokerHost is the only Alpaca trading host the engine accepts (ADR-005 §5.1).
const PaperBrokerHost = "paper-api.alpaca.markets"

// TradingModePaper is the only trading mode supported until a live-trading ADR exists.
const TradingModePaper = "paper"

// Config holds all runtime configuration for the bot engine.
type Config struct {
	Environment  string
	Port         int
	PaperTrading bool

	// OrderExecutionEnabled gates every order route (ADR-005 §5.2). Default false.
	OrderExecutionEnabled bool

	// Alpaca API
	AlpacaAPIKey    string
	AlpacaSecretKey string
	AlpacaBaseURL   string

	// Signal service
	SignalServiceURL string

	// AuthTokensJSON / AuthTokensFile hold the scoped service-token table (ADR-003).
	AuthTokensJSON string
	AuthTokensFile string

	// verified is set by Validate once the paper-mode guard has passed.
	verified bool
}

// Load reads configuration from environment variables and validates it.
// Any invalid or unsafe configuration is fatal: the engine fails closed.
func Load() (*Config, error) {
	paper, err := getEnvBoolStrict("PAPER_TRADING", true)
	if err != nil {
		return nil, err
	}
	execEnabled, err := getEnvBoolStrict("ORDER_EXECUTION_ENABLED", false)
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		Environment:           getEnv("ENV", "development"),
		Port:                  getEnvInt("PORT", 8080),
		PaperTrading:          paper,
		OrderExecutionEnabled: execEnabled,
		AlpacaAPIKey:          os.Getenv("ALPACA_API_KEY"),
		AlpacaSecretKey:       os.Getenv("ALPACA_SECRET_KEY"),
		AlpacaBaseURL:         getEnv("ALPACA_BASE_URL", "https://"+PaperBrokerHost),
		SignalServiceURL:      getEnv("SIGNAL_SERVICE_URL", "http://signal-service:8000"),
		AuthTokensJSON:        os.Getenv("AUTH_TOKENS"),
		AuthTokensFile:        os.Getenv("AUTH_TOKENS_FILE"),
	}

	if cfg.AlpacaAPIKey == "" {
		return nil, fmt.Errorf("ALPACA_API_KEY is required")
	}
	if cfg.AlpacaSecretKey == "" {
		return nil, fmt.Errorf("ALPACA_SECRET_KEY is required")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Validate enforces the paper-mode guard (ADR-005 §5.1): PAPER_TRADING must be true
// AND the broker host must be the Alpaca paper host. Live trading is not supported.
func (c *Config) Validate() error {
	c.verified = false

	if !c.PaperTrading {
		return fmt.Errorf("PAPER_TRADING=false is not supported: live trading requires a dedicated ADR (ADR-005 §5.1)")
	}
	if strings.EqualFold(os.Getenv("TRADING_MODE"), "live") {
		return fmt.Errorf("TRADING_MODE=live is not supported: live trading requires a dedicated ADR (ADR-005 §5.1)")
	}

	u, err := url.Parse(c.AlpacaBaseURL)
	if err != nil {
		return fmt.Errorf("ALPACA_BASE_URL is not a valid URL")
	}
	if u.Scheme != "https" {
		return fmt.Errorf("ALPACA_BASE_URL must use https")
	}
	if !strings.EqualFold(u.Hostname(), PaperBrokerHost) || u.Port() != "" {
		return fmt.Errorf("ALPACA_BASE_URL host %q is not the paper host %q: refusing to start", u.Host, PaperBrokerHost)
	}
	if u.User != nil {
		return fmt.Errorf("ALPACA_BASE_URL must not contain credentials")
	}

	c.verified = true
	return nil
}

// TradingMode reports the verified mode. It is only "paper" after Validate succeeded.
func (c *Config) TradingMode() (mode string, verified bool) {
	if c.verified {
		return TradingModePaper, true
	}
	return "unverified", false
}

// BrokerHost returns the host part of ALPACA_BASE_URL (never credentials).
func (c *Config) BrokerHost() string {
	u, err := url.Parse(c.AlpacaBaseURL)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func getEnv(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return defaultValue
}

// getEnvBoolStrict rejects unparseable values instead of silently using the default,
// so a typo such as PAPER_TRADING=ture cannot change the safety posture.
func getEnvBoolStrict(key string, defaultValue bool) (bool, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return defaultValue, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean, got %q", key, v)
	}
	return b, nil
}
