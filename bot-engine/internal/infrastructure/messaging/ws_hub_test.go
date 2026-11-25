package messaging

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/trading-bot/bot-engine/internal/application/usecases"
	"github.com/trading-bot/bot-engine/internal/domain/entities"
	"github.com/trading-bot/bot-engine/internal/domain/ports"
)

type hubEnv struct {
	hub    *WSHub
	srv    *httptest.Server
	cancel context.CancelFunc
	seq    int
	mu     sync.Mutex
}

func newHubEnv(t *testing.T, origins ...string) *hubEnv {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	e := &hubEnv{hub: NewWSHub(origins, zap.NewNop()), cancel: cancel}
	go e.hub.Run(ctx)
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id") // tests only: lets two clients share an ID
		if id == "" {
			e.mu.Lock()
			e.seq++
			id = "c" + string(rune('0'+e.seq))
			e.mu.Unlock()
		}
		e.hub.ServeWS(w, r, id)
	}))
	t.Cleanup(func() { cancel(); e.srv.Close() })
	return e
}

func (e *hubEnv) dial(t *testing.T, header http.Header, query string) *websocket.Conn {
	t.Helper()
	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(e.srv.URL, "http")+"/"+query, header)
	if err != nil {
		code := 0
		if resp != nil {
			code = resp.StatusCode
		}
		t.Fatalf("dial: %v (HTTP %d)", err, code)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func (e *hubEnv) waitClients(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for e.hub.ConnectedClients() != n {
		if time.Now().After(deadline) {
			t.Fatalf("want %d clients, have %d", n, e.hub.ConnectedClients())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func readEnvelope(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var env map[string]any
	if err := json.Unmarshal(msg, &env); err != nil {
		t.Fatal(err)
	}
	return env
}

func sampleOrder() *entities.Order {
	o, _ := entities.NewMarketOrder("AAPL", entities.OrderSideBuy, decimal.RequireFromString("33.333333"), "rsi", "sig-1")
	p := decimal.RequireFromString("150.02")
	return o.ApplyBrokerState(entities.BrokerState{BrokerID: "b-1", Status: entities.OrderStatusFilled, FilledQty: o.Quantity, FilledAvgPrice: &p})
}

func TestBroadcastReachesEveryClientAsContractEvent(t *testing.T) {
	e := newHubEnv(t)
	a, b := e.dial(t, nil, ""), e.dial(t, nil, "")
	e.waitClients(t, 2)

	e.hub.HandleDomainEvent(context.Background(), ports.DomainEvent{Type: usecases.EventOrderPlaced, Payload: sampleOrder()})
	for _, c := range []*websocket.Conn{a, b} {
		env := readEnvelope(t, c)
		data := env["data"].(map[string]any)
		if env["v"] != float64(1) || env["type"] != TypeOrderPlaced || env["id"] == "" ||
			data["quantity"] != 33.333333 || data["filled_price"] != 150.02 || data["status"] != "filled" {
			t.Fatalf("envelope = %v", env)
		}
	}
}

func TestUnrelayedEventsAreNotSent(t *testing.T) {
	e := newHubEnv(t)
	c := e.dial(t, nil, "")
	e.waitClients(t, 1)
	e.hub.HandleDomainEvent(context.Background(), ports.DomainEvent{Type: "Internal", Payload: map[string]string{"secret": "x"}})
	e.hub.HandleDomainEvent(context.Background(), ports.DomainEvent{Type: usecases.EventSignalReceived, Payload: &ports.Signal{ID: "s", Price: decimal.NewFromInt(1)}})
	if env := readEnvelope(t, c); env["type"] != TypeSignalReceived {
		t.Fatalf("first message should be the signal, got %v", env)
	}
}

func TestOriginAllowlist(t *testing.T) {
	e := newHubEnv(t, "https://app.example.com")
	e.dial(t, http.Header{"Origin": {"https://app.example.com"}}, "")
	e.dial(t, nil, "") // non-browser client: no Origin header

	_, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(e.srv.URL, "http")+"/", http.Header{"Origin": {"https://evil.example"}})
	if err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign origin: err=%v resp=%v", err, resp)
	}
}

func TestEmptyAllowlistRejectsEveryBrowserOrigin(t *testing.T) {
	e := newHubEnv(t)
	_, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(e.srv.URL, "http")+"/", http.Header{"Origin": {"http://localhost:3000"}})
	if err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("want 403, got err=%v resp=%v", err, resp)
	}
}

func TestSameClientIDNoLongerEvictsTheOtherConnection(t *testing.T) {
	e := newHubEnv(t)
	first := e.dial(t, nil, "?id=dup")
	second := e.dial(t, nil, "?id=dup")
	e.waitClients(t, 2)
	_ = first.Close()
	e.waitClients(t, 1)
	e.hub.HandleDomainEvent(context.Background(), ports.DomainEvent{Type: usecases.EventOrderPlaced, Payload: sampleOrder()})
	if env := readEnvelope(t, second); env["type"] != TypeOrderPlaced {
		t.Fatalf("surviving client got %v", env)
	}
}

func TestConcurrentBroadcastsWithManyClients(t *testing.T) {
	e := newHubEnv(t)
	const clients, events = 5, 50
	conns := make([]*websocket.Conn, clients)
	for i := range conns {
		conns[i] = e.dial(t, nil, "")
	}
	e.waitClients(t, clients)

	var wg sync.WaitGroup
	for i := 0; i < events; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.hub.HandleDomainEvent(context.Background(), ports.DomainEvent{Type: usecases.EventOrderPlaced, Payload: sampleOrder()})
		}()
	}
	wg.Wait()
	for _, c := range conns {
		for i := 0; i < events; i++ {
			readEnvelope(t, c)
		}
	}
}

func TestSlowClientIsDropped(t *testing.T) {
	h := NewWSHub(nil, zap.NewNop())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go h.Run(ctx)
	slow := &client{id: "slow", send: make(chan []byte, 1)} // no writer drains it
	h.register <- slow
	h.broadcast <- []byte("1")
	h.broadcast <- []byte("2") // buffer full → dropped
	deadline := time.Now().Add(2 * time.Second)
	for h.ConnectedClients() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("slow client was not dropped")
		}
		time.Sleep(5 * time.Millisecond)
	}
	<-slow.send // the buffered message
	if _, open := <-slow.send; open {
		t.Fatal("send channel should be closed")
	}
}

func TestShutdownClosesConnections(t *testing.T) {
	e := newHubEnv(t)
	c := e.dial(t, nil, "")
	e.waitClients(t, 1)
	e.cancel()
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := c.ReadMessage(); err == nil {
		t.Fatal("connection should close on shutdown")
	}
}

func TestInboundMessagesOverLimitCloseTheConnection(t *testing.T) {
	e := newHubEnv(t)
	c := e.dial(t, nil, "")
	e.waitClients(t, 1)
	_ = c.WriteMessage(websocket.TextMessage, []byte(strings.Repeat("x", maxInboundSize+1)))
	e.waitClients(t, 0)
}
