package server

// BL-07: order-path integration tests. Everything is real except the broker
// (fakebroker) and signal-service (an httptest stub speaking its HTTP API):
// router + auth + request IDs → OrdersHandler → PlaceOrderUseCase →
// signalclient (HTTP) → in-memory repo → event bus → broker port.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/trading-bot/bot-engine/internal/application/eventbus"
	"github.com/trading-bot/bot-engine/internal/application/usecases"
	"github.com/trading-bot/bot-engine/internal/domain/entities"
	"github.com/trading-bot/bot-engine/internal/domain/ports"
	"github.com/trading-bot/bot-engine/internal/infrastructure/auditlog"
	"github.com/trading-bot/bot-engine/internal/infrastructure/auth"
	"github.com/trading-bot/bot-engine/internal/infrastructure/broker/fakebroker"
	"github.com/trading-bot/bot-engine/internal/infrastructure/config"
	"github.com/trading-bot/bot-engine/internal/infrastructure/http/apierror"
	"github.com/trading-bot/bot-engine/internal/infrastructure/repository"
	"github.com/trading-bot/bot-engine/internal/infrastructure/signalclient"
)

const engineSignalToken = "engine-to-signal-token"

// rawSecret appears in upstream errors and must never reach a response body.
const rawSecret = "PKSECRET-raw-upstream-detail"

// signalStub is a minimal signal-service: POST /api/v1/signals/generate.
type signalStub struct {
	srv       *httptest.Server
	direction atomic.Value // "BUY", "SELL" or "HOLD"
	status    atomic.Int32 // non-200 makes the call fail
	calls     atomic.Int32

	mu         sync.Mutex
	authHeader string
	requestID  string
}

func newSignalStub(t *testing.T) *signalStub {
	s := &signalStub{}
	s.direction.Store("BUY")
	s.status.Store(http.StatusOK)
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.calls.Add(1)
		s.mu.Lock()
		s.authHeader, s.requestID = r.Header.Get("Authorization"), r.Header.Get("X-Request-ID")
		s.mu.Unlock()
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/signals/generate" {
			http.NotFound(w, r)
			return
		}
		if code := int(s.status.Load()); code != http.StatusOK {
			w.WriteHeader(code)
			_, _ = fmt.Fprintf(w, `{"error":"market_data_unavailable","detail":"%s"}`, rawSecret)
			return
		}
		var req struct{ Symbol, Strategy string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		dir := s.direction.Load().(string)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "sig-" + req.Symbol, "symbol": req.Symbol, "direction": dir, "strength": "STRONG",
			"price": "150.00", "confidence": 0.9, "reason": "test", "strategy_name": req.Strategy,
			"is_actionable": dir != "HOLD", "generated_at": time.Now().UTC().Format(time.RFC3339),
		})
	}))
	t.Cleanup(s.srv.Close)
	return s
}

const adminToken = "admin-token-for-tests"

type orderEnv struct {
	router *gin.Engine
	ks     *usecases.KillSwitchService
	audit  *auditlog.Memory
	broker *fakebroker.Broker
	repo   *repository.InMemoryOrderRepo
	signal *signalStub
	events chan ports.DomainEvent
}

func newOrderEnv(t *testing.T, execEnabled bool) *orderEnv {
	t.Helper()
	return newOrderEnvForced(t, execEnabled, false)
}

func newOrderEnvForced(t *testing.T, execEnabled, forced bool) *orderEnv {
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
		{ID: "oncall", Type: auth.ActorHuman, SHA256: auth.HashToken(adminToken), Scopes: []string{auth.ScopePortfolioRead, auth.ScopeAdminKillswitch}},
	})
	if err != nil {
		t.Fatal(err)
	}

	e := &orderEnv{
		audit:  auditlog.NewMemory(zap.NewNop()),
		broker: fakebroker.New(decimal.NewFromInt(100_000)),
		repo:   repository.NewInMemoryOrderRepo(),
		signal: newSignalStub(t),
		events: make(chan ports.DomainEvent, 64),
	}
	e.broker.SetFillPrice("AAPL", decimal.RequireFromString("150.02"))
	bus := eventbus.New(zap.NewNop())
	bus.Subscribe("OrderPlaced", func(_ context.Context, ev ports.DomainEvent) { e.events <- ev })

	signals := signalclient.New(e.signal.srv.URL, engineSignalToken, zap.NewNop())
	e.ks = usecases.NewKillSwitchService(&auditlog.MemoryKillSwitch{}, e.broker, e.audit, bus, forced, zap.NewNop())
	if err := e.ks.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	uc := usecases.NewPlaceOrderUseCase(e.broker, e.repo, signals, bus, entities.DefaultRiskRules(), zap.NewNop()).
		WithAudit(e.audit).WithKillSwitch(e.ks)
	e.router = NewRouter(Deps{Config: cfg, Tokens: store, PlaceOrder: uc, KillSwitch: e.ks, Broker: e.broker, Hub: &fakeHub{}, Registry: prometheus.NewRegistry(), Logger: zap.NewNop()})
	return e
}

