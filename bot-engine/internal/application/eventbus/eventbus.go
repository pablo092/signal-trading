// Package eventbus implements the Observer pattern for domain events.
//
// Components publish events without knowing who consumes them.
// Consumers subscribe to event types and are notified asynchronously.
//
// Usage:
//
//	bus := eventbus.New(logger)
//	bus.Subscribe("OrderPlaced", func(ctx context.Context, e ports.DomainEvent) {
//	    // notify Flutter clients via WebSocket
//	})
//	bus.Publish(ctx, ports.DomainEvent{Type: "OrderPlaced", Payload: order})
package eventbus

import (
	"context"
	"sync"

	"go.uber.org/zap"

	"github.com/trading-bot/bot-engine/internal/domain/ports"
)

// HandlerFunc is the callback invoked when a matching event is published.
type HandlerFunc func(ctx context.Context, event ports.DomainEvent)

// EventBus is an in-process pub/sub broker.
// Goroutine-safe. Handlers run in separate goroutines (non-blocking publish).
type EventBus struct {
	mu       sync.RWMutex
	handlers map[string][]HandlerFunc
	logger   *zap.Logger
}

// New creates an initialized EventBus.
func New(logger *zap.Logger) *EventBus {
	return &EventBus{
		handlers: make(map[string][]HandlerFunc),
		logger:   logger,
	}
}

// Subscribe registers a handler for the given event type.
// Multiple handlers per event type are supported.
func (b *EventBus) Subscribe(eventType string, handler HandlerFunc) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[eventType] = append(b.handlers[eventType], handler)
	b.logger.Debug("eventbus.subscribed", zap.String("event_type", eventType))
}

// Publish dispatches an event to all registered handlers.
// Each handler runs in its own goroutine — publish never blocks.
// Implements ports.EventPublisherPort.
func (b *EventBus) Publish(ctx context.Context, event ports.DomainEvent) error {
	b.mu.RLock()
	handlers := make([]HandlerFunc, len(b.handlers[event.Type]))
	copy(handlers, b.handlers[event.Type])
	b.mu.RUnlock()

	if len(handlers) == 0 {
		b.logger.Debug("eventbus.no_handlers", zap.String("event_type", event.Type))
		return nil
	}

	b.logger.Debug("eventbus.publishing",
		zap.String("event_type", event.Type),
		zap.Int("handler_count", len(handlers)),
	)

	for _, h := range handlers {
		h := h // capture for goroutine
		go func() {
			defer func() {
				if r := recover(); r != nil {
					b.logger.Error("eventbus.handler_panic",
						zap.String("event_type", event.Type),
						zap.Any("panic", r),
					)
				}
			}()
			h(ctx, event)
		}()
	}
	return nil
}

// SubscribeAll registers a handler that receives every event type.
// Useful for audit logging or metrics collectors.
func (b *EventBus) SubscribeAll(handler HandlerFunc) {
	b.Subscribe("*", handler)
}
