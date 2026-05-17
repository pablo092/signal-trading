// Package config loads and validates application configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds all runtime configuration for the bot engine.
type Config struct {
	Environment string
	Port        int
	PaperTrading bool

	// Alpaca API
	AlpacaAPIKey    string
	AlpacaSecretKey string
	AlpacaBaseURL   string

	// Signal service
	SignalServiceURL string
}

// Load reads configuration from environment variables and validates required values.
func Load() (*Config, error) {
	cfg := &Config{
		Environment:      getEnv("ENV", "development"),
		Port:             getEnvInt("PORT", 8080),
		PaperTrading:     getEnvBool("PAPER_TRADING", true),
		AlpacaAPIKey:     os.Getenv("ALPACA_API_KEY"),
		AlpacaSecretKey:  os.Getenv("ALPACA_SECRET_KEY"),
		AlpacaBaseURL:    getEnv("ALPACA_BASE_URL", "https://paper-api.alpaca.markets"),
		SignalServiceURL: getEnv("SIGNAL_SERVICE_URL", "http://signal-service:8000"),
	}

	if cfg.AlpacaAPIKey == "" {
		return nil, fmt.Errorf("ALPACA_API_KEY is required")
	}
	if cfg.AlpacaSecretKey == "" {
		return nil, fmt.Errorf("ALPACA_SECRET_KEY is required")
	}

	return cfg, nil
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

func getEnvBool(key string, defaultValue bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return defaultValue
}
