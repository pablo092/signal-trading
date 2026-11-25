// Package config loads and validates application configuration from environment variables.
package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
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
	SignalServiceURL   string
	SignalServiceToken string // bot-engine's own token for signal-service (market:read)

	// AuthTokensJSON / AuthTokensFile hold the scoped service-token table (ADR-003).
	AuthTokensJSON string
	AuthTokensFile string

	// DatabaseURL enables Postgres persistence (orders, audit_events, kill
	// switch). Empty = in-memory stores, audit to logs only (BL-14).
	DatabaseURL string

	// KillSwitchForced (KILL_SWITCH=true) engages the kill switch at startup and
	// keeps it engaged: it cannot be released through the API (ADR-005 §5.7).
	KillSwitchForced bool

	// WSAllowedOrigins is the browser origin allowlist for /ws (BL-15).
	// Requests without an Origin header (non-browser clients) are allowed;
	// they still need a token.
	WSAllowedOrigins []string

	// Risk holds the order-intent settings (ADR-005 §5.3–5.5, M3).
	Risk RiskConfig

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

	killSwitch, err := getEnvBoolStrict("KILL_SWITCH", false)
	if err != nil {
		return nil, err
	}

	risk, err := loadRisk()
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		Risk:                  risk,
		Environment:           getEnv("ENV", "development"),
		Port:                  getEnvInt("PORT", 8080),
		PaperTrading:          paper,
		OrderExecutionEnabled: execEnabled,
		AlpacaAPIKey:          os.Getenv("ALPACA_API_KEY"),
		AlpacaSecretKey:       os.Getenv("ALPACA_SECRET_KEY"),
		AlpacaBaseURL:         getEnv("ALPACA_BASE_URL", "https://"+PaperBrokerHost),
		SignalServiceURL:      getEnv("SIGNAL_SERVICE_URL", "http://signal-service:8000"),
		SignalServiceToken:    os.Getenv("SIGNAL_SERVICE_TOKEN"),
		AuthTokensJSON:        os.Getenv("AUTH_TOKENS"),
		AuthTokensFile:        os.Getenv("AUTH_TOKENS_FILE"),
		DatabaseURL:           os.Getenv("DATABASE_URL"),
		KillSwitchForced:      killSwitch,
		WSAllowedOrigins:      splitList(os.Getenv("WS_ALLOWED_ORIGINS")),
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

// RiskConfig holds the order-intent and risk-engine settings. Every value is
// parsed strictly: an unparseable or out-of-range value is fatal.
type RiskConfig struct {
	IntentTTL              time.Duration   // INTENT_TTL: approval deadline after creation
	SignalMaxAge           time.Duration   // RISK_SIGNAL_MAX_AGE: oldest signal an intent may use
	MaxOrderNotional       decimal.Decimal // RISK_MAX_ORDER_NOTIONAL: absolute cap per order, in account currency
	SymbolAllowlist        []string        // RISK_SYMBOL_ALLOWLIST: set but empty = reject every symbol
	StrategyAllowlist      []string        // RISK_STRATEGY_ALLOWLIST: set but empty = reject every strategy
	AllowExtendedHours     bool            // RISK_ALLOW_EXTENDED_HOURS: skip the market-open check
	IntentRateLimitPerHour int             // INTENT_RATE_LIMIT_PER_HOUR: creations per actor
}

// DefaultRiskConfig is the conservative paper-trading default.
func DefaultRiskConfig() RiskConfig {
	return RiskConfig{
		IntentTTL:              5 * time.Minute,
		SignalMaxAge:           5 * time.Minute,
		MaxOrderNotional:       decimal.NewFromInt(1000),
		SymbolAllowlist:        []string{"SPY", "QQQ", "AAPL", "MSFT"},
		StrategyAllowlist:      []string{"rsi", "ma_cross"},
		IntentRateLimitPerHour: 10,
	}
}

func loadRisk() (RiskConfig, error) {
	r := DefaultRiskConfig()
	var err error
	if r.IntentTTL, err = getEnvDuration("INTENT_TTL", r.IntentTTL, time.Minute, time.Hour); err != nil {
		return r, err
	}
	if r.SignalMaxAge, err = getEnvDuration("RISK_SIGNAL_MAX_AGE", r.SignalMaxAge, 30*time.Second, time.Hour); err != nil {
		return r, err
	}
	if v := strings.TrimSpace(os.Getenv("RISK_MAX_ORDER_NOTIONAL")); v != "" {
		d, perr := decimal.NewFromString(v)
		if perr != nil || !d.IsPositive() {
			return r, fmt.Errorf("RISK_MAX_ORDER_NOTIONAL must be a positive number, got %q", v)
		}
		r.MaxOrderNotional = d
	}
	// A variable that is set (even to "") replaces the default list.
	if v, ok := os.LookupEnv("RISK_SYMBOL_ALLOWLIST"); ok {
		r.SymbolAllowlist = upper(splitList(v))
	}
	if v, ok := os.LookupEnv("RISK_STRATEGY_ALLOWLIST"); ok {
		r.StrategyAllowlist = splitList(v)
	}
	if r.AllowExtendedHours, err = getEnvBoolStrict("RISK_ALLOW_EXTENDED_HOURS", false); err != nil {
		return r, err
	}
	if v := strings.TrimSpace(os.Getenv("INTENT_RATE_LIMIT_PER_HOUR")); v != "" {
		n, perr := strconv.Atoi(v)
		if perr != nil || n < 1 || n > 1000 {
			return r, fmt.Errorf("INTENT_RATE_LIMIT_PER_HOUR must be an integer in [1, 1000], got %q", v)
		}
		r.IntentRateLimitPerHour = n
	}
	return r, nil
}

func getEnvDuration(key string, def, lo, hi time.Duration) (time.Duration, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < lo || d > hi {
		return 0, fmt.Errorf("%s must be a duration in [%s, %s], got %q", key, lo, hi, v)
	}
	return d, nil
}

func upper(in []string) []string {
	for i, s := range in {
		in[i] = strings.ToUpper(s)
	}
	return in
}

// splitList parses a comma-separated list, dropping empty items.
func splitList(v string) []string {
	var out []string
	for _, item := range strings.Split(v, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}
