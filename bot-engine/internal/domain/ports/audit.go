package ports

import (
	"context"
	"errors"
	"time"

	"github.com/trading-bot/bot-engine/internal/domain/entities"
)

// Actor is who caused an action: an authenticated principal or the engine itself.
// It is a domain type, so entities such as OrderIntent can record it.
type Actor = entities.Actor

// SystemActor is used for actions the engine takes on its own (startup, env override).
var SystemActor = Actor{ID: "system", Type: "system"}

type actorKey struct{}

// WithActor returns ctx carrying the actor responsible for the request.
func WithActor(ctx context.Context, a Actor) context.Context {
	return context.WithValue(ctx, actorKey{}, a)
}

// ActorFrom returns the actor carried by ctx, or SystemActor.
func ActorFrom(ctx context.Context) Actor {
	if a, ok := ctx.Value(actorKey{}).(Actor); ok && a.ID != "" {
		return a
	}
	return SystemActor
}

// AuditEvent is one append-only audit record (ADR-005 §5.8).
type AuditEvent struct {
	ID         int64
	OccurredAt time.Time
	Actor      Actor
	Action     string         // e.g. "order.submitted", "kill_switch.engaged"
	Target     string         // e.g. an order ID
	Result     string         // e.g. "ok", "rejected", "skipped", "failed"
	RequestID  string         // filled from ctx by the store when empty
	Payload    map[string]any // inputs used for the decision; never secrets
}

// AuditLogPort appends audit events. Implementations never update or delete.
type AuditLogPort interface {
	Record(ctx context.Context, e AuditEvent) error
}

// KillSwitchState is the persisted emergency-stop flag (ADR-005 §5.7).
type KillSwitchState struct {
	Engaged   bool
	Reason    string
	UpdatedBy string
	UpdatedAt time.Time
}

// KillSwitchStore persists the kill-switch flag.
type KillSwitchStore interface {
	Load(ctx context.Context) (KillSwitchState, error)
	Save(ctx context.Context, s KillSwitchState) error
}

// ErrKillSwitchEngaged: trading is halted; no order may be submitted.
var ErrKillSwitchEngaged = errors.New("kill switch engaged")