type placeResp struct {
	Skipped    bool    `json:"skipped"`
	SkipReason string  `json:"skip_reason"`
	OrderID    string  `json:"order_id"`
	Symbol     string  `json:"symbol"`
	Side       string  `json:"side"`
	Quantity   float64 `json:"quantity"`
	Status     string  `json:"status"`
}

func (e *orderEnv) place(t *testing.T, token, body string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/orders/place", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	if strings.Contains(w.Body.String(), rawSecret) {
		t.Fatalf("raw upstream detail leaked: %s", w.Body.String())
	}
	return w
}

const aaplBody = `{"symbol":"AAPL","strategy":"rsi"}`

func decodePlace(t *testing.T, w *httptest.ResponseRecorder) placeResp {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var r placeResp
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func decodeEnvelope(t *testing.T, w *httptest.ResponseRecorder, status int, code string) apierror.Body {
	t.Helper()
	var b apierror.Body
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		t.Fatalf("not an error envelope (%d): %s", w.Code, w.Body.String())
	}
	if w.Code != status || b.Error != code || b.RequestID == "" || b.Detail == "" {
		t.Fatalf("want %d %s with detail and request_id, got %d %+v", status, code, w.Code, b)
	}
	return b
}

func (e *orderEnv) onlyOrder(t *testing.T) *entities.Order {
	t.Helper()
	orders, _ := e.repo.FindBySymbol(context.Background(), "AAPL")
	if len(orders) != 1 {
		t.Fatalf("want 1 persisted order, got %d", len(orders))
	}
	return orders[0]
}

// ── happy path ──

