package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/trading-bot/bot-engine/internal/domain/entities"
	"github.com/trading-bot/bot-engine/internal/domain/ports"
)

// IntentRepo is the Postgres ports.IntentRepositoryPort.
type IntentRepo struct{ pool *pgxpool.Pool }

var _ ports.IntentRepositoryPort = (*IntentRepo)(nil)

// NewIntentRepo returns an intent repository on pool.
func NewIntentRepo(pool *pgxpool.Pool) *IntentRepo { return &IntentRepo{pool: pool} }

// Create implements ports.IntentRepositoryPort.
func (r *IntentRepo) Create(ctx context.Context, i *entities.OrderIntent) error {
	doc, err := encodeIntent(i)
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx, `INSERT INTO order_intents (id, status, actor_id, idempotency_key, signal_id,
		created_at, expires_at, updated_at, doc, creation_snapshot)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)`,
		i.ID, string(i.Status), i.CreatedBy.ID, i.IdempotencyKey, i.Signal.ID, i.CreatedAt, i.ExpiresAt, i.UpdatedAt, doc)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
		switch pgErr.ConstraintName {
		case "order_intents_idempotency_uq":
			return ports.ErrIdempotencyKeyExists
		case "order_intents_active_signal_uq":
			return ports.ErrActiveIntentForSignal
		}
	}
	if err != nil {
		return fmt.Errorf("postgres: create intent: %w", err)
	}
	return nil
}

// Get implements ports.IntentRepositoryPort.
func (r *IntentRepo) Get(ctx context.Context, id uuid.UUID) (*entities.OrderIntent, error) {
	return r.one(ctx, "SELECT doc FROM order_intents WHERE id = $1", id)
}

// FindByIdempotencyKey implements ports.IntentRepositoryPort.
func (r *IntentRepo) FindByIdempotencyKey(ctx context.Context, actorID, key string) (*entities.OrderIntent, error) {
	return r.one(ctx, "SELECT creation_snapshot FROM order_intents WHERE actor_id = $1 AND idempotency_key = $2", actorID, key)
}

// List implements ports.IntentRepositoryPort.
func (r *IntentRepo) List(ctx context.Context, f ports.IntentFilter) ([]*entities.OrderIntent, error) {
	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	if f.Status != "" {
		return r.many(ctx, "SELECT doc FROM order_intents WHERE status = $1 ORDER BY created_at DESC LIMIT $2", string(f.Status), limit)
	}
	return r.many(ctx, "SELECT doc FROM order_intents ORDER BY created_at DESC LIMIT $1", limit)
}

// Update implements ports.IntentRepositoryPort: a compare-and-set on status.
func (r *IntentRepo) Update(ctx context.Context, i *entities.OrderIntent, from entities.IntentStatus) error {
	doc, err := encodeIntent(i)
	if err != nil {
		return err
	}
	tag, err := r.pool.Exec(ctx, `UPDATE order_intents SET status = $2, updated_at = $3, doc = $4
		WHERE id = $1 AND status = $5`, i.ID, string(i.Status), i.UpdatedAt, doc, string(from))
	if err != nil {
		return fmt.Errorf("postgres: update intent: %w", err)
	}
	if tag.RowsAffected() == 0 {
		if _, gerr := r.Get(ctx, i.ID); errors.Is(gerr, ports.ErrIntentNotFound) {
			return ports.ErrIntentNotFound
		}
		return ports.ErrStaleIntent
	}
	return nil
}

// ExpiredPending implements ports.IntentRepositoryPort.
func (r *IntentRepo) ExpiredPending(ctx context.Context, now time.Time) ([]*entities.OrderIntent, error) {
	return r.many(ctx, "SELECT doc FROM order_intents WHERE status = 'pending_approval' AND expires_at <= $1 ORDER BY expires_at", now)
}

func (r *IntentRepo) one(ctx context.Context, q string, args ...any) (*entities.OrderIntent, error) {
	var doc []byte
	if err := r.pool.QueryRow(ctx, q, args...).Scan(&doc); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ports.ErrIntentNotFound
		}
		return nil, fmt.Errorf("postgres: get intent: %w", err)
	}
	return decodeIntent(doc)
}

func (r *IntentRepo) many(ctx context.Context, q string, args ...any) ([]*entities.OrderIntent, error) {
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: query intents: %w", err)
	}
	docs, err := pgx.CollectRows(rows, pgx.RowTo[[]byte])
	if err != nil {
		return nil, fmt.Errorf("postgres: scan intents: %w", err)
	}
	out := make([]*entities.OrderIntent, 0, len(docs))
	for _, d := range docs {
		i, err := decodeIntent(d)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, nil
}

