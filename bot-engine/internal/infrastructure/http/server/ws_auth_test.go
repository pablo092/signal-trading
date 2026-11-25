package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"

	"github.com/trading-bot/bot-engine/internal/infrastructure/auth"
	"github.com/trading-bot/bot-engine/internal/infrastructure/config"
	"github.com/trading-bot/bot-engine/internal/infrastructure/messaging"
)

// BL-15: /ws requires a portfolio:read token, by header or subprotocol, and an
// allowlisted browser origin.
func newWSServer(t *testing.T) string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{PaperTrading: true, AlpacaBaseURL: "https://paper-api.alpaca.markets"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	store, err := auth.NewStore([]*auth.Principal{
		{ID: "mcp-gateway", Type: auth.ActorAgent, SHA256: auth.HashToken(agentToken), Scopes: []string{auth.ScopePortfolioRead}},
		{ID: "app", Type: auth.ActorService, SHA256: auth.HashToken(appToken), Scopes: []string{auth.ScopeMarketRead}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	hub := messaging.NewWSHub([]string{"https://app.example.com"}, zap.NewNop())
	go hub.Run(ctx)
	r := NewRouter(Deps{Config: cfg, Tokens: store, Broker: newFakeBroker(), Hub: hub, Registry: prometheus.NewRegistry(), Logger: zap.NewNop()})
	srv := httptest.NewServer(r)
	t.Cleanup(func() { cancel(); srv.Close() })
	return "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
}

func dialWS(url string, h http.Header, subprotocols ...string) (*websocket.Conn, int, error) {
	d := websocket.Dialer{Subprotocols: subprotocols}
	conn, resp, err := d.Dial(url, h)
	code := 0
	if resp != nil {
		code = resp.StatusCode
	}
	return conn, code, err
}

func TestWS_RequiresToken(t *testing.T) {
	url := newWSServer(t)
	for name, tc := range map[string]struct {
		url          string
		header       http.Header
		subprotocols []string
		want         int
	}{
		"no token":            {url, nil, nil, http.StatusUnauthorized},
		"token in query":      {url + "?token=" + agentToken, nil, nil, http.StatusUnauthorized},
		"wrong subprotocol":   {url, nil, []string{"token", agentToken}, http.StatusUnauthorized},
		"token without scope": {url, http.Header{"Authorization": {"Bearer " + appToken}}, nil, http.StatusForbidden},
		"foreign origin":      {url, http.Header{"Authorization": {"Bearer " + agentToken}, "Origin": {"https://evil.example"}}, nil, http.StatusForbidden},
	} {
		t.Run(name, func(t *testing.T) {
			conn, code, err := dialWS(tc.url, tc.header, tc.subprotocols...)
			if err == nil {
				conn.Close()
				t.Fatal("upgrade should have been refused")
			}
			if code != tc.want {
				t.Fatalf("status %d, want %d", code, tc.want)
			}
		})
	}
}

func TestWS_AcceptsHeaderOrSubprotocolToken(t *testing.T) {
	url := newWSServer(t)
	conn, _, err := dialWS(url, http.Header{"Authorization": {"Bearer " + agentToken}})
	if err != nil {
		t.Fatalf("header token: %v", err)
	}
	conn.Close()

	conn, _, err = dialWS(url, http.Header{"Origin": {"https://app.example.com"}}, messaging.Subprotocol, agentToken)
	if err != nil {
		t.Fatalf("subprotocol token: %v", err)
	}
	defer conn.Close()
	if conn.Subprotocol() != messaging.Subprotocol {
		t.Fatalf("server echoed %q; it must echo only %q, never the token", conn.Subprotocol(), messaging.Subprotocol)
	}
}
