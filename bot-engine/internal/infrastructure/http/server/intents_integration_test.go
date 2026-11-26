package server

// BL-21/22: order-intent HTTP path. Real router, auth, request IDs, handlers,
// IntentService, risk engine, signal client (HTTP stub) and in-memory repos;
// only the broker is the fake.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/trading-bot/bot-engine/internal/application/eventbus"
	"github.com/trading-bot/bot-engine/internal/application/usecases"
	"github.com/trading-bot/bot-engine/internal/contracttest"
	"github.com/trading-bot/bot-engine/internal/domain/entities"
	"github.com/trading-bot/bot-engine/internal/domain/ports"
	"github.com/trading-bot/bot-engine/internal/infrastructure/auditlog"
	"github.com/trading-bot/bot-engine/internal/infrastructure/auth"
	"github.com/trading-bot/bot-engine/internal/infrastructure/broker/fakebroker"
	"github.com/trading-bot/bot-engine/internal/infrastructure/config"
	"github.com/trading-bot/bot-engine/internal/infrastructure/repository"
	"github.com/trading-bot/bot-engine/internal/infrastructure/signalclient"
)

const (
	proposerToken = "proposer-agent-token"
	approverToken = "approver-human-token"
	readerToken   = "reader-service-token"
)

type intentHTTPEnv struct {
	router *gin.Engine
	broker *fakebroker.Broker
	signal *signalStub
}

func newIntentHTTPEnv(t *testing.T, execEnabled bool) *intentHTTPEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{PaperTrading: true, AlpacaBaseURL: "https://paper-api.alpaca.markets", OrderExecutionEnabled: execEnabled}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	store, err := auth.NewStore([]*auth.Principal{
		{ID: "mcp-gateway", Type: auth.ActorAgent, SHA256: auth.HashToken(proposerToken), Scopes: []string{auth.ScopePortfolioRead, auth.ScopeIntentCreate}},
		{ID: "owner", Type: auth.ActorHuman, SHA256: auth.HashToken(approverToken), Scopes: []string{auth.ScopePortfolioRead, auth.ScopeIntentCreate, auth.ScopeIntentApprove}},
		{ID: "app", Type: auth.ActorService, SHA256: auth.HashToken(readerToken), Scopes: []string{auth.ScopePortfolioRead}},
	})
	if err != nil {
		t.Fatal(err)
	}
	e := &intentHTTPEnv{broker: fakebroker.New(decimal.NewFromInt(100_000)), signal: newSignalStub(t)}
	e.broker.SetFillPrice("AAPL", decimal.RequireFromString("150.02"))
	audit := auditlog.NewMemory(zap.NewNop())
	bus := eventbus.New(zap.NewNop())
	ks := usecases.NewKillSwitchService(&auditlog.MemoryKillSwitch{}, e.broker, audit, bus, false, zap.NewNop())
	if err := ks.Init(context.Background()); err != nil {
		t.Fatal(err)
	}
	policy := usecases.RiskPolicy{
		Rules: entities.DefaultRiskRules(), SignalMaxAge: 5 * time.Minute, MaxOrderNotional: decimal.NewFromInt(1000),
		SymbolAllowlist: []string{"AAPL"}, StrategyAllowlist: []string{"rsi"},
	}
	svc := usecases.NewIntentService(usecases.IntentDeps{
		Intents: repository.NewInMemoryIntentRepo(), Orders: repository.NewInMemoryOrderRepo(),
		Signals: signalclient.New(e.signal.srv.URL, engineSignalToken, zap.NewNop()), Broker: e.broker,
		Risk: usecases.NewRiskEngine(policy, e.broker, ks), KillSwitch: ks, Audit: audit, Publisher: bus,
		Policy: usecases.IntentPolicy{TTL: 5 * time.Minute, RateLimitPerHour: 100, ExecutionEnabled: execEnabled},
		Logger: zap.NewNop(),
	})
	e.router = NewRouter(Deps{Config: cfg, Tokens: store, KillSwitch: ks, Intents: svc, Broker: e.broker, Hub: &fakeHub{}, Registry: prometheus.NewRegistry(), Logger: zap.NewNop()})
	return e
}

