package server

// BL-07, ported to order intents in M4: order-path integration tests. Everything
// is real except the broker (fakebroker) and signal-service (an httptest stub
// speaking its HTTP API): router + auth + request IDs → IntentsHandler →
// IntentService + RiskEngine → signalclient (HTTP) → in-memory repos → event
// bus → broker port. The legacy POST /api/v1/orders/place route is gone.

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
	keys   atomic.Int32
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
		{ID: "mcp-gateway", Type: auth.ActorAgent, SHA256: auth.HashToken(agentToken), Scopes: []string{auth.ScopeMarketRead, auth.ScopePortfolioRead, auth.ScopeIntentCreate}},
		{ID: "owner", Type: auth.ActorHuman, SHA256: auth.HashToken(humanToken), Scopes: []string{auth.ScopePortfolioRead, auth.ScopeIntentCreate, auth.ScopeIntentApprove}},
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
	bus.Subscribe(usecases.EventOrderPlaced, func(_ context.Context, ev ports.DomainEvent) { e.events <- ev })

	e.ks = usecases.NewKillSwitchService(&auditlog.MemoryKillSwitch{}, e.broker, e.audit, bus, forced, zap.NewNop())
	if err := e.ks.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	policy := usecases.RiskPolicy{
		Rules: entities.DefaultRiskRules(), SignalMaxAge: 5 * time.Minute, MaxOrderNotional: decimal.NewFromInt(1000),
		SymbolAllowlist: []string{"AAPL"}, StrategyAllowlist: []string{"rsi"},
	}
	svc := usecases.NewIntentService(usecases.IntentDeps{
		Intents: repository.NewInMemoryIntentRepo(), Orders: e.repo,
		Signals: signalclient.New(e.signal.srv.URL, engineSignalToken, zap.NewNop()), Broker: e.broker,
		Risk: usecases.NewRiskEngine(policy, e.broker, e.ks), KillSwitch: e.ks, Audit: e.audit, Publisher: bus,
		Policy: usecases.IntentPolicy{TTL: 5 * time.Minute, RateLimitPerHour: 1000, ExecutionEnabled: execEnabled},
		Logger: zap.NewNop(),
	})
	e.router = NewRouter(Deps{Config: cfg, Tokens: store, Intents: svc, KillSwitch: e.ks, Broker: e.broker, Hub: &fakeHub{}, Registry: prometheus.NewRegistry(), Logger: zap.NewNop()})
	return e
}

