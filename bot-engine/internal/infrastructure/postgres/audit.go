package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/trading-bot/bot-engine/internal/domain/ports"
	"github.com/trading-bot/bot-engine/internal/infrastructure/auditlog"
)

// AuditLog is the Postgres ports.AuditLogPort over the append-only audit_events table.
type AuditLog struct{ pool *pgxpool.Pool }

var _ ports.AuditLogPort = (*AuditLog)(nil)

// NewAuditLog returns an audit log on pool.
func NewAuditLog(pool *pgxpool.Pool) *AuditLog { return &AuditLog{pool: pool} }

// Record appends e. The payload hash is computed from its canonical JSON.
func (a *AuditLog) Record(ctx context.Context, e ports.AuditEvent) error {
	e = auditlog.Normalize(ctx, e)
	payload, hash, err := auditlog.Canonical(e.Payload)
	if err != nil {
		return err
	}
	_, err = a.pool.Exec(ctx, `INSERT INTO audit_events
		(occurred_at, actor_id, actor_type, action, target, result, request_id, payload, payload_sha256)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9)`,
		e.OccurredAt, e.Actor.ID, e.Actor.Type, e.Action, e.Target, e.Result, e.RequestID, string(payload), hash)
	if err != nil {
		return fmt.Errorf("postgres: record audit event: %w", err)
	}
	return nil
}

// Recent returns the latest n events, newest first (for tests and admin reads).
func (a *AuditLog) Recent(ctx context.Context, n int) ([]ports.AuditEvent, error) {
	rows, err := a.pool.Query(ctx, `SELECT id, occurred_at, actor_id, actor_type, action, target, result, request_id, payload
		FROM audit_events ORDER BY id DESC LIMIT $1`, n)
	if err != nil {
		return nil, fmt.Errorf("postgres: query audit events: %w", err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (ports.AuditEvent, error) {
		var e ports.AuditEvent
		err := row.Scan(&e.ID, &e.OccurredAt, &e.Actor.ID, &e.Actor.Type, &e.Action, &e.Target, &e.Result, &e.RequestID, &e.Payload)
		return e, err
	})
}
