package broker

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/trading-bot/bot-engine/internal/domain/entities"
	"github.com/trading-bot/bot-engine/internal/domain/ports"
)

func TestStreamTradeUpdatesMapsEvents(t *testing.T) {
	srv := fakeAlpaca(t, paperAccount, map[string]http.HandlerFunc{
		"GET /v2beta1/events/trades": func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("APCA-API-KEY-ID") == "" {
				t.Error("stream request is not authenticated")
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(": keepalive\n"))
			_, _ = w.Write([]byte(`data: {"event":"fill","at":"2026-11-25T14:30:01Z","order":{"id":"b-1","client_order_id":"c-1","symbol":"AAPL","side":"buy","type":"market","status":"filled","qty":"2","filled_qty":"2","filled_avg_price":"189.5"}}` + "\n"))
		},
	})
	b := newTestBroker(t, srv)

	var got []ports.TradeUpdate
	err := b.StreamTradeUpdates(context.Background(), func(u ports.TradeUpdate) { got = append(got, u) })
	if err != nil {
		t.Fatalf("stream ended with %v, want nil on EOF", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d updates", len(got))
	}
	u := got[0]
	if u.Event != "fill" || u.Order.ClientOrderID != "c-1" || u.Order.Status != entities.OrderStatusFilled ||
		u.Order.FilledAvgPrice == nil || u.Order.FilledAvgPrice.String() != "189.5" {
		t.Fatalf("update = %+v", u)
	}
}

func TestStreamTradeUpdatesHidesResponseBody(t *testing.T) {
	srv := fakeAlpaca(t, paperAccount, map[string]http.HandlerFunc{
		"GET /v2beta1/events/trades": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"account PA123SECRET forbidden"}`))
		},
	})
	err := newTestBroker(t, srv).StreamTradeUpdates(context.Background(), func(ports.TradeUpdate) {})
	if !errors.Is(err, ports.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if strings.Contains(err.Error(), "PA123SECRET") {
		t.Fatal("stream error leaked the response body")
	}
}

func TestCapabilitiesReportStreaming(t *testing.T) {
	if !(&AlpacaBroker{}).Capabilities().TradeStreaming {
		t.Fatal("alpaca adapter streams trade updates")
	}
}
