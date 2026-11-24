package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"

	"github.com/trading-bot/bot-engine/internal/application/usecases"
	"github.com/trading-bot/bot-engine/internal/domain/entities"
	"github.com/trading-bot/bot-engine/internal/domain/ports"
	"github.com/trading-bot/bot-engine/internal/infrastructure/auth"
	"github.com/trading-bot/bot-engine/internal/infrastructure/config"
)

// ── fakes ──

type fakeBroker struct{ submitted int }

func (b *fakeBroker) SubmitOrder(context.Context, *entities.Order) (string, error) {
	b.submitted++
	return "fake-1", nil
}
func (b *fakeBroker) CancelOrder(context.Context, string) error { return nil }
func (b *fakeBroker) GetOrderStatus(context.Context, string) (entities.OrderStatus, float64, error) {
	return entities.OrderStatusFilled, 0, nil
}
func (b *fakeBroker) GetPortfolioEquity(context.Context) (float64, error) { return 100000, nil }
func (b *fakeBroker) GetPositions(context.Context) ([]entities.Position, error) {
	return []entities.Position{{Symbol: "AAPL", Quantity: 1}}, nil
}
func (b *fakeBroker) IsMarketOpen(context.Context) (bool, error) { return true, nil }

type holdSignals struct{}

func (holdSignals) GetSignal(_ context.Context, sym, strat string) (*ports.Signal, error) {
	return &ports.Signal{Symbol: sym, Direction: "HOLD", StrategyName: strat}, nil
}

type nopRepo struct{}

func (nopRepo) Save(context.Context, *entities.Order) error                    { return nil }
func (nopRepo) FindByID(context.Context, interface{}) (*entities.Order, error) { return nil, nil }
func (nopRepo) FindByStatus(context.Context, entities.OrderStatus) ([]*entities.Order, error) {
	return nil, nil
}
func (nopRepo) FindBySymbol(context.Context, string) ([]*entities.Order, error) { return nil, nil }
func (nopRepo) Update(context.Context, *entities.Order) error                   { return nil }

type nopPub struct{}

func (nopPub) Publish(context.Context, ports.DomainEvent) error { return nil }

type fakeHub struct{ served []string }

func (h *fakeHub) ServeWS(w http.ResponseWriter, _ *http.Request, id string) {
	h.served = append(h.served, id)
	w.WriteHeader(http.StatusSwitchingProtocols)
}
func (h *fakeHub) ConnectedClients() int { return len(h.served) }

// ── setup ──

const (
	agentToken = "agent-token-for-tests"
	humanToken = "human-token-for-tests"
	appToken   = "app-token-for-tests"
)

type env struct {
	router *gin.Engine
	broker *fakeBroker
	hub    *fakeHub
}

func newEnv(t *testing.T, execEnabled bool) *env {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{PaperTrading: true, AlpacaBaseURL: "https://paper-api.alpaca.markets", OrderExecutionEnabled: execEnabled}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	store, err := auth.NewStore([]*auth.Principal{
		{ID: "mcp-gateway", Type: auth.ActorAgent, SHA256: auth.HashToken(agentToken), Scopes: []string{auth.ScopeMarketRead, auth.ScopePortfolioRead}},
		{ID: "owner", Type: auth.ActorHuman, SHA256: auth.HashToken(humanToken), Scopes: []string{auth.ScopePortfolioRead, auth.ScopeOrdersPlaceLegacy}},
		{ID: "app", Type: auth.ActorService, SHA256: auth.HashToken(appToken), Scopes: []string{auth.ScopeMarketRead}},
	})
	if err != nil {
		t.Fatal(err)
	}
	b := &fakeBroker{}
	h := &fakeHub{}
	uc := usecases.NewPlaceOrderUseCase(b, nopRepo{}, holdSignals{}, nopPub{}, entities.DefaultRiskRules(), zap.NewNop())
	r := NewRouter(Deps{Config: cfg, Tokens: store, PlaceOrder: uc, Broker: b, Hub: h, Registry: prometheus.NewRegistry(), Logger: zap.NewNop()})
	return &env{router: r, broker: b, hub: h}
}

func (e *env) do(method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	return w
}

// ── tests ──

// Route-table test (ADR-003): every registered route is in the table and every
// non-public route declares a real scope.
func TestEveryRouteIsDeclared(t *testing.T) {
	e := newEnv(t, false)
	table := map[string]string{}
	for _, r := range Routes(Deps{Config: &config.Config{}, Logger: zap.NewNop(), Hub: &fakeHub{}, Registry: prometheus.NewRegistry()}) {
		table[r.Method+" "+r.Path] = r.Scope
		if r.Scope == "" {
			t.Errorf("%s %s has no scope", r.Method, r.Path)
		}
		if r.Scope == ScopePublic && strings.HasPrefix(r.Path, "/api/") {
			t.Errorf("%s %s: /api routes can never be public", r.Method, r.Path)
		}
	}
	for _, ri := range e.router.Routes() {
		if _, ok := table[ri.Method+" "+ri.Path]; !ok {
			t.Errorf("route %s %s registered outside the route-scope table", ri.Method, ri.Path)
		}
	}
}

