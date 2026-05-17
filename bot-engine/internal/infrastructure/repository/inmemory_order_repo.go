// Package repository provides in-memory implementations of domain repository ports.
// Use these for local development and testing; swap for PostgreSQL in production.
package repository

import (
	"context"
	"fmt"
	"sync"

	"github.com/google/uuid"

	"github.com/trading-bot/bot-engine/internal/domain/entities"
	"github.com/trading-bot/bot-engine/internal/domain/ports"
)

// InMemoryOrderRepo is a thread-safe, in-process order store.
// Implements ports.OrderRepositoryPort.
type InMemoryOrderRepo struct {
	mu     sync.RWMutex
	orders map[uuid.UUID]*entities.Order
}

// NewInMemoryOrderRepo creates an empty in-memory order repository.
func NewInMemoryOrderRepo() *InMemoryOrderRepo {
	return &InMemoryOrderRepo{orders: make(map[uuid.UUID]*entities.Order)}
}

var _ ports.OrderRepositoryPort = (*InMemoryOrderRepo)(nil)

func (r *InMemoryOrderRepo) Save(_ context.Context, o *entities.Order) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.orders[o.ID] = o
	return nil
}

func (r *InMemoryOrderRepo) FindByID(_ context.Context, id interface{}) (*entities.Order, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	uid, ok := id.(uuid.UUID)
	if !ok {
		return nil, fmt.Errorf("invalid id type %T", id)
	}
	o, exists := r.orders[uid]
	if !exists {
		return nil, fmt.Errorf("order %s not found", uid)
	}
	return o, nil
}

func (r *InMemoryOrderRepo) FindByStatus(_ context.Context, status entities.OrderStatus) ([]*entities.Order, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*entities.Order
	for _, o := range r.orders {
		if o.Status == status {
			result = append(result, o)
		}
	}
	return result, nil
}

func (r *InMemoryOrderRepo) FindBySymbol(_ context.Context, symbol string) ([]*entities.Order, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*entities.Order
	for _, o := range r.orders {
		if o.Symbol == symbol {
			result = append(result, o)
		}
	}
	return result, nil
}

func (r *InMemoryOrderRepo) Update(_ context.Context, o *entities.Order) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.orders[o.ID] = o
	return nil
}
