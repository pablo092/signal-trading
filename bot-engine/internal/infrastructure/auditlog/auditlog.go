// Package auditlog holds what every ports.AuditLogPort shares: event
// normalization, the canonical payload hash, and the in-memory/log store used
// when no database is configured (BL-14).
package auditlog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/trading-bot/bot-engine/internal/domain/ports"
	"github.com/trading-bot/bot-engine/internal/infrastructure/reqid"
)

// Normalize fills the timestamp, actor and request ID from ctx when missing.
func Normalize(ctx context.Context, e ports.AuditEvent) ports.AuditEvent {
	if e.OccurredAt.IsZero() {
		e.OccurredAt = time.Now().UTC()
	}
	if e.Actor.ID == "" {
		e.Actor = ports.ActorFrom(ctx)
	}
	if e.RequestID == "" {
		e.RequestID = reqid.FromContext(ctx)
	}
	if e.Payload == nil {
		e.Payload = map[string]any{}
	}
	return e
}

// Canonical returns the payload as JSON (map keys sorted by encoding/json)
// and its SHA-256, so a stored payload can be checked for tampering.
func Canonical(payload map[string]any) ([]byte, string, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, "", fmt.Errorf("audit payload: %w", err)
	}
	sum := sha256.Sum256(b)
	return b, hex.EncodeToString(sum[:]), nil
}

// Memory keeps events in process and writes each one to the log. It is the
// audit store when DATABASE_URL is unset: events survive only in log storage.
type Memory struct {
	mu     sync.Mutex
	events []ports.AuditEvent
	logger *zap.Logger
	max    int
	lastID int64 // monotonic, so IDs stay unique after old events are dropped
}

var _ ports.AuditLogPort = (*Memory)(nil)

// NewMemory returns a log-backed audit store that keeps the last 1000 events.
func NewMemory(logger *zap.Logger) *Memory { return &Memory{logger: logger, max: 1000} }

// Record logs and keeps e.
func (m *Memory) Record(ctx context.Context, e ports.AuditEvent) error {
	e = Normalize(ctx, e)
	payload, hash, err := Canonical(e.Payload)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.lastID++
	e.ID = m.lastID
	m.events = append(m.events, e)
	if len(m.events) > m.max {
		m.events = m.events[len(m.events)-m.max:]
	}
	m.mu.Unlock()
	m.logger.Info("audit.event",
		zap.String("action", e.Action), zap.String("result", e.Result), zap.String("target", e.Target),
		zap.String("actor_id", e.Actor.ID), zap.String("actor_type", e.Actor.Type),
		zap.String("request_id", e.RequestID), zap.ByteString("payload", payload), zap.String("payload_sha256", hash))
	return nil
}

// Events returns a copy of the kept events, oldest first.
func (m *Memory) Events() []ports.AuditEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]ports.AuditEvent(nil), m.events...)
}

// MemoryKillSwitch is the kill-switch store when DATABASE_URL is unset. The
// flag does not survive a restart; KILL_SWITCH=true is the durable override.
type MemoryKillSwitch struct {
	mu sync.Mutex
	s  ports.KillSwitchState
}

var _ ports.KillSwitchStore = (*MemoryKillSwitch)(nil)

// Load implements ports.KillSwitchStore.
func (k *MemoryKillSwitch) Load(context.Context) (ports.KillSwitchState, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.s, nil
}

// Save implements ports.KillSwitchStore.
func (k *MemoryKillSwitch) Save(_ context.Context, s ports.KillSwitchState) error {
	k.mu.Lock()
	k.s = s
	k.mu.Unlock()
	return nil
}