func (e *intentHTTPEnv) do(t *testing.T, method, path, token, body string, headers ...string) *httptest.ResponseRecorder {
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

func (e *intentHTTPEnv) propose(t *testing.T, key string) *httptest.ResponseRecorder {
	t.Helper()
	return e.do(t, http.MethodPost, "/api/v1/order-intents", proposerToken, aaplBody, "Idempotency-Key", key)
}

type intentBody struct {
	ID, Status string
	Order      *struct{ Status string }
}

func decodeIntent(t *testing.T, w *httptest.ResponseRecorder, status int) intentBody {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status %d, want %d: %s", w.Code, status, w.Body.String())
	}
	contracttest.Validate(t, "order-intent.schema.json", w.Body.Bytes())
	var b intentBody
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestIntentHTTP_ProposeApproveFill(t *testing.T) {
	e := newIntentHTTPEnv(t, true)
	created := decodeIntent(t, e.propose(t, "key-00000001"), http.StatusCreated)
	if created.Status != "pending_approval" || len(e.broker.Submitted) != 0 {
		t.Fatalf("created = %+v, submissions = %d", created, len(e.broker.Submitted))
	}

	// A read-only token sees it, both alone and in the list.
	decodeIntent(t, e.do(t, http.MethodGet, "/api/v1/order-intents/"+created.ID, readerToken, ""), http.StatusOK)
	list := e.do(t, http.MethodGet, "/api/v1/order-intents?status=pending_approval", readerToken, "")
	if list.Code != http.StatusOK {
		t.Fatalf("list: %d %s", list.Code, list.Body.String())
	}
	contracttest.Validate(t, "order-intent-list.schema.json", list.Body.Bytes())

	// The agent that proposed it cannot approve it.
	decodeEnvelope(t, e.do(t, http.MethodPost, "/api/v1/order-intents/"+created.ID+"/approve", proposerToken, `{}`), http.StatusForbidden, "insufficient_scope")

	approved := decodeIntent(t, e.do(t, http.MethodPost, "/api/v1/order-intents/"+created.ID+"/approve", approverToken, `{"reason":"ok"}`), http.StatusOK)
	if approved.Status != "filled" || approved.Order == nil || approved.Order.Status != "filled" {
		t.Fatalf("approved = %+v", approved)
	}
	if len(e.broker.Submitted) != 1 || e.broker.Submitted[0].ClientOrderID != created.ID {
		t.Fatalf("want one submission with client_order_id = intent id, got %+v", e.broker.Submitted)
	}

	// A second approval is refused: the intent is no longer pending.
	decodeEnvelope(t, e.do(t, http.MethodPost, "/api/v1/order-intents/"+created.ID+"/approve", approverToken, `{}`), http.StatusConflict, "intent_not_pending")
}

func TestIntentHTTP_IdempotencyKey(t *testing.T) {
	e := newIntentHTTPEnv(t, true)
	decodeEnvelope(t, e.do(t, http.MethodPost, "/api/v1/order-intents", proposerToken, aaplBody), http.StatusBadRequest, "idempotency_key_required")
	decodeEnvelope(t, e.propose(t, "short"), http.StatusBadRequest, "idempotency_key_required")

	first := e.propose(t, "key-00000001")
	again := e.propose(t, "key-00000001")
	a, b := decodeIntent(t, first, http.StatusCreated), decodeIntent(t, again, http.StatusCreated)
	if a.ID != b.ID || again.Header().Get("Idempotent-Replayed") != "true" || first.Header().Get("Idempotent-Replayed") != "" {
		t.Fatalf("replay: %s vs %s, header %q", a.ID, b.ID, again.Header().Get("Idempotent-Replayed"))
	}
	if e.signal.calls.Load() != 1 {
		t.Fatalf("a replay must not fetch the signal again (calls=%d)", e.signal.calls.Load())
	}
	other := `{"symbol":"AAPL","strategy":"ma_cross"}`
	decodeEnvelope(t, e.do(t, http.MethodPost, "/api/v1/order-intents", proposerToken, other, "Idempotency-Key", "key-00000001"), http.StatusUnprocessableEntity, "idempotency_key_reused")
}

func TestIntentHTTP_ApprovalNeedsExecutionEnabled(t *testing.T) {
	e := newIntentHTTPEnv(t, false)
	created := decodeIntent(t, e.propose(t, "key-00000001"), http.StatusCreated)
	decodeEnvelope(t, e.do(t, http.MethodPost, "/api/v1/order-intents/"+created.ID+"/approve", approverToken, `{}`), http.StatusForbidden, "execution_disabled")
	if len(e.broker.Submitted) != 0 {
		t.Fatal("nothing may reach the broker while execution is disabled")
	}
}

func TestIntentHTTP_UnknownOutcomeIs202(t *testing.T) {
	e := newIntentHTTPEnv(t, true)
	created := decodeIntent(t, e.propose(t, "key-00000001"), http.StatusCreated)
	e.broker.FailSubmit = errUnavailable()
	e.broker.FailGet = errUnavailable()
	got := decodeIntent(t, e.do(t, http.MethodPost, "/api/v1/order-intents/"+created.ID+"/approve", approverToken, `{}`), http.StatusAccepted)
	if got.Status != "approved" {
		t.Fatalf("unknown outcome must leave the intent approved, got %s", got.Status)
	}
}

func TestIntentHTTP_RejectAndCancel(t *testing.T) {
	e := newIntentHTTPEnv(t, true)
	a := decodeIntent(t, e.propose(t, "key-00000001"), http.StatusCreated)
	decodeEnvelope(t, e.do(t, http.MethodPost, "/api/v1/order-intents/"+a.ID+"/reject", approverToken, `{}`), http.StatusBadRequest, "invalid_request")
	if r := decodeIntent(t, e.do(t, http.MethodPost, "/api/v1/order-intents/"+a.ID+"/reject", approverToken, `{"reason":"not today"}`), http.StatusOK); r.Status != "rejected" {
		t.Fatalf("reject: %+v", r)
	}

	e.signal.direction.Store("BUY")
	b := decodeIntent(t, e.do(t, http.MethodPost, "/api/v1/order-intents", proposerToken, `{"symbol":"AAPL","strategy":"rsi"}`, "Idempotency-Key", "key-00000002"), http.StatusCreated)
	decodeEnvelope(t, e.do(t, http.MethodPost, "/api/v1/order-intents/"+b.ID+"/cancel", approverToken, `{}`), http.StatusForbidden, "not_intent_owner")
	if c := decodeIntent(t, e.do(t, http.MethodPost, "/api/v1/order-intents/"+b.ID+"/cancel", proposerToken, `{}`), http.StatusOK); c.Status != "canceled" {
		t.Fatalf("cancel: %+v", c)
	}
}

func TestIntentHTTP_NotFoundAndBadID(t *testing.T) {
	e := newIntentHTTPEnv(t, true)
	decodeEnvelope(t, e.do(t, http.MethodGet, "/api/v1/order-intents/6f1c2d3e-4b5a-4c6d-8e7f-9a0b1c2d3e4f", readerToken, ""), http.StatusNotFound, "not_found")
	decodeEnvelope(t, e.do(t, http.MethodGet, "/api/v1/order-intents/not-a-uuid", readerToken, ""), http.StatusNotFound, "not_found")
}

func TestIntentHTTP_ScopesAreEnforced(t *testing.T) {
	e := newIntentHTTPEnv(t, true)
	id := "6f1c2d3e-4b5a-4c6d-8e7f-9a0b1c2d3e4f"
	for _, rt := range []struct{ m, p string }{
		{"GET", "/api/v1/order-intents"}, {"POST", "/api/v1/order-intents"}, {"GET", "/api/v1/order-intents/" + id},
		{"POST", "/api/v1/order-intents/" + id + "/approve"}, {"POST", "/api/v1/order-intents/" + id + "/reject"},
		{"POST", "/api/v1/order-intents/" + id + "/cancel"},
	} {
		if w := e.do(t, rt.m, rt.p, "", `{}`); w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a token: %d", rt.m, rt.p, w.Code)
		}
	}
	// A read-only service token can neither propose nor decide.
	decodeEnvelope(t, e.do(t, http.MethodPost, "/api/v1/order-intents", readerToken, aaplBody, "Idempotency-Key", "key-00000001"), http.StatusForbidden, "insufficient_scope")
	decodeEnvelope(t, e.do(t, http.MethodPost, "/api/v1/order-intents/"+id+"/reject", readerToken, `{"reason":"x"}`), http.StatusForbidden, "insufficient_scope")
}

func errUnavailable() error { return fmt.Errorf("fake: %w", ports.ErrUnavailable) }
