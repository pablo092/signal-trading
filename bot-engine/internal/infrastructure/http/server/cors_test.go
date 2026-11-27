package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"

	"github.com/trading-bot/bot-engine/internal/infrastructure/auth"
	"github.com/trading-bot/bot-engine/internal/infrastructure/config"
)

const localApp = "http://localhost:5000"

func corsEnv(t *testing.T, origins []string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{PaperTrading: true, AlpacaBaseURL: "https://paper-api.alpaca.markets", CORSAllowedOrigins: origins}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	store, err := auth.NewStore([]*auth.Principal{
		{ID: "owner", Type: auth.ActorHuman, SHA256: auth.HashToken(humanToken), Scopes: []string{auth.ScopePortfolioRead}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return NewRouter(Deps{Config: cfg, Tokens: store, Broker: newFakeBroker(), Hub: &fakeHub{}, Registry: prometheus.NewRegistry(), Logger: zap.NewNop()})
}

func preflight(r *gin.Engine, path, origin string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodOptions, path, nil)
	req.Header.Set("Origin", origin)
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	req.Header.Set("Access-Control-Request-Headers", "authorization, content-type, idempotency-key")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// A browser preflight carries no token, so it must be answered before auth,
// for both static and parameterized routes.
func TestCORSPreflightAnsweredBeforeAuth(t *testing.T) {
	r := corsEnv(t, []string{localApp})
	for _, path := range []string{"/api/v1/order-intents", "/api/v1/order-intents/abc/approve", "/api/v1/portfolio/equity"} {
		w := preflight(r, path, localApp)
		if w.Code != http.StatusNoContent || w.Header().Get("Access-Control-Allow-Origin") != localApp {
			t.Errorf("%s: status=%d allow-origin=%q, want 204 and the origin", path, w.Code, w.Header().Get("Access-Control-Allow-Origin"))
		}
	}
}

func TestCORSKeepsAuthOnRealRequests(t *testing.T) {
	r := corsEnv(t, []string{localApp})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/order-intents", nil)
	req.Header.Set("Origin", localApp)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 without a token", w.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/trading-mode", nil)
	req.Header.Set("Origin", localApp)
	req.Header.Set("Authorization", "Bearer "+humanToken)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Header().Get("Access-Control-Allow-Origin") != localApp {
		t.Fatalf("status=%d allow-origin=%q, want 200 and the origin", w.Code, w.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestCORSOffByDefault(t *testing.T) {
	r := corsEnv(t, nil)
	w := preflight(r, "/api/v1/order-intents", localApp)
	if w.Header().Get("Access-Control-Allow-Origin") != "" || w.Code == http.StatusNoContent {
		t.Fatalf("status=%d allow-origin=%q: CORS must be off without CORS_ALLOWED_ORIGINS", w.Code, w.Header().Get("Access-Control-Allow-Origin"))
	}
}
