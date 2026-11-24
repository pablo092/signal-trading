// Package server builds the bot-engine HTTP router from a single route-scope table.
//
// Every route is declared in Routes with the scope it requires (ADR-003, deny by
// default). Only the explicit public set (health probes, metrics) is unauthenticated.
// TestEveryRouteIsDeclared fails if a route is registered outside this table.
package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"

	"github.com/trading-bot/bot-engine/internal/application/usecases"
	"github.com/trading-bot/bot-engine/internal/domain/ports"
	"github.com/trading-bot/bot-engine/internal/infrastructure/auth"
	"github.com/trading-bot/bot-engine/internal/infrastructure/config"
	httphandlers "github.com/trading-bot/bot-engine/internal/infrastructure/http/handlers"
	"github.com/trading-bot/bot-engine/internal/infrastructure/middleware"
)

// ScopePublic marks the few routes that need no token.
const ScopePublic = "public"

// Route is one entry of the route-scope table.
type Route struct {
	Method  string
	Path    string
	Scope   string
	Handler gin.HandlerFunc
}

// WSServer is the subset of the WebSocket hub the router needs.
type WSServer interface {
	ServeWS(w http.ResponseWriter, r *http.Request, clientID string)
	ConnectedClients() int
}

// Deps are the router's dependencies.
type Deps struct {
	Config     *config.Config
	Tokens     *auth.Store
	PlaceOrder *usecases.PlaceOrderUseCase
	Broker     ports.BrokerPort
	Hub        WSServer
	Registry   *prometheus.Registry
	Logger     *zap.Logger
}

// Routes returns the route-scope table.
func Routes(d Deps) []Route {
	mode, verified := d.Config.TradingMode()
	paper := verified && mode == config.TradingModePaper

	orders := httphandlers.NewOrdersHandler(d.PlaceOrder, d.Config.OrderExecutionEnabled, d.Logger)
	portfolio := httphandlers.NewPortfolioHandler(d.Broker, paper, d.Logger)
	tradingMode := httphandlers.NewTradingModeHandler(httphandlers.TradingModeInfo{
		Mode:                  mode,
		Verified:              verified,
		BrokerHost:            d.Config.BrokerHost(),
		OrderExecutionEnabled: d.Config.OrderExecutionEnabled,
	})

	return []Route{
		// Public: liveness/readiness probes and Prometheus scrape (bind to localhost/private network).
		{http.MethodGet, "/health/live", ScopePublic, func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"status": "alive"})
		}},
		{http.MethodGet, "/health/ready", ScopePublic, func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"status": "ready", "service": "bot-engine", "paper_mode": paper, "ws_clients": d.Hub.ConnectedClients()})
		}},
		{http.MethodGet, "/metrics", ScopePublic, gin.WrapH(promhttp.HandlerFor(d.Registry, promhttp.HandlerOpts{}))},

		// Read-only.
		{http.MethodGet, "/api/v1/trading-mode", auth.ScopePortfolioRead, tradingMode.Get},
		{http.MethodGet, "/api/v1/portfolio/equity", auth.ScopePortfolioRead, portfolio.GetEquity},
		{http.MethodGet, "/api/v1/portfolio/positions", auth.ScopePortfolioRead, portfolio.GetPositions},
		{http.MethodGet, "/ws", auth.ScopePortfolioRead, func(c *gin.Context) {
			// Client ID is server-assigned from the authenticated principal, never from the query string.
			actor := "unknown"
			if p := auth.PrincipalFrom(c); p != nil {
				actor = p.ID
			}
			d.Hub.ServeWS(c.Writer, c.Request, actor+"-"+uuid.NewString())
		}},

		// Write (deprecated, human-only legacy scope; also gated by ORDER_EXECUTION_ENABLED).
		{http.MethodPost, "/api/v1/orders/place", auth.ScopeOrdersPlaceLegacy, orders.PlaceOrder},
	}
}

// NewRouter builds the Gin engine from the route-scope table.
func NewRouter(d Deps) *gin.Engine {
	router := gin.New()
	router.Use(
		middleware.Recovery(d.Logger),
		middleware.RequestID(),
		middleware.StructuredLogger(d.Logger),
		middleware.Tracing("bot-engine"),
		middleware.PrometheusMetrics(d.Registry),
	)
	for _, r := range Routes(d) {
		handlers := []gin.HandlerFunc{r.Handler}
		if r.Scope != ScopePublic {
			handlers = append([]gin.HandlerFunc{auth.Require(d.Tokens, r.Scope, d.Logger)}, handlers...)
		}
		router.Handle(r.Method, r.Path, handlers...)
	}
	return router
}