func (e *orderEnv) send(t *testing.T, method, path, token, body string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
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

// propose creates an intent with a fresh Idempotency-Key.
func (e *orderEnv) propose(t *testing.T, token, body string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	key := fmt.Sprintf("key-%08d", e.keys.Add(1))
	return e.send(t, http.MethodPost, "/api/v1/order-intents", token, body, append([]string{"Idempotency-Key", key}, headers...)...)
}

func (e *orderEnv) approve(t *testing.T, id string) *httptest.ResponseRecorder {
	t.Helper()
	return e.send(t, http.MethodPost, "/api/v1/order-intents/"+id+"/approve", humanToken, `{}`)
}

const aaplBody = `{"symbol":"AAPL","strategy":"rsi"}`

type intentResp struct {
	ID            string `json:"id"`
	Status        string `json:"status"`
	Side          string `json:"side"`
	Qty           float64
	RejectionCode string `json:"rejection_code"`
	Risk          struct {
		Checks []struct {
			Name   string `json:"name"`
			Passed bool   `json:"passed"`
			Detail string `json:"detail"`
		} `json:"checks"`
	} `json:"risk"`
	Order *struct {
		ID       string `json:"id"`
		BrokerID string `json:"broker_id"`
		Status   string `json:"status"`
	} `json:"order"`
}

func decodeIntentResp(t *testing.T, w *httptest.ResponseRecorder, status int) intentResp {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status %d, want %d: %s", w.Code, status, w.Body.String())
	}
	var r intentResp
	if err := json.Unmarshal(w.Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func (r intentResp) failed() []string {
	var out []string
	for _, c := range r.Risk.Checks {
		if !c.Passed {
			out = append(out, c.Name)
		}
	}
	return out
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

// proposeAndApprove runs the happy path up to the approval response.
func (e *orderEnv) proposeAndApprove(t *testing.T) (intentResp, *httptest.ResponseRecorder) {
	t.Helper()
	created := decodeIntentResp(t, e.propose(t, agentToken, aaplBody), http.StatusCreated)
	if created.Status != "pending_approval" {
		t.Fatalf("created = %+v (failed checks %v)", created, created.failed())
	}
	return created, e.approve(t, created.ID)
}

// ── happy path ──

func TestOrderPath_BuyIsSubmittedFilledPersistedAndPublished(t *testing.T) {
	e := newOrderEnv(t, true)
	created, w := e.proposeAndApprove(t)
	r := decodeIntentResp(t, w, http.StatusOK)
	if r.Status != "filled" || r.Side != "buy" || r.Order == nil || r.Order.Status != "filled" {
		t.Fatalf("response = %+v", r)
	}
	// min(5% of $100k, $1000 cap) at the signal price $150.00 → 6.666666 shares.
	if r.Qty != 6.666666 {
		t.Fatalf("quantity = %v", r.Qty)
	}

	if len(e.broker.Submitted) != 1 {
		t.Fatalf("want 1 submission, got %d", len(e.broker.Submitted))
	}
	sub := e.broker.Submitted[0]
	if sub.ClientOrderID != created.ID || sub.Type != entities.OrderTypeMarket || !sub.Qty.Equal(decimal.RequireFromString("6.666666")) {
		t.Fatalf("submission = %+v", sub)
	}

	o := e.onlyOrder(t)
	if o.ID.String() != created.ID || o.Status != entities.OrderStatusFilled || o.BrokerID != "fake-1" ||
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

func TestOrderPath_SellClosesTheHeldPosition(t *testing.T) {
	e := newOrderEnv(t, true)
	e.signal.direction.Store("SELL")
	e.broker.SetPositions([]entities.Position{{Symbol: "AAPL", Quantity: decimal.RequireFromString("3")}})
	_, w := e.proposeAndApprove(t)
	r := decodeIntentResp(t, w, http.StatusOK)
	if r.Side != "sell" || len(e.broker.Submitted) != 1 || e.broker.Submitted[0].Side != entities.OrderSideSell ||
		!e.broker.Submitted[0].Qty.Equal(decimal.RequireFromString("3")) {
		t.Fatalf("response = %+v, submissions = %+v", r, e.broker.Submitted)
	}
}

func TestOrderPath_SellWithoutAPositionIsRefused(t *testing.T) {
	e := newOrderEnv(t, true)
	e.signal.direction.Store("SELL")
	r := decodeIntentResp(t, e.propose(t, agentToken, aaplBody), http.StatusCreated)
	if r.Status != "rejected" || r.RejectionCode != "risk_failed" || !contains(r.failed(), usecases.CheckSellCovered) {
		t.Fatalf("no shorts: %+v (failed %v)", r, r.failed())
	}
}

func TestOrderPath_RequestIDAndTokenReachSignalService(t *testing.T) {
	e := newOrderEnv(t, true)
	w := e.propose(t, agentToken, aaplBody, "X-Request-ID", "trace-bl07-1")
	decodeIntentResp(t, w, http.StatusCreated)
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

func TestOrderPath_HoldSignalIsRejectedAtCreation(t *testing.T) {
	e := newOrderEnv(t, true)
	e.signal.direction.Store("HOLD")
	r := decodeIntentResp(t, e.propose(t, agentToken, aaplBody), http.StatusCreated)
	if r.Status != "rejected" || !contains(r.failed(), usecases.CheckSignalActionable) || len(e.broker.Submitted) != 0 {
		t.Fatalf("response = %+v, submissions = %d", r, len(e.broker.Submitted))
	}
	if orders, _ := e.repo.FindBySymbol(context.Background(), "AAPL"); len(orders) != 0 {
		t.Fatalf("persisted %d orders for a HOLD", len(orders))
	}
}

func TestOrderPath_DailyLossLimitIsRejectedAtCreation(t *testing.T) {
	e := newOrderEnv(t, true)
	e.broker.SetAccount(ports.Account{Equity: decimal.NewFromInt(97_000), LastEquity: decimal.NewFromInt(100_000), BuyingPower: decimal.NewFromInt(97_000), Status: "ACTIVE"})
	r := decodeIntentResp(t, e.propose(t, agentToken, aaplBody), http.StatusCreated)
	if r.Status != "rejected" || !contains(r.failed(), usecases.CheckDailyLoss) || len(e.broker.Submitted) != 0 {
		t.Fatalf("response = %+v (failed %v)", r, r.failed())
	}
}

func TestOrderPath_ExecutionDisabledNeverReachesTheBroker(t *testing.T) {
	e := newOrderEnv(t, false)
	created := decodeIntentResp(t, e.propose(t, agentToken, aaplBody), http.StatusCreated)
	decodeEnvelope(t, e.approve(t, created.ID), http.StatusForbidden, apierror.CodeExecutionDisabled)
	if len(e.broker.Submitted) != 0 {
		t.Fatal("disabled execution must not reach the broker")
	}
}

func TestOrderPath_OnlyIntentScopesCanProposeAndDecide(t *testing.T) {
	e := newOrderEnv(t, true)
	decodeEnvelope(t, e.propose(t, "", aaplBody), http.StatusUnauthorized, apierror.CodeUnauthorized)
	decodeEnvelope(t, e.propose(t, "wrong-token", aaplBody), http.StatusUnauthorized, apierror.CodeUnauthorized)
	decodeEnvelope(t, e.propose(t, appToken, aaplBody), http.StatusForbidden, apierror.CodeInsufficientScope)
	if e.signal.calls.Load() != 0 {
		t.Fatal("rejected callers must not reach signal-service")
	}
	created := decodeIntentResp(t, e.propose(t, agentToken, aaplBody), http.StatusCreated)
	for _, tok := range []string{agentToken, appToken, adminToken} {
		decodeEnvelope(t, e.send(t, http.MethodPost, "/api/v1/order-intents/"+created.ID+"/approve", tok, `{}`), http.StatusForbidden, apierror.CodeInsufficientScope)
	}
	if len(e.broker.Submitted) != 0 {
		t.Fatal("only a human intent:approve token may submit")
	}
}

func TestOrderPath_InvalidBody(t *testing.T) {
	e := newOrderEnv(t, true)
	for _, body := range []string{``, `{`, `{"symbol":"AAPL"}`, `{"symbol":1,"strategy":"rsi"}`, `{"symbol":"AA PL","strategy":"rsi"}`} {
		decodeEnvelope(t, e.propose(t, agentToken, body), http.StatusBadRequest, apierror.CodeInvalidRequest)
	}
	if e.signal.calls.Load() != 0 {
		t.Fatal("invalid bodies must not reach signal-service")
	}
}

// ── failures: status, code, and what is left behind ──

func TestOrderPath_SignalServiceDown(t *testing.T) {
	e := newOrderEnv(t, true)
	e.signal.status.Store(http.StatusServiceUnavailable)
	decodeEnvelope(t, e.propose(t, agentToken, aaplBody), http.StatusBadGateway, apierror.CodeSignalUnavailable)
	if len(e.broker.Submitted) != 0 {
		t.Fatal("no signal, no order")
	}
}

func TestOrderPath_BrokerAccountUnavailableFailsClosed(t *testing.T) {
	e := newOrderEnv(t, true)
	e.broker.FailAccount = fmt.Errorf("alpaca get account: %w: %s", ports.ErrUnavailable, rawSecret)
	r := decodeIntentResp(t, e.propose(t, agentToken, aaplBody), http.StatusCreated) // send() checks rawSecret
	if r.Status != "rejected" || !contains(r.failed(), usecases.CheckAccountActive) || len(e.broker.Submitted) != 0 {
		t.Fatalf("fail closed: no account, no intent: %+v (failed %v)", r, r.failed())
	}
}

func TestOrderPath_InsufficientBuyingPowerIsRejectedAtCreation(t *testing.T) {
	e := newOrderEnv(t, true)
	e.broker.SetAccount(ports.Account{Equity: decimal.NewFromInt(100_000), LastEquity: decimal.NewFromInt(100_000), BuyingPower: decimal.NewFromInt(100), Status: "ACTIVE"})
	r := decodeIntentResp(t, e.propose(t, agentToken, aaplBody), http.StatusCreated)
	if r.Status != "rejected" || !contains(r.failed(), usecases.CheckBuyingPower) {
		t.Fatalf("response = %+v (failed %v)", r, r.failed())
	}
}

func TestOrderPath_BrokerRejection(t *testing.T) {
	e := newOrderEnv(t, true)
	e.broker.FailSubmit = fmt.Errorf("alpaca place order: %w (HTTP 422: %s)", ports.ErrRejected, rawSecret)
	_, w := e.proposeAndApprove(t)
	r := decodeIntentResp(t, w, http.StatusOK)
	if r.Status != "rejected" || r.RejectionCode != "broker_rejected" {
		t.Fatalf("response = %+v", r)
	}
	if o := e.onlyOrder(t); o.Status != entities.OrderStatusRejected {
		t.Fatalf("persisted status = %s", o.Status)
	}
}

func TestOrderPath_TimeoutButBrokerHasOrderIsReconciled(t *testing.T) {
	e := newOrderEnv(t, true)
	e.broker.SubmitThenFail = fmt.Errorf("alpaca place order: %w: %w", ports.ErrUnavailable, context.DeadlineExceeded)
	_, w := e.proposeAndApprove(t)
	r := decodeIntentResp(t, w, http.StatusOK)
	if r.Status != "filled" || len(e.broker.Submitted) != 1 {
		t.Fatalf("response = %+v, submissions = %d", r, len(e.broker.Submitted))
	}
	if o := e.onlyOrder(t); o.BrokerID != "fake-1" || o.Status != entities.OrderStatusFilled {
		t.Fatalf("persisted order = %+v", o)
	}
}

func TestOrderPath_TimeoutAndOrderNotAtBrokerIsNotPlaced(t *testing.T) {
	e := newOrderEnv(t, true)
	e.broker.FailSubmit = fmt.Errorf("alpaca place order: %w", ports.ErrUnavailable)
	_, w := e.proposeAndApprove(t)
	if r := decodeIntentResp(t, w, http.StatusOK); r.Status != "rejected" || r.RejectionCode != "broker_rejected" {
		t.Fatalf("response = %+v", r)
	}
	if o := e.onlyOrder(t); o.Status != entities.OrderStatusRejected {
		t.Fatalf("persisted status = %s", o.Status)
	}
}

func TestOrderPath_UnknownOutcomeIsAcceptedAndLeftPending(t *testing.T) {
	e := newOrderEnv(t, true)
	e.broker.FailSubmit = fmt.Errorf("alpaca place order: %w", ports.ErrUnavailable)
	e.broker.FailGet = fmt.Errorf("alpaca get order by client ID: %w", ports.ErrUnavailable)
	_, w := e.proposeAndApprove(t)
	if r := decodeIntentResp(t, w, http.StatusAccepted); r.Status != "approved" {
		t.Fatalf("intent must stay approved until reconciled: %+v", r)
	}
	if o := e.onlyOrder(t); o.Status != entities.OrderStatusPending {
		t.Fatalf("persisted status = %s, want pending (needs reconciliation)", o.Status)
	}
}

// ── concurrency: one intent, many approvals, exactly one order ──

func TestOrderPath_ConcurrentApprovalsSubmitOnce(t *testing.T) {
	e := newOrderEnv(t, true)
	created := decodeIntentResp(t, e.propose(t, agentToken, aaplBody), http.StatusCreated)
	const n = 10
	var wg sync.WaitGroup
	codes := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/order-intents/"+created.ID+"/approve", strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+humanToken)
			w := httptest.NewRecorder()
			e.router.ServeHTTP(w, req)
			codes[i] = w.Code
		}(i)
	}
	wg.Wait()
	ok := 0
	for i, c := range codes {
		switch c {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
		default:
			t.Fatalf("approval %d: status %d", i, c)
		}
	}
	if ok != 1 || len(e.broker.Submitted) != 1 || e.broker.Submitted[0].ClientOrderID != created.ID {
		t.Fatalf("want exactly one approval and submission, got %d and %d", ok, len(e.broker.Submitted))
	}
}

// ── kill switch (BL-19) and audit trail (BL-14) ──

func (e *orderEnv) killSwitch(t *testing.T, method, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	return e.send(t, method, "/api/v1/admin/kill-switch", token, body)
}

func (e *orderEnv) auditActions() []string {
	var out []string
	for _, ev := range e.audit.Events() {
		out = append(out, ev.Action)
	}
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// inOrder reports whether want appears in got as a subsequence.
func inOrder(got, want []string) bool {
	i := 0
	for _, g := range got {
		if i < len(want) && g == want[i] {
			i++
		}
	}
	return i == len(want)
}

func TestKillSwitch_EngageBlocksIntentsAndReleaseResumes(t *testing.T) {
	e := newOrderEnv(t, true)
	if w := e.killSwitch(t, "GET", humanToken, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"engaged":false`) {
		t.Fatalf("initial state: %d %s", w.Code, w.Body.String())
	}
	pending := decodeIntentResp(t, e.propose(t, agentToken, aaplBody), http.StatusCreated)

	w := e.killSwitch(t, "POST", adminToken, `{"engaged":true,"reason":"runaway strategy"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"cancel_all":"ok"`) || !strings.Contains(w.Body.String(), `"updated_by":"oncall"`) {
		t.Fatalf("engage: %d %s", w.Code, w.Body.String())
	}
	calls := e.signal.calls.Load()
	decodeEnvelope(t, e.propose(t, agentToken, `{"symbol":"AAPL","strategy":"rsi"}`), http.StatusConflict, apierror.CodeKillSwitchEngaged)
	if e.signal.calls.Load() != calls {
		t.Fatal("a halted engine must not call signal-service")
	}
	// An intent created before the halt cannot be submitted during it.
	if r := decodeIntentResp(t, e.approve(t, pending.ID), http.StatusOK); r.Status != "rejected" || r.RejectionCode != "kill_switch" {
		t.Fatalf("approve during halt: %+v", r)
	}
	if len(e.broker.Submitted) != 0 {
		t.Fatal("a halted engine must not submit")
	}

	if w := e.killSwitch(t, "POST", adminToken, `{"engaged":false,"reason":"fixed"}`); w.Code != 200 {
		t.Fatalf("release: %d %s", w.Code, w.Body.String())
	}
	_, aw := e.proposeAndApprove(t)
	decodeIntentResp(t, aw, http.StatusOK)

	got := e.auditActions()
	if !inOrder(got, []string{"kill_switch.engaged", "kill_switch.released", "order.submit_requested", "order.submitted"}) {
		t.Fatalf("audit trail = %s", strings.Join(got, ","))
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
	decodeEnvelope(t, e.propose(t, agentToken, aaplBody), http.StatusConflict, apierror.CodeKillSwitchEngaged)
}

func TestKillSwitch_EngageCancelsOpenOrdersEvenIfBrokerFails(t *testing.T) {
	e := newOrderEnv(t, true)
	e.broker.FailCancel = fmt.Errorf("alpaca cancel all: %w: %s", ports.ErrUnavailable, rawSecret)
	w := e.killSwitch(t, "POST", adminToken, `{"engaged":true,"reason":"incident"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"cancel_all":"failed"`) {
		t.Fatalf("engage with failing broker: %d %s", w.Code, w.Body.String())
	}
	if engaged, _ := e.ks.Engaged(context.Background()); !engaged {
		t.Fatal("the switch must hold even when cancel-all fails")
	}
}

// The legacy route is gone (M4): it is an unknown route now, for every token.
func TestLegacyPlaceRouteIsGone(t *testing.T) {
	e := newOrderEnv(t, true)
	for _, tok := range []string{humanToken, agentToken, ""} {
		w := e.send(t, http.MethodPost, "/api/v1/orders/place", tok, aaplBody)
		decodeEnvelope(t, w, http.StatusNotFound, apierror.CodeNotFound)
	}
	if e.signal.calls.Load() != 0 || len(e.broker.Submitted) != 0 {
		t.Fatal("the removed route must not reach any upstream")
	}
}