// intentDoc is the stored JSON form. Decimals are strings: no float rounding.
type intentDoc struct {
	ID                uuid.UUID           `json:"id"`
	Status            string              `json:"status"`
	Symbol            string              `json:"symbol"`
	Strategy          string              `json:"strategy"`
	Side              string              `json:"side"`
	Qty               string              `json:"qty"`
	EstimatedNotional string              `json:"estimated_notional"`
	Signal            signalDoc           `json:"signal"`
	CreatedBy         entities.Actor      `json:"created_by"`
	IdempotencyKey    string              `json:"idempotency_key"`
	RequestHash       string              `json:"request_hash"`
	CreatedAt         time.Time           `json:"created_at"`
	UpdatedAt         time.Time           `json:"updated_at"`
	ExpiresAt         time.Time           `json:"expires_at"`
	DecidedBy         *entities.Actor     `json:"decided_by"`
	DecidedAt         *time.Time          `json:"decided_at"`
	DecisionReason    string              `json:"decision_reason"`
	RejectionCode     string              `json:"rejection_code"`
	Risk              entities.RiskReport `json:"risk"`
}

type signalDoc struct {
	ID          string    `json:"id"`
	Direction   string    `json:"direction"`
	Confidence  float64   `json:"confidence"`
	Price       string    `json:"price"`
	GeneratedAt time.Time `json:"generated_at"`
	Reason      string    `json:"reason,omitempty"`
}

func encodeIntent(i *entities.OrderIntent) ([]byte, error) {
	b, err := json.Marshal(intentDoc{
		ID: i.ID, Status: string(i.Status), Symbol: i.Symbol, Strategy: i.Strategy, Side: string(i.Side),
		Qty: i.Qty.String(), EstimatedNotional: i.EstimatedNotional.String(),
		Signal: signalDoc{ID: i.Signal.ID, Direction: i.Signal.Direction, Confidence: i.Signal.Confidence,
			Price: i.Signal.Price.String(), GeneratedAt: i.Signal.GeneratedAt, Reason: i.Signal.Reason},
		CreatedBy: i.CreatedBy, IdempotencyKey: i.IdempotencyKey, RequestHash: i.RequestHash,
		CreatedAt: i.CreatedAt, UpdatedAt: i.UpdatedAt, ExpiresAt: i.ExpiresAt,
		DecidedBy: i.DecidedBy, DecidedAt: i.DecidedAt, DecisionReason: i.DecisionReason,
		RejectionCode: string(i.RejectionCode), Risk: i.Risk,
	})
	if err != nil {
		return nil, fmt.Errorf("postgres: encode intent: %w", err)
	}
	return b, nil
}

func decodeIntent(b []byte) (*entities.OrderIntent, error) {
	var d intentDoc
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("postgres: decode intent: %w", err)
	}
	dec := func(s string) (decimal.Decimal, error) {
		if s == "" {
			return decimal.Zero, nil
		}
		return decimal.NewFromString(s)
	}
	qty, err := dec(d.Qty)
	if err != nil {
		return nil, fmt.Errorf("postgres: decode intent qty: %w", err)
	}
	notional, err := dec(d.EstimatedNotional)
	if err != nil {
		return nil, fmt.Errorf("postgres: decode intent notional: %w", err)
	}
	price, err := dec(d.Signal.Price)
	if err != nil {
		return nil, fmt.Errorf("postgres: decode signal price: %w", err)
	}
	return &entities.OrderIntent{
		ID: d.ID, Status: entities.IntentStatus(d.Status), Symbol: d.Symbol, Strategy: d.Strategy,
		Side: entities.OrderSide(d.Side), Qty: qty, EstimatedNotional: notional,
		Signal: entities.SignalSnapshot{ID: d.Signal.ID, Direction: d.Signal.Direction, Confidence: d.Signal.Confidence,
			Price: price, GeneratedAt: d.Signal.GeneratedAt, Reason: d.Signal.Reason},
		CreatedBy: d.CreatedBy, IdempotencyKey: d.IdempotencyKey, RequestHash: d.RequestHash,
		CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt, ExpiresAt: d.ExpiresAt,
		DecidedBy: d.DecidedBy, DecidedAt: d.DecidedAt, DecisionReason: d.DecisionReason,
		RejectionCode: entities.RejectionCode(d.RejectionCode), Risk: d.Risk,
	}, nil
}