func TestProtectedRoutesRequireToken(t *testing.T) {
	e := newEnv(t, true)
	for _, rt := range []struct{ m, p string }{
		{"GET", "/api/v1/trading-mode"}, {"GET", "/api/v1/portfolio/equity"},
		{"GET", "/api/v1/portfolio/positions"}, {"GET", "/ws"}, {"POST", "/api/v1/orders/place"},
	} {
		for _, tok := range []string{"", "wrong-token"} {
			if w := e.do(rt.m, rt.p, tok, `{"symbol":"AAPL","strategy":"rsi"}`); w.Code != http.StatusUnauthorized {
				t.Errorf("%s %s token=%q: got %d, want 401", rt.m, rt.p, tok, w.Code)
			}
		}
	}
	if e.broker.submitted != 0 {
		t.Fatal("no order may reach the broker without auth")
	}
}

func TestPublicRoutes(t *testing.T) {
	e := newEnv(t, false)
	for _, p := range []string{"/health/live", "/health/ready", "/metrics"} {
		if w := e.do("GET", p, "", ""); w.Code != http.StatusOK {
			t.Errorf("%s: got %d", p, w.Code)
		}
	}
}

// M1 exit criterion: the agent-gateway token is rejected on every non-read route.
func TestAgentTokenRejectedOnWriteRoutes(t *testing.T) {
	for _, exec := range []bool{false, true} {
		e := newEnv(t, exec)
		for _, r := range Routes(Deps{Config: &config.Config{}, Logger: zap.NewNop(), Hub: &fakeHub{}, Registry: prometheus.NewRegistry()}) {
			if r.Method == http.MethodGet {
				continue
			}
			w := e.do(r.Method, r.Path, agentToken, `{"symbol":"AAPL","strategy":"rsi"}`)
			if w.Code != http.StatusForbidden {
				t.Errorf("exec=%v %s %s with agent token: got %d, want 403", exec, r.Method, r.Path, w.Code)
			}
		}
		if e.broker.submitted != 0 {
			t.Fatal("agent token reached the broker")
		}
	}
}

func TestOrderRouteDisabledByDefault(t *testing.T) {
	e := newEnv(t, false)
	w := e.do("POST", "/api/v1/orders/place", humanToken, `{"symbol":"AAPL","strategy":"rsi"}`)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "execution_disabled") {
		t.Fatalf("got %d %s, want 403 execution_disabled", w.Code, w.Body.String())
	}
	if e.broker.submitted != 0 {
		t.Fatal("order reached the broker while execution is disabled")
	}
}

func TestOrderRouteEnabledForHumanLegacyScope(t *testing.T) {
	e := newEnv(t, true)
	w := e.do("POST", "/api/v1/orders/place", humanToken, `{"symbol":"AAPL","strategy":"rsi"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"skipped":true`) {
		t.Fatalf("got %d %s, want 200 skipped (HOLD signal)", w.Code, w.Body.String())
	}
}

func TestScopeEnforced(t *testing.T) {
	e := newEnv(t, false)
	w := e.do("GET", "/api/v1/portfolio/equity", appToken, "")
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "insufficient_scope") {
		t.Fatalf("got %d %s, want 403 insufficient_scope", w.Code, w.Body.String())
	}
}

func TestReadRoutesWithAgentToken(t *testing.T) {
	e := newEnv(t, false)

	w := e.do("GET", "/api/v1/trading-mode", agentToken, "")
	if w.Code != http.StatusOK {
		t.Fatalf("trading-mode: %d", w.Code)
	}
	var tm map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &tm)
	if tm["mode"] != "paper" || tm["verified"] != true || tm["broker_host"] != "paper-api.alpaca.markets" || tm["order_execution_enabled"] != false {
		t.Fatalf("unexpected trading-mode body: %v", tm)
	}

	w = e.do("GET", "/api/v1/portfolio/equity", agentToken, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"paper_mode":true`) {
		t.Fatalf("equity: %d %s", w.Code, w.Body.String())
	}
	if w = e.do("GET", "/api/v1/portfolio/positions", agentToken, ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "AAPL") {
		t.Fatalf("positions: %d %s", w.Code, w.Body.String())
	}
}

func TestWSClientIDIsServerAssigned(t *testing.T) {
	e := newEnv(t, false)
	e.do("GET", "/ws?client_id=spoofed", agentToken, "")
	if len(e.hub.served) != 1 || !strings.HasPrefix(e.hub.served[0], "mcp-gateway-") || strings.Contains(e.hub.served[0], "spoofed") {
		t.Fatalf("client id must come from the principal: %v", e.hub.served)
	}
}

func TestUnverifiedConfigReportsUnverified(t *testing.T) {
	cfg := &config.Config{PaperTrading: true, AlpacaBaseURL: "https://api.alpaca.markets"}
	_ = cfg.Validate()
	store, _ := auth.NewStore([]*auth.Principal{{ID: "o", Type: auth.ActorHuman, SHA256: auth.HashToken("t"), Scopes: []string{auth.ScopePortfolioRead}}})
	r := NewRouter(Deps{Config: cfg, Tokens: store, Broker: &fakeBroker{}, Hub: &fakeHub{}, Registry: prometheus.NewRegistry(), Logger: zap.NewNop()})
	req := httptest.NewRequest("GET", "/api/v1/portfolio/equity", nil)
	req.Header.Set("Authorization", "Bearer t")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if !strings.Contains(w.Body.String(), `"paper_mode":false`) {
		t.Fatalf("paper_mode must be false when unverified: %s", w.Body.String())
	}
}
