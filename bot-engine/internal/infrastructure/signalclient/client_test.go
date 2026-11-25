package signalclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"

	"github.com/trading-bot/bot-engine/internal/infrastructure/reqid"
)

func TestGetSignalSendsBearerToken(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"1","symbol":"AAPL","direction":"BUY","strength":"STRONG","price":1,"confidence":0.9,"is_actionable":true,"generated_at":"2026-11-24T00:00:00Z"}`))
	}))
	defer srv.Close()

	s, err := New(srv.URL, "svc-token", zap.NewNop()).GetSignal(context.Background(), "AAPL", "rsi")
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer svc-token" || gotPath != "/api/v1/signals/generate" {
		t.Fatalf("auth=%q path=%q", gotAuth, gotPath)
	}
	if s.Direction != "BUY" || !s.IsActionable {
		t.Fatalf("unexpected signal %+v", s)
	}
}

func TestGetSignalNoTokenAndUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("no Authorization header expected without a token")
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	if _, err := New(srv.URL, "", zap.NewNop()).GetSignal(context.Background(), "AAPL", "rsi"); err == nil {
		t.Fatal("a 401 from signal-service must surface as an error")
	}
}

func TestGetSignalForwardsRequestID(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get(reqid.Header)
		_, _ = w.Write([]byte(`{"id":"1","symbol":"AAPL","direction":"HOLD","strength":"WEAK","price":1,"confidence":0.1,"is_actionable":false,"generated_at":"2026-11-24T00:00:00Z"}`))
	}))
	defer srv.Close()

	ctx := reqid.WithContext(context.Background(), "corr-123")
	if _, err := New(srv.URL, "t", zap.NewNop()).GetSignal(ctx, "AAPL", "rsi"); err != nil {
		t.Fatal(err)
	}
	if got != "corr-123" {
		t.Fatalf("X-Request-ID = %q, want corr-123", got)
	}
}
