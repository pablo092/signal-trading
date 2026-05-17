// Package messaging provides the WebSocket hub that broadcasts domain events
// to connected Flutter clients in real time.
package messaging

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"

	"github.com/trading-bot/bot-engine/internal/domain/ports"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true }, // allow all origins (tighten in prod)
}

// client represents a single connected WebSocket client.
type client struct {
	id   string
	conn *websocket.Conn
	send chan []byte
}

// WSHub manages all active WebSocket connections and broadcasts events.
type WSHub struct {
	mu      sync.RWMutex
	clients map[string]*client
	logger  *zap.Logger
}

// NewWSHub creates an initialised WebSocket hub.
func NewWSHub(logger *zap.Logger) *WSHub {
	return &WSHub{
		clients: make(map[string]*client),
		logger:  logger,
	}
}

// Run starts the hub's maintenance loop (ping/pong, cleanup).
// Must be called in a separate goroutine.
func (h *WSHub) Run(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			h.closeAll()
			return
		case <-ticker.C:
			h.ping()
		}
	}
}

// ServeWS upgrades an HTTP connection to WebSocket and registers the client.
func (h *WSHub) ServeWS(w http.ResponseWriter, r *http.Request, clientID string) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Error("ws.upgrade_failed", zap.String("client_id", clientID), zap.Error(err))
		return
	}

	c := &client{id: clientID, conn: conn, send: make(chan []byte, 64)}

	h.mu.Lock()
	h.clients[clientID] = c
	h.mu.Unlock()

	h.logger.Info("ws.client_connected",
		zap.String("client_id", clientID),
		zap.Int("total_clients", h.ConnectedClients()),
	)

	go h.writePump(c)
	h.readPump(c) // blocks until disconnected
}

// HandleDomainEvent is registered as an EventBus handler.
// It serialises the event and broadcasts it to all connected clients.
func (h *WSHub) HandleDomainEvent(ctx context.Context, event ports.DomainEvent) {
	payload, err := json.Marshal(map[string]interface{}{
		"type":    event.Type,
		"payload": event.Payload,
	})
	if err != nil {
		h.logger.Error("ws.marshal_failed", zap.String("event_type", event.Type), zap.Error(err))
		return
	}
	h.broadcast(payload)
}

// ConnectedClients returns the number of currently connected WebSocket clients.
func (h *WSHub) ConnectedClients() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

func (h *WSHub) broadcast(message []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, c := range h.clients {
		select {
		case c.send <- message:
		default:
			h.logger.Warn("ws.client_send_buffer_full", zap.String("client_id", c.id))
		}
	}
}

func (h *WSHub) writePump(c *client) {
	defer c.conn.Close()
	for msg := range c.send {
		if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
			h.logger.Debug("ws.write_error", zap.String("client_id", c.id), zap.Error(err))
			return
		}
	}
}

func (h *WSHub) readPump(c *client) {
	defer func() {
		h.mu.Lock()
		delete(h.clients, c.id)
		h.mu.Unlock()
		close(c.send)
		c.conn.Close()
		h.logger.Info("ws.client_disconnected", zap.String("client_id", c.id))
	}()

	c.conn.SetReadLimit(512)
	c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			return
		}
	}
}

func (h *WSHub) ping() {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, c := range h.clients {
		c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
			h.logger.Debug("ws.ping_failed", zap.String("client_id", c.id))
		}
	}
}

func (h *WSHub) closeAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.clients {
		c.conn.Close()
	}
}
