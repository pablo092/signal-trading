package repository

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/trading-bot/bot-engine/internal/domain/entities"
	"github.com/trading-bot/bot-engine/internal/domain/ports"
)

// InMemoryIntentRepo is the intent store when DATABASE_URL is unset. Intents
// and idempotency keys do not survive a restart.
type InMemoryIntentRepo struct {
	mu        sync.Mutex
	intents   map[uuid.UUID]*entities.OrderIntent
	snapshots map[string]*entities.OrderIntent // actorID + "\x00" + key -> creation snapshot
}

var _ ports.IntentRepositoryPort = (*InMemoryIntentRepo)(nil)

// NewInMemoryIntentRepo returns an empty store.
func NewInMemoryIntentRepo() *InMemoryIntentRepo {
	return &InMemoryIntentRepo{intents: map[uuid.UUID]*entities.OrderIntent{}, snapshots: map[string]*entities.OrderIntent{}}
}

func idemKey(actorID, key string) string { return actorID + "\x00" + key }

func clone(i *entities.OrderIntent) *entities.OrderIntent {
	c := *i
	c.Risk.Checks = append([]entities.RiskCheck(nil), i.Risk.Checks...)
	return &c
}

// Create implements ports.IntentRepositoryPort.
func (r *InMemoryIntentRepo) Create(_ context.Context, i *entities.OrderIntent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := idemKey(i.CreatedBy.ID, i.IdempotencyKey)
	if _, dup := r.snapshots[k]; dup {
		return ports.ErrIdempotencyKeyExists
	}
	if i.Status.Active() {
		for _, other := range r.intents {
			if other.Status.Active() && other.Signal.ID == i.Signal.ID {
				return fmt.Errorf("%w: %s", ports.ErrActiveIntentForSignal, other.ID)
			}
		}
	}
	r.intents[i.ID] = clone(i)
	r.snapshots[k] = clone(i)
	return nil
}

// Get implements ports.IntentRepositoryPort.
func (r *InMemoryIntentRepo) Get(_ context.Context, id uuid.UUID) (*entities.OrderIntent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	i, ok := r.intents[id]
	if !ok {
		return nil, ports.ErrIntentNotFound
	}
	return clone(i), nil
}

// FindByIdempotencyKey implements ports.IntentRepositoryPort.
func (r *InMemoryIntentRepo) FindByIdempotencyKey(_ context.Context, actorID, key string) (*entities.OrderIntent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	i, ok := r.snapshots[idemKey(actorID, key)]
	if !ok {
		return nil, ports.ErrIntentNotFound
	}
	return clone(i), nil
}

// List implements ports.IntentRepositoryPort.
func (r *InMemoryIntentRepo) List(_ context.Context, f ports.IntentFilter) ([]*entities.OrderIntent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*entities.OrderIntent, 0, len(r.intents))
	for _, i := range r.intents {
		if f.Status == "" || i.Status == f.Status {
			out = append(out, clone(i))
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].CreatedAt.After(out[b].CreatedAt) })
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, nil
}

// Update implements ports.IntentRepositoryPort (compare-and-set on status).
func (r *InMemoryIntentRepo) Update(_ context.Context, i *entities.OrderIntent, from entities.IntentStatus) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cur, ok := r.intents[i.ID]
	if !ok {
		return ports.ErrIntentNotFound
	}
	if cur.Status != from {
		return ports.ErrStaleIntent
	}
	r.intents[i.ID] = clone(i)
	return nil
}

// ExpiredPending implements ports.IntentRepositoryPort.
func (r *InMemoryIntentRepo) ExpiredPending(_ context.Context, now time.Time) ([]*entities.OrderIntent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []*entities.OrderIntent
	for _, i := range r.intents {
		if i.Expired(now) {
			out = append(out, clone(i))
		}
	}
	return out, nil
}
