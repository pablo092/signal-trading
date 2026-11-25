package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/trading-bot/bot-engine/internal/domain/ports"
)

// KillSwitchStore persists the kill-switch flag in its single-row table.
type KillSwitchStore struct{ pool *pgxpool.Pool }

var _ ports.KillSwitchStore = (*KillSwitchStore)(nil)

// NewKillSwitchStore returns a kill-switch store on pool.
func NewKillSwitchStore(pool *pgxpool.Pool) *KillSwitchStore { return &KillSwitchStore{pool: pool} }

// Load reads the flag.
func (k *KillSwitchStore) Load(ctx context.Context) (ports.KillSwitchState, error) {
	var s ports.KillSwitchState
	err := k.pool.QueryRow(ctx, "SELECT engaged, reason, updated_by, updated_at FROM kill_switch WHERE id = 1").
		Scan(&s.Engaged, &s.Reason, &s.UpdatedBy, &s.UpdatedAt)
	if err != nil {
		return ports.KillSwitchState{}, fmt.Errorf("postgres: load kill switch: %w", err)
	}
	s.UpdatedAt = s.UpdatedAt.UTC()
	return s, nil
}

// Save writes the flag.
func (k *KillSwitchStore) Save(ctx context.Context, s ports.KillSwitchState) error {
	tag, err := k.pool.Exec(ctx, "UPDATE kill_switch SET engaged = $1, reason = $2, updated_by = $3, updated_at = $4 WHERE id = 1",
		s.Engaged, s.Reason, s.UpdatedBy, s.UpdatedAt)
	if err != nil {
		return fmt.Errorf("postgres: save kill switch: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("postgres: save kill switch: row missing (migration 0002 not applied?)")
	}
	return nil
}
