package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/trading-bot/bot-engine/internal/domain/entities"
	"github.com/trading-bot/bot-engine/internal/domain/ports"
)

// ErrOrderNotFound is returned by FindByID and Update for an unknown order.
var ErrOrderNotFound = ports.ErrOrderNotFound

// OrderRepo is the Postgres ports.OrderRepositoryPort.
type OrderRepo struct{ pool *pgxpool.Pool }

var _ ports.OrderRepositoryPort = (*OrderRepo)(nil)

// NewOrderRepo returns an order repository on pool.
func NewOrderRepo(pool *pgxpool.Pool) *OrderRepo { return &OrderRepo{pool: pool} }

const orderColumns = `id, symbol, side, type, quantity::text, limit_price::text, status, broker_id,
	filled_qty::text, filled_price::text, filled_at, strategy_id, signal_id, created_at, updated_at`

// Save inserts a new order.
func (r *OrderRepo) Save(ctx context.Context, o *entities.Order) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO orders (id, symbol, side, type, quantity, limit_price, status, broker_id,
		filled_qty, filled_price, filled_at, strategy_id, signal_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5::numeric, $6::numeric, $7, $8, $9::numeric, $10::numeric, $11, $12, $13, $14, $15)`,
		o.ID, o.Symbol, string(o.Side), string(o.Type), o.Quantity.String(), decText(o.LimitPrice), string(o.Status),
		nullable(o.BrokerID), o.FilledQty.String(), decText(o.FilledPrice), o.FilledAt, o.StrategyID, o.SignalID,
		o.CreatedAt, o.UpdatedAt)
	if err != nil {
		return fmt.Errorf("postgres: save order: %w", err)
	}
	return nil
}

// Update replaces the mutable fields of an existing order.
func (r *OrderRepo) Update(ctx context.Context, o *entities.Order) error {
	tag, err := r.pool.Exec(ctx, `UPDATE orders SET status = $2, broker_id = $3, filled_qty = $4::numeric,
		filled_price = $5::numeric, filled_at = $6, updated_at = $7 WHERE id = $1`,
		o.ID, string(o.Status), nullable(o.BrokerID), o.FilledQty.String(), decText(o.FilledPrice), o.FilledAt, o.UpdatedAt)
	if err != nil {
		return fmt.Errorf("postgres: update order: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("postgres: update order %s: %w", o.ID, ErrOrderNotFound)
	}
	return nil
}

// FindByID returns the order with id (a uuid.UUID or its string form).
func (r *OrderRepo) FindByID(ctx context.Context, id interface{}) (*entities.Order, error) {
	var oid uuid.UUID
	switch v := id.(type) {
	case uuid.UUID:
		oid = v
	case string:
		parsed, err := uuid.Parse(v)
		if err != nil {
			return nil, fmt.Errorf("postgres: order id %q: %w", v, ErrOrderNotFound)
		}
		oid = parsed
	default:
		return nil, fmt.Errorf("postgres: unsupported order id type %T", id)
	}
	orders, err := r.query(ctx, "WHERE id = $1", oid)
	if err != nil {
		return nil, err
	}
	if len(orders) == 0 {
		return nil, fmt.Errorf("postgres: order %s: %w", oid, ErrOrderNotFound)
	}
	return orders[0], nil
}

// FindByStatus returns orders in status, oldest first.
func (r *OrderRepo) FindByStatus(ctx context.Context, status entities.OrderStatus) ([]*entities.Order, error) {
	return r.query(ctx, "WHERE status = $1 ORDER BY created_at", string(status))
}

// FindBySymbol returns orders for symbol, oldest first.
func (r *OrderRepo) FindBySymbol(ctx context.Context, symbol string) ([]*entities.Order, error) {
	return r.query(ctx, "WHERE symbol = $1 ORDER BY created_at", symbol)
}

func (r *OrderRepo) query(ctx context.Context, where string, args ...any) ([]*entities.Order, error) {
	rows, err := r.pool.Query(ctx, "SELECT "+orderColumns+" FROM orders "+where, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: query orders: %w", err)
	}
	out, err := pgx.CollectRows(rows, scanOrder)
	if err != nil {
		return nil, fmt.Errorf("postgres: scan orders: %w", err)
	}
	return out, nil
}

func scanOrder(row pgx.CollectableRow) (*entities.Order, error) {
	var (
		o                                 entities.Order
		side, typ, status, qty, filledQty string
		limit, filledPrice, brokerID      *string
		filledAt                          *time.Time
	)
	if err := row.Scan(&o.ID, &o.Symbol, &side, &typ, &qty, &limit, &status, &brokerID,
		&filledQty, &filledPrice, &filledAt, &o.StrategyID, &o.SignalID, &o.CreatedAt, &o.UpdatedAt); err != nil {
		return nil, err
	}
	var err error
	if o.Quantity, err = decimal.NewFromString(qty); err != nil {
		return nil, err
	}
	if o.FilledQty, err = decimal.NewFromString(filledQty); err != nil {
		return nil, err
	}
	if o.LimitPrice, err = decPtr(limit); err != nil {
		return nil, err
	}
	if o.FilledPrice, err = decPtr(filledPrice); err != nil {
		return nil, err
	}
	o.Side, o.Type, o.Status = entities.OrderSide(side), entities.OrderType(typ), entities.OrderStatus(status)
	if brokerID != nil {
		o.BrokerID = *brokerID
	}
	if filledAt != nil {
		t := filledAt.UTC()
		o.FilledAt = &t
	}
	o.CreatedAt, o.UpdatedAt = o.CreatedAt.UTC(), o.UpdatedAt.UTC()
	return &o, nil
}

func decText(d *decimal.Decimal) *string {
	if d == nil {
		return nil
	}
	s := d.String()
	return &s
}

func decPtr(s *string) (*decimal.Decimal, error) {
	if s == nil {
		return nil, nil
	}
	d, err := decimal.NewFromString(*s)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
