package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/trading-bot/bot-engine/internal/contracttest"
	"github.com/trading-bot/bot-engine/internal/domain/entities"
)

// BL-16 producer tests: bot-engine HTTP responses conform to contracts/.
func TestHTTPResponsesMatchTheContract(t *testing.T) {
	e := newOrderEnv(t, true)
	e.broker.SetPositions([]entities.Position{
		{Symbol: "AAPL", Quantity: decimal.NewFromInt(2), AverageEntryPrice: decimal.RequireFromString("180.1"), CurrentPrice: decimal.RequireFromString("189.42"), UnrealizedPnL: decimal.RequireFromString("18.64")},
		{Symbol: "TSLA", Quantity: decimal.NewFromInt(-3), AverageEntryPrice: decimal.NewFromInt(250)},
	})
	get := func(path, token string) []byte {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		e.router.ServeHTTP(w, req)
		return w.Body.Bytes()
	}

	contracttest.Validate(t, "portfolio-equity.schema.json", get("/api/v1/portfolio/equity", humanToken))
	contracttest.Validate(t, "portfolio-positions.schema.json", get("/api/v1/portfolio/positions", humanToken))
	contracttest.Validate(t, "kill-switch.schema.json", get("/api/v1/admin/kill-switch", humanToken))
	contracttest.Validate(t, "kill-switch.schema.json", e.killSwitch(t, "POST", adminToken, `{"engaged":true,"reason":"contract"}`).Body.Bytes())

	for name, body := range map[string][]byte{
		"401":                get("/api/v1/portfolio/equity", ""),
		"403 required_scope": get("/api/v1/portfolio/equity", appToken),
		"404":                get("/api/v1/nope", humanToken),
		"409 kill switch":    e.place(t, humanToken, aaplBody).Body.Bytes(),
	} {
		t.Run(name, func(t *testing.T) { contracttest.Validate(t, "error.schema.json", body) })
	}
}
