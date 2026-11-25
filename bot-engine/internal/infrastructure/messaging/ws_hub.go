// Package messaging provides the WebSocket hub that relays domain events to
// connected clients as versioned contract events (BL-15, BL-16).
//
// Concurrency model: one Run goroutine owns the client set; each connection
// has exactly one writer (writePump, which also sends pings) and one reader
// (readPump). Nothing else touches a connection, so gorilla/websocket's
// single-writer rule always holds.
package messaging

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"

	"github.com/trading-bot/bot-engine/internal/domain/ports"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = 25 * time.Second // < pongWait
	maxInboundSize = 512              // clients only send control frames
	sendBuffer     = 64

	// Subprotocol lets browsers authenticate the upgrade: they cannot set an
	// Authorization header, so they send "Sec-WebSocket-Protocol: bearer, <token>".
	// The server echoes only "bearer", never the token.
	Subprotocol = "bearer"
)

// client is one connection. send is closed only by the hub's Run goroutine.
type client struct {
	id   string
	conn *websocket.Conn
	send chan []byte
}

// WSHub manages all active WebSocket connections and broadcasts events.
type WSHub struct {
	register   chan *client
	unregister chan *client
	broadcast  chan []byte
	done       chan struct{}
	count      atomic.Int64
	upgrader   websocket.Upgrader
	logger     *zap.Logger
}

// NewWSHub creates a hub. allowedOrigins is the browser origin allowlist
// (e.g. "https://app.example.com"); requests without an Origin header are
// non-browser clients and are allowed (they still need a token).
func NewWSHub(allowedOrigins []string, logger *zap.Logger) *WSHub {
	allowed := make(map[string]bool, len(allowedOrigins))
	for _, o := range allowedOrigins {
		allowed[strings.ToLower(strings.TrimRight(o, "/"))] = true
	}
	h := &WSHub{
		register:   make(chan *client),
		unregister: make(chan *client),
		broadcast:  make(chan []byte, 256),
		done:       make(chan struct{}),
		logger:     logger,
	}
	h.upgrader = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		Subprotocols:    []string{Subprotocol},
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			if origin == "" {
				return true
			}
			ok := allowed[strings.ToLower(origin)]
			if !ok {
				logger.Warn("ws.origin_rejected", zap.String("origin", origin))
			}
			return ok
		},
	}
	return h
}

// Run owns the client set until ctx ends, then closes every connection.
func (h *WSHub) Run(ctx context.Context) {
	clients := map[*client]bool{}
	drop := func(c *client) {
		if clients[c] {
			delete(clients, c)
			close(c.send)
			h.count.Store(int64(len(clients)))
		}
	}
	defer func() {
		close(h.done)
		for c := range clients {
			drop(c)
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case c := <-h.register:
			clients[c] = true
			h.count.Store(int64(len(clients)))
			h.logger.Info("ws.client_connected", zap.String("client_id", c.id), zap.Int("total_clients", len(clients)))
		case c := <-h.unregister:
			drop(c)
			h.logger.Info("ws.client_disconnected", zap.String("client_id", c.id))
		case msg := <-h.broadcast:
			for c := range clients {
				select {
				case c.send <- msg:
				default: // a client that cannot keep up is disconnected, not silently skipped
					h.logger.Warn("ws.slow_client_dropped", zap.String("client_id", c.id))
					drop(c)
				}
			}
		}
	}
}

// ServeWS upgrades the (already authenticated) request and serves the
// connection until it closes. clientID is server-assigned.
func (h *WSHub) ServeWS(w http.ResponseWriter, r *http.Request, clientID string) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Warn("ws.upgrade_failed", zap.String("client_id", clientID), zap.Error(err))
		return // the upgrader already wrote the HTTP error
	}
	c := &client{id: clientID, conn: conn, send: make(chan []byte, sendBuffer)}
	select {
	case h.register <- c:
	case <-h.done:
		_ = conn.Close()
		return
	}
	go h.writePump(c)
	h.readPump(c) // blocks until the connection ends
}

// ConnectedClients returns the number of connected clients.
func (h *WSHub) ConnectedClients() int { return int(h.count.Load()) }

// HandleDomainEvent is an EventBus handler: it converts the domain event to
// its contract form and broadcasts it. Unknown event types are not relayed.
func (h *WSHub) HandleDomainEvent(_ context.Context, event ports.DomainEvent) {
	msg, ok, err := EncodeEvent(event, time.Now().UTC())
	if err != nil {
		h.logger.Error("ws.encode_failed", zap.String("event_type", event.Type), zap.Error(err))
		return
	}
	if !ok {
		return
	}
	select {
	case h.broadcast <- msg:
	case <-h.done:
	default:
		h.logger.Warn("ws.broadcast_queue_full", zap.String("event_type", event.Type))
	}
}

// writePump is the only writer of c.conn: queued messages and pings.
func (h *WSHub) writePump(c *client) {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close() // unblocks readPump
	}()
	for {
		select {
		case msg, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok { // hub dropped the client
				_ = c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseGoingAway, ""))
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				h.logger.Debug("ws.write_error", zap.String("client_id", c.id), zap.Error(err))
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// readPump is the only reader: it enforces the pong deadline and the inbound
// size limit, discards data frames, and unregisters the client on exit.
func (h *WSHub) readPump(c *client) {
	defer func() {
		select {
		case h.unregister <- c:
		case <-h.done:
		}
		_ = c.conn.Close()
	}()
	c.conn.SetReadLimit(maxInboundSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error { return c.conn.SetReadDeadline(time.Now().Add(pongWait)) })
	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			return
		}
	}
}
