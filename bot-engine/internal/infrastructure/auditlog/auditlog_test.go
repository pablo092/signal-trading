package auditlog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"go.uber.org/zap"

	"github.com/trading-bot/bot-engine/internal/domain/ports"
	"github.com/trading-bot/bot-engine/internal/infrastructure/reqid"
)

func TestNormalizeFillsActorTimeAndRequestIDFromContext(t *testing.T) {
	ctx := reqid.WithContext(ports.WithActor(context.Background(), ports.Actor{ID: "oncall", Type: "human"}), "req-1")
	e := Normalize(ctx, ports.AuditEvent{Action: "order.placed"})
	if e.Actor.ID != "oncall" || e.RequestID != "req-1" || e.OccurredAt.IsZero() || e.Payload == nil {
		t.Fatalf("normalized = %+v", e)
	}
	if got := Normalize(context.Background(), ports.AuditEvent{}).Actor; got != ports.SystemActor {
		t.Fatalf("no actor in ctx must mean the system actor, got %+v", got)
	}
}

func TestCanonicalHashIsOverTheExactStoredBytes(t *testing.T) {
	// Map key order must not change the bytes, or the stored hash would not verify.
	a, ha, err := Canonical(map[string]any{"b": 1, "a": "x"})
	if err != nil {
		t.Fatal(err)
	}
	b, hb, _ := Canonical(map[string]any{"a": "x", "b": 1})
	if string(a) != string(b) || ha != hb {
		t.Fatalf("canonical form differs: %s vs %s", a, b)
	}
	sum := sha256.Sum256(a)
	if ha != hex.EncodeToString(sum[:]) {
		t.Fatal("hash is not sha256 of the payload bytes")
	}
	if _, _, err := Canonical(map[string]any{"bad": make(chan int)}); err == nil {
		t.Fatal("unencodable payload must fail, not be stored silently")
	}
}

func TestMemoryKeepsUniqueIDsAfterTrimming(t *testing.T) {
	m := NewMemory(zap.NewNop())
	m.max = 3
	for i := 0; i < 5; i++ {
		if err := m.Record(context.Background(), ports.AuditEvent{Action: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	ev := m.Events()
	if len(ev) != 3 || ev[0].ID != 3 || ev[2].ID != 5 {
		t.Fatalf("ids after trim = %v %v %v (len %d)", ev[0].ID, ev[1].ID, ev[2].ID, len(ev))
	}
}