func TestOrderPath_BuyIsSubmittedFilledPersistedAndPublished(t *testing.T) {
	e := newOrderEnv(t, true)
	r := decodePlace(t, e.place(t, humanToken, aaplBody))

	if r.Skipped || r.Side != "buy" || r.Status != string(entities.OrderStatusFilled) {
		t.Fatalf("response = %+v", r)
	}
	// 5% of $100k at the signal price $150.00 → 33.333333 shares.
	if r.Quantity != 33.333333 {
		t.Fatalf("quantity = %v", r.Quantity)
	}

	if len(e.broker.Submitted) != 1 {
		t.Fatalf("want 1 submission, got %d", len(e.broker.Submitted))
	}
	sub := e.broker.Submitted[0]
	if sub.ClientOrderID != r.OrderID || sub.Type != entities.OrderTypeMarket || !sub.Qty.Equal(decimal.RequireFromString("33.333333")) {
		t.Fatalf("submission = %+v", sub)
	}

	o := e.onlyOrder(t)
	if o.ID.String() != r.OrderID || o.Status != entities.OrderStatusFilled || o.BrokerID != "fake-1" ||
		o.FilledPrice == nil || !o.FilledPrice.Equal(decimal.RequireFromString("150.02")) {
		t.Fatalf("persisted order = %+v", o)
	}

	select {
	case ev := <-e.events:
		if placed, ok := ev.Payload.(*entities.Order); !ok || placed.ID != o.ID {
			t.Fatalf("event payload = %#v", ev.Payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OrderPlaced was not published")
	}
}

func TestOrderPath_SellSignal(t *testing.T) {
	e := newOrderEnv(t, true)
	e.signal.direction.Store("SELL")
	r := decodePlace(t, e.place(t, humanToken, aaplBody))
	if r.Side != "sell" || e.broker.Submitted[0].Side != entities.OrderSideSell {
		t.Fatalf("response = %+v, submission = %+v", r, e.broker.Submitted)
	}
}

func TestOrderPath_RequestIDAndTokenReachSignalService(t *testing.T) {
	e := newOrderEnv(t, true)
	w := e.place(t, humanToken, aaplBody, "X-Request-ID", "trace-bl07-1")
	decodePlace(t, w)
	e.signal.mu.Lock()
	defer e.signal.mu.Unlock()
	if e.signal.requestID != "trace-bl07-1" || w.Header().Get("X-Request-ID") != "trace-bl07-1" {
		t.Fatalf("request ID not propagated: upstream=%q response=%q", e.signal.requestID, w.Header().Get("X-Request-ID"))
	}
	if e.signal.authHeader != "Bearer "+engineSignalToken {
		t.Fatal("engine did not authenticate to signal-service with its own token")
	}
}

// ── nothing is placed ──

func TestOrderPath_HoldSignalPlacesNothing(t *testing.T) {
	e := newOrderEnv(t, true)
	e.signal.direction.Store("HOLD")
	r := decodePlace(t, e.place(t, humanToken, aaplBody))
	if !r.Skipped || r.OrderID != "" || len(e.broker.Submitted) != 0 {
		t.Fatalf("response = %+v, submissions = %d", r, len(e.broker.Submitted))
	}
	if orders, _ := e.repo.FindBySymbol(context.Background(), "AAPL"); len(orders) != 0 {
		t.Fatalf("persisted %d orders for a HOLD", len(orders))
	}
}

func TestOrderPath_DailyLossLimitPlacesNothing(t *testing.T) {
	e := newOrderEnv(t, true)
	e.broker.SetAccount(ports.Account{Equity: decimal.NewFromInt(97_000), LastEquity: decimal.NewFromInt(100_000), BuyingPower: decimal.NewFromInt(97_000)})
	r := decodePlace(t, e.place(t, humanToken, aaplBody))
	if !r.Skipped || !strings.Contains(r.SkipReason, "daily loss") || len(e.broker.Submitted) != 0 {
		t.Fatalf("response = %+v", r)
	}
}

func TestOrderPath_ExecutionDisabledNeverReachesUpstreams(t *testing.T) {
	e := newOrderEnv(t, false)
	decodeEnvelope(t, e.place(t, humanToken, aaplBody), http.StatusForbidden, apierror.CodeExecutionDisabled)
	if e.signal.calls.Load() != 0 || len(e.broker.Submitted) != 0 {
		t.Fatal("disabled execution must not call signal-service or the broker")
	}
}

func TestOrderPath_OnlyTheOrdersScopeCanPlace(t *testing.T) {
	e := newOrderEnv(t, true)
	decodeEnvelope(t, e.place(t, "", aaplBody), http.StatusUnauthorized, apierror.CodeUnauthorized)
	decodeEnvelope(t, e.place(t, "wrong-token", aaplBody), http.StatusUnauthorized, apierror.CodeUnauthorized)
	decodeEnvelope(t, e.place(t, agentToken, aaplBody), http.StatusForbidden, apierror.CodeInsufficientScope)
	decodeEnvelope(t, e.place(t, appToken, aaplBody), http.StatusForbidden, apierror.CodeInsufficientScope)
	if e.signal.calls.Load() != 0 || len(e.broker.Submitted) != 0 {
		t.Fatal("rejected callers must not reach upstreams")
	}
}

func TestOrderPath_InvalidBody(t *testing.T) {
	e := newOrderEnv(t, true)
	for _, body := range []string{``, `{`, `{"symbol":"AAPL"}`, `{"symbol":1,"strategy":"rsi"}`} {
		decodeEnvelope(t, e.place(t, humanToken, body), http.StatusBadRequest, apierror.CodeInvalidRequest)
	}
	if e.signal.calls.Load() != 0 {
		t.Fatal("invalid bodies must not reach signal-service")
	}
}

// ── failures: status, code, and what is left behind ──

func TestOrderPath_SignalServiceDown(t *testing.T) {
	e := newOrderEnv(t, true)
	e.signal.status.Store(http.StatusServiceUnavailable)
	decodeEnvelope(t, e.place(t, humanToken, aaplBody), http.StatusBadGateway, apierror.CodeSignalUnavailable)
	if len(e.broker.Submitted) != 0 {
		t.Fatal("no signal, no order")
	}
}

func TestOrderPath_BrokerAccountUnavailable(t *testing.T) {
	e := newOrderEnv(t, true)
	e.broker.FailAccount = fmt.Errorf("alpaca get account: %w: %s", ports.ErrUnavailable, rawSecret)
	decodeEnvelope(t, e.place(t, humanToken, aaplBody), http.StatusBadGateway, apierror.CodeBrokerUnavailable)
	if len(e.broker.Submitted) != 0 {
		t.Fatal("fail closed: no account, no order")
	}
}

func TestOrderPath_InsufficientBuyingPowerIsRejected(t *testing.T) {
	e := newOrderEnv(t, true)
	e.broker.SetAccount(ports.Account{Equity: decimal.NewFromInt(100_000), LastEquity: decimal.NewFromInt(100_000), BuyingPower: decimal.NewFromInt(100)})
	b := decodeEnvelope(t, e.place(t, humanToken, aaplBody), http.StatusUnprocessableEntity, apierror.CodeOrderRejected)
	if !strings.Contains(b.Detail, "insufficient buying power") {
		t.Fatalf("detail = %q", b.Detail)
	}
	if o := e.onlyOrder(t); o.Status != entities.OrderStatusRejected {
		t.Fatalf("persisted status = %s", o.Status)
	}
}

func TestOrderPath_BrokerRejection(t *testing.T) {
	e := newOrderEnv(t, true)
	e.broker.FailSubmit = fmt.Errorf("alpaca place order: %w (HTTP 422: %s)", ports.ErrRejected, rawSecret)
	decodeEnvelope(t, e.place(t, humanToken, aaplBody), http.StatusUnprocessableEntity, apierror.CodeOrderRejected)
	if o := e.onlyOrder(t); o.Status != entities.OrderStatusRejected {
		t.Fatalf("persisted status = %s", o.Status)
	}
}

func TestOrderPath_TimeoutButBrokerHasOrderIsReconciled(t *testing.T) {
	e := newOrderEnv(t, true)
	e.broker.SubmitThenFail = fmt.Errorf("alpaca place order: %w: %w", ports.ErrUnavailable, context.DeadlineExceeded)
	r := decodePlace(t, e.place(t, humanToken, aaplBody))
	if r.Status != string(entities.OrderStatusFilled) || len(e.broker.Submitted) != 1 {
		t.Fatalf("response = %+v, submissions = %d", r, len(e.broker.Submitted))
	}
	if o := e.onlyOrder(t); o.BrokerID != "fake-1" || o.Status != entities.OrderStatusFilled {
		t.Fatalf("persisted order = %+v", o)
	}
}

func TestOrderPath_TimeoutAndOrderNotAtBrokerIsNotPlaced(t *testing.T) {
	e := newOrderEnv(t, true)
	e.broker.FailSubmit = fmt.Errorf("alpaca place order: %w", ports.ErrUnavailable)
	decodeEnvelope(t, e.place(t, humanToken, aaplBody), http.StatusBadGateway, apierror.CodeBrokerUnavailable)
	if o := e.onlyOrder(t); o.Status != entities.OrderStatusRejected {
		t.Fatalf("persisted status = %s", o.Status)
	}
}

func TestOrderPath_UnknownOutcomeIsReportedAndLeftPending(t *testing.T) {
	e := newOrderEnv(t, true)
	e.broker.FailSubmit = fmt.Errorf("alpaca place order: %w", ports.ErrUnavailable)
	e.broker.FailGet = fmt.Errorf("alpaca get order by client ID: %w", ports.ErrUnavailable)
	b := decodeEnvelope(t, e.place(t, humanToken, aaplBody), http.StatusBadGateway, apierror.CodeOutcomeUnknown)
	if !strings.Contains(b.Detail, "do not retry") {
		t.Fatalf("detail must warn against retrying: %q", b.Detail)
	}
	if o := e.onlyOrder(t); o.Status != entities.OrderStatusPending {
		t.Fatalf("persisted status = %s, want pending (needs reconciliation)", o.Status)
	}
}

// ── concurrency ──

func TestOrderPath_ConcurrentOrdersGetDistinctClientOrderIDs(t *testing.T) {
	e := newOrderEnv(t, true)
	const n = 20
	var wg sync.WaitGroup
	codes := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/orders/place", strings.NewReader(aaplBody))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+humanToken)
			w := httptest.NewRecorder()
			e.router.ServeHTTP(w, req)
			codes[i] = w.Code
		}(i)
	}
	wg.Wait()
	for i, c := range codes {
		if c != http.StatusOK {
			t.Fatalf("request %d: status %d", i, c)
		}
	}
	seen := map[string]bool{}
	for _, o := range e.broker.Orders() {
		if seen[o.ClientOrderID] {
			t.Fatalf("duplicate client order ID %s", o.ClientOrderID)
		}
		seen[o.ClientOrderID] = true
	}
	orders, _ := e.repo.FindBySymbol(context.Background(), "AAPL")
	if len(seen) != n || len(orders) != n {
		t.Fatalf("want %d broker orders and %d persisted, got %d and %d", n, n, len(seen), len(orders))
	}
	for _, o := range orders {
		if !seen[o.ID.String()] {
			t.Fatalf("persisted order %s has no broker order", o.ID)
		}
	}
}

