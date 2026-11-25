package broker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alpacahq/alpaca-trade-api-go/v3/alpaca"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/trading-bot/bot-engine/internal/domain/entities"
	"github.com/trading-bot/bot-engine/internal/domain/ports"
	"github.com/trading-bot/bot-engine/internal/infrastructure/config"
)

const paperAccount = `{"account_number":"PA3TEST","status":"ACTIVE","currency":"USD","equity":"100000.25","last_equity":"99000","cash":"50000","buying_power":"200000","trading_blocked":false}`

// fakeAlpaca serves the account endpoint plus whatever routes a test adds.
func fakeAlpaca(t *testing.T, account string, routes map[string]http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v2/account", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(account)) })
	for pattern, h := range routes {
		mux.HandleFunc(pattern, h)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newTestBroker(t *testing.T, srv *httptest.Server) *AlpacaBroker {
	t.Helper()
	b, err := connect(context.Background(), clientFor(srv), zap.NewNop())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	return b
}

func clientFor(srv *httptest.Server) *alpaca.Client {
	return alpaca.NewClient(alpaca.ClientOpts{APIKey: "k", APISecret: "s", BaseURL: srv.URL, RetryLimit: 1, RetryDelay: time.Millisecond})
}

func apiError(status int, msg string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"code":40010001,"message":"` + msg + `"}`))
	}
}

func TestConnect_VerifiesPaperAccount(t *testing.T) {
	b := newTestBroker(t, fakeAlpaca(t, paperAccount, nil))
	if b.Mode() != ports.TradingModePaper {
		t.Fatalf("mode = %q", b.Mode())
	}
}

func TestConnect_RefusesNonPaperAccount(t *testing.T) {
	srv := fakeAlpaca(t, `{"account_number":"123456789","status":"ACTIVE"}`, nil)
	if _, err := connect(context.Background(), clientFor(srv), zap.NewNop()); err == nil || !strings.Contains(err.Error(), "not a paper account") {
		t.Fatalf("want refusal, got %v", err)
	}
}

func TestConnect_FailsClosedWhenBrokerUnreachable(t *testing.T) {
	srv := fakeAlpaca(t, paperAccount, nil)
	srv.Close()
	_, err := connect(context.Background(), clientFor(srv), zap.NewNop())
	if !errors.Is(err, ports.ErrUnavailable) {
		t.Fatalf("want ErrUnavailable, got %v", err)
	}
}

func TestNewAlpacaBroker_RefusesUnverifiedConfig(t *testing.T) {
	cfg := &config.Config{PaperTrading: false, AlpacaBaseURL: "https://api.alpaca.markets"}
	if _, err := NewAlpacaBroker(context.Background(), cfg, zap.NewNop()); err == nil {
		t.Fatal("want refusal for a non-paper config")
	}
}

func TestGetAccount_MapsDecimals(t *testing.T) {
	a, err := newTestBroker(t, fakeAlpaca(t, paperAccount, nil)).GetAccount(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !a.Equity.Equal(decimal.RequireFromString("100000.25")) || !a.DayPnL().Equal(decimal.RequireFromString("1000.25")) {
		t.Fatalf("equity=%s dayPnL=%s", a.Equity, a.DayPnL())
	}
	if a.TradingBlocked {
		t.Fatal("not blocked")
	}
}

func TestSubmitOrder_SendsClientOrderIDAndMapsResponse(t *testing.T) {
	var got map[string]any
	srv := fakeAlpaca(t, paperAccount, map[string]http.HandlerFunc{
		"POST /v2/orders": func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&got)
			_, _ = w.Write([]byte(`{"id":"ord-1","client_order_id":"coid-1","symbol":"AAPL","side":"buy","type":"market",
				"status":"filled","qty":"1.5","filled_qty":"1.5","filled_avg_price":"150.01","filled_at":"2026-11-24T15:00:00Z"}`))
		},
	})
	o, err := newTestBroker(t, srv).SubmitOrder(context.Background(), ports.SubmitOrderRequest{
		ClientOrderID: "coid-1", Symbol: "AAPL", Side: entities.OrderSideBuy, Type: entities.OrderTypeMarket,
		Qty: decimal.RequireFromString("1.5"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got["client_order_id"] != "coid-1" || got["qty"] != "1.5" || got["time_in_force"] != "day" {
		t.Fatalf("request body = %v", got)
	}
	if o.ID != "ord-1" || o.Status != entities.OrderStatusFilled || o.FilledAvgPrice == nil ||
		!o.FilledAvgPrice.Equal(decimal.RequireFromString("150.01")) || o.FilledAt == nil {
		t.Fatalf("order = %+v", o)
	}
}

func TestSubmitOrder_ValidatesBeforeSending(t *testing.T) {
	called := false
	srv := fakeAlpaca(t, paperAccount, map[string]http.HandlerFunc{
		"POST /v2/orders": func(http.ResponseWriter, *http.Request) { called = true },
	})
	_, err := newTestBroker(t, srv).SubmitOrder(context.Background(), ports.SubmitOrderRequest{
		Symbol: "AAPL", Side: entities.OrderSideBuy, Type: entities.OrderTypeMarket, Qty: decimal.NewFromInt(1),
	})
	if !errors.Is(err, ports.ErrInvalidOrder) || called {
		t.Fatalf("want ErrInvalidOrder without a request, got %v (called=%v)", err, called)
	}
}

func TestErrorMapping(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
		want    error
	}{
		{"404 not found", apiError(404, "order not found"), ports.ErrNotFound},
		{"429 rate limited", apiError(429, "too many requests"), ports.ErrRateLimited},
		{"403 insufficient buying power", apiError(403, "insufficient buying power"), ports.ErrInsufficientFunds},
		{"422 rejected", apiError(422, "qty must be > 0"), ports.ErrRejected},
		{"401 unauthorized", apiError(401, "unauthorized"), ports.ErrUnavailable},
		{"500 server error", apiError(500, "internal"), ports.ErrUnavailable},
		{"non-JSON 502", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(502)
			_, _ = w.Write([]byte("bad gateway"))
		}, ports.ErrUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := fakeAlpaca(t, paperAccount, map[string]http.HandlerFunc{"GET /v2/orders/{id}": tc.handler})
			_, err := newTestBroker(t, srv).GetOrder(context.Background(), "ord-1")
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}
}

func TestErrorMapping_DropsResponseBody(t *testing.T) {
	srv := fakeAlpaca(t, paperAccount, map[string]http.HandlerFunc{
		"GET /v2/orders/{id}": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(403)
			_, _ = w.Write([]byte(`{"code":1,"message":"forbidden","account_number":"PA3SECRET"}`))
		},
	})
	_, err := newTestBroker(t, srv).GetOrder(context.Background(), "ord-1")
	if err == nil || strings.Contains(err.Error(), "PA3SECRET") {
		t.Fatalf("error must not carry the body: %v", err)
	}
}

func TestCallHonoursContextCancellation(t *testing.T) {
	release := make(chan struct{})
	srv := fakeAlpaca(t, paperAccount, map[string]http.HandlerFunc{
		"GET /v2/clock": func(http.ResponseWriter, *http.Request) { <-release },
	})
	defer close(release)
	b := newTestBroker(t, srv)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := b.GetClock(ctx)
	if !errors.Is(err, ports.ErrUnavailable) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want ErrUnavailable wrapping DeadlineExceeded, got %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("call did not return when the context expired")
	}
}

func TestGetOrder_RejectsMalformedIDs(t *testing.T) {
	b := newTestBroker(t, fakeAlpaca(t, paperAccount, nil))
	for _, id := range []string{"", "../account", "a/b", "id?x=1", strings.Repeat("a", 65)} {
		if _, err := b.GetOrder(context.Background(), id); !errors.Is(err, ports.ErrInvalidOrder) {
			t.Fatalf("id %q: want ErrInvalidOrder, got %v", id, err)
		}
		if err := b.CancelOrder(context.Background(), id); !errors.Is(err, ports.ErrInvalidOrder) {
			t.Fatalf("cancel %q: want ErrInvalidOrder, got %v", id, err)
		}
	}
}

func TestGetOrderByClientID(t *testing.T) {
	srv := fakeAlpaca(t, paperAccount, map[string]http.HandlerFunc{
		"GET /v2/orders:by_client_order_id": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("client_order_id") != "coid-9" {
				apiError(404, "not found")(w, r)
				return
			}
			_, _ = w.Write([]byte(`{"id":"ord-9","client_order_id":"coid-9","status":"accepted","qty":"2","filled_qty":"0"}`))
		},
	})
	b := newTestBroker(t, srv)
	o, err := b.GetOrderByClientID(context.Background(), "coid-9")
	if err != nil || o.ID != "ord-9" || o.Status != entities.OrderStatusSubmitted {
		t.Fatalf("got %+v, %v", o, err)
	}
	if _, err := b.GetOrderByClientID(context.Background(), "other"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestGetPositions_UsesBrokerPrices(t *testing.T) {
	srv := fakeAlpaca(t, paperAccount, map[string]http.HandlerFunc{
		"GET /v2/positions": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`[
				{"symbol":"AAPL","qty":"2","side":"long","avg_entry_price":"100","current_price":"110.5","market_value":"221","unrealized_pl":"21"},
				{"symbol":"TSLA","qty":"3","side":"short","avg_entry_price":"200","current_price":null,"market_value":null,"unrealized_pl":null},
				{"symbol":"ZERO","qty":"0","side":"long","avg_entry_price":"1"}
			]`))
		},
	})
	ps, err := newTestBroker(t, srv).GetPositions(context.Background())
	if err != nil || len(ps) != 3 {
		t.Fatalf("got %v, %v", ps, err)
	}
	if !ps[0].CurrentPrice.Equal(decimal.RequireFromString("110.5")) || !ps[0].UnrealizedPnL.Equal(decimal.NewFromInt(21)) {
		t.Fatalf("AAPL = %+v", ps[0])
	}
	if !ps[1].Quantity.Equal(decimal.NewFromInt(-3)) || !ps[1].CurrentPrice.IsZero() {
		t.Fatalf("short TSLA = %+v", ps[1])
	}
	if !ps[2].CurrentPrice.IsZero() { // qty 0 must not divide by zero
		t.Fatalf("ZERO = %+v", ps[2])
	}
}

func TestFromAlpacaStatus(t *testing.T) {
	cases := map[string]entities.OrderStatus{
		"new": entities.OrderStatusSubmitted, "accepted": entities.OrderStatusSubmitted,
		"pending_new": entities.OrderStatusSubmitted, "pending_cancel": entities.OrderStatusSubmitted,
		"done_for_day": entities.OrderStatusSubmitted, "held": entities.OrderStatusSubmitted,
		"partially_filled": entities.OrderStatusPartiallyFilled, "filled": entities.OrderStatusFilled,
		"canceled": entities.OrderStatusCanceled, "replaced": entities.OrderStatusCanceled,
		"rejected": entities.OrderStatusRejected, "expired": entities.OrderStatusExpired,
		"something_new": entities.OrderStatusSubmitted, // unknown is never terminal
	}
	for in, want := range cases {
		if got := FromAlpacaStatus(in); got != want {
			t.Errorf("%s -> %s, want %s", in, got, want)
		}
	}
}
