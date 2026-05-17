// Package signalclient implements ports.SignalServicePort via HTTP.
// Calls the Python signal-service REST API to fetch trading signals.
package signalclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/trading-bot/bot-engine/internal/domain/ports"
)

// signalResponse mirrors the JSON response from signal-service POST /api/v1/signals/generate.
type signalResponse struct {
	ID           string    `json:"id"`
	Symbol       string    `json:"symbol"`
	Direction    string    `json:"direction"`
	Strength     string    `json:"strength"`
	Price        float64   `json:"price"`
	Confidence   float64   `json:"confidence"`
	Reason       string    `json:"reason"`
	StrategyName string    `json:"strategy_name"`
	IsActionable bool      `json:"is_actionable"`
	GeneratedAt  time.Time `json:"generated_at"`
}

// Client calls the signal-service over HTTP.
type Client struct {
	baseURL    string
	httpClient *http.Client
	logger     *zap.Logger
}

// New creates a signal service HTTP client.
func New(baseURL string, logger *zap.Logger) *Client {
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
		logger: logger,
	}
}

// GetSignal fetches a trading signal for the given symbol and strategy.
// Implements ports.SignalServicePort.
func (c *Client) GetSignal(ctx context.Context, symbol, strategy string) (*ports.Signal, error) {
	url := fmt.Sprintf("%s/api/v1/signals/generate", c.baseURL)

	body := fmt.Sprintf(
		`{"symbol":%q,"strategy":%q,"timeframe":"1Hour","bars_limit":100}`,
		symbol, strategy,
	)

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, url,
		strings.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("building signal request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling signal service: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("signal service returned HTTP %d", resp.StatusCode)
	}

	var sr signalResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, fmt.Errorf("decoding signal response: %w", err)
	}

	c.logger.Debug("signal_client.received",
		zap.String("symbol", sr.Symbol),
		zap.String("direction", sr.Direction),
		zap.Float64("confidence", sr.Confidence),
	)

	return &ports.Signal{
		ID:           sr.ID,
		Symbol:       sr.Symbol,
		Direction:    sr.Direction,
		Strength:     sr.Strength,
		Price:        sr.Price,
		Confidence:   sr.Confidence,
		Reason:       sr.Reason,
		StrategyName: sr.StrategyName,
		IsActionable: sr.IsActionable,
		GeneratedAt:  sr.GeneratedAt,
	}, nil
}