// ── kill switch (BL-19) and audit trail (BL-14) ──

func (e *orderEnv) killSwitch(t *testing.T, method, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/api/v1/admin/kill-switch", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	return w
}

func (e *orderEnv) auditActions() []string {
	var out []string
	for _, ev := range e.audit.Events() {
		out = append(out, ev.Action)
	}
	return out
}

func TestKillSwitch_EngageBlocksOrdersAndReleaseResumes(t *testing.T) {
	e := newOrderEnv(t, true)
	if w := e.killSwitch(t, "GET", humanToken, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"engaged":false`) {
		t.Fatalf("initial state: %d %s", w.Code, w.Body.String())
	}

	w := e.killSwitch(t, "POST", adminToken, `{"engaged":true,"reason":"runaway strategy"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"cancel_all":"ok"`) || !strings.Contains(w.Body.String(), `"updated_by":"oncall"`) {
		t.Fatalf("engage: %d %s", w.Code, w.Body.String())
	}
	decodeEnvelope(t, e.place(t, humanToken, aaplBody), http.StatusConflict, apierror.CodeKillSwitchEngaged)
	if e.signal.calls.Load() != 0 || len(e.broker.Submitted) != 0 {
		t.Fatal("a halted engine must not call upstreams")
	}

	if w := e.killSwitch(t, "POST", adminToken, `{"engaged":false,"reason":"fixed"}`); w.Code != 200 {
		t.Fatalf("release: %d %s", w.Code, w.Body.String())
	}
	decodePlace(t, e.place(t, humanToken, aaplBody))

	got := strings.Join(e.auditActions(), ",")
	if !strings.HasPrefix(got, "kill_switch.engaged,order.blocked,kill_switch.released,order.submit_requested,order.submitted") {
		t.Fatalf("audit trail = %s", got)
	}
	for _, ev := range e.audit.Events() {
		if ev.Action == "kill_switch.engaged" && (ev.Actor.ID != "oncall" || ev.RequestID == "") {
			t.Fatalf("engage event lacks actor or request ID: %+v", ev)
		}
	}
}

func TestKillSwitch_OnlyAdminScopeMayChangeIt(t *testing.T) {
	e := newOrderEnv(t, true)
	body := `{"engaged":true,"reason":"x"}`
	decodeEnvelope(t, e.killSwitch(t, "POST", "", body), http.StatusUnauthorized, apierror.CodeUnauthorized)
	for _, tok := range []string{humanToken, agentToken, appToken} {
		decodeEnvelope(t, e.killSwitch(t, "POST", tok, body), http.StatusForbidden, apierror.CodeInsufficientScope)
	}
	if engaged, _ := e.ks.Engaged(context.Background()); engaged {
		t.Fatal("an unauthorized call engaged the switch")
	}
}

func TestKillSwitch_InvalidRequests(t *testing.T) {
	e := newOrderEnv(t, true)
	for _, body := range []string{``, `{}`, `{"engaged":true}`, `{"engaged":true,"reason":"   "}`, `{"engaged":"yes","reason":"x"}`,
		`{"engaged":true,"reason":"` + strings.Repeat("x", 201) + `"}`} {
		decodeEnvelope(t, e.killSwitch(t, "POST", adminToken, body), http.StatusBadRequest, apierror.CodeInvalidRequest)
	}
}

func TestKillSwitch_EnvOverrideCannotBeReleased(t *testing.T) {
	e := newOrderEnvForced(t, true, true)
	w := e.killSwitch(t, "GET", humanToken, "")
	if !strings.Contains(w.Body.String(), `"engaged":true`) || !strings.Contains(w.Body.String(), `"forced":true`) {
		t.Fatalf("state: %s", w.Body.String())
	}
	decodeEnvelope(t, e.killSwitch(t, "POST", adminToken, `{"engaged":false,"reason":"try"}`), http.StatusConflict, apierror.CodeKillSwitchForced)
	decodeEnvelope(t, e.place(t, humanToken, aaplBody), http.StatusConflict, apierror.CodeKillSwitchEngaged)
}

func TestKillSwitch_EngageCancelsOpenOrdersEvenIfBrokerFails(t *testing.T) {
	e := newOrderEnv(t, true)
	e.broker.FailCancel = fmt.Errorf("alpaca cancel all: %w: %s", ports.ErrUnavailable, rawSecret)
	w := e.killSwitch(t, "POST", adminToken, `{"engaged":true,"reason":"incident"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"cancel_all":"failed"`) || strings.Contains(w.Body.String(), rawSecret) {
		t.Fatalf("engage with failing broker: %d %s", w.Code, w.Body.String())
	}
	if engaged, _ := e.ks.Engaged(context.Background()); !engaged {
		t.Fatal("the switch must hold even when cancel-all fails")
	}
}
