package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/trading-bot/bot-engine/internal/domain/entities"
	"github.com/trading-bot/bot-engine/internal/domain/ports"
)

func pgIntent(t *testing.T, signalID, key string, now time.Time) *entities.OrderIntent {
	t.Helper()
	it, err := entities.NewOrderIntent("AAPL", "rsi", entities.SignalSnapshot{
		ID: signalID, Direction: "BUY", Confidence: 0.8, Price: decimal.RequireFromString("150.25"), GeneratedAt: now,
	}, entities.Actor{ID: "mcp-gateway", Type: "agent"}, key, "hash", now, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	it.Side, it.Qty, it.EstimatedNotional = entities.OrderSideBuy, decimal.RequireFromString("6.655574"), decimal.RequireFromString("999.9999")
	it.Risk = entities.RiskReport{Passed: true, Stage: entities.RiskStageCreation, EvaluatedAt: now.UTC(),
		Checks: []entities.RiskCheck{{Name: "kill_switch", Passed: true, Detail: "released"}}}
	next, err := it.Transition(entities.IntentPendingApproval, now)
	if err != nil {
		t.Fatal(err)
	}
	return next
}

func TestIntentRepoRoundTripAndCAS(t *testing.T) {
	ctx := context.Background()
	repo := NewIntentRepo(testPool(t))
	now := time.Now().UTC().Truncate(time.Microsecond)
	it := pgIntent(t, "sig-1", "key-00000001", now)
	if err := repo.Create(ctx, it); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != entities.IntentPendingApproval || !got.Qty.Equal(it.Qty) || !got.Signal.Price.Equal(it.Signal.Price) ||
		got.CreatedBy != it.CreatedBy || len(got.Risk.Checks) != 1 || !got.ExpiresAt.Equal(it.ExpiresAt) {
		t.Fatalf("round trip lost data: %+v", got)
	}

	approved, _ := got.Decide(entities.IntentApproved, entities.Actor{ID: "owner", Type: "human"}, "ok", now)
	if err := repo.Update(ctx, approved, entities.IntentPendingApproval); err != nil {
		t.Fatal(err)
	}
	// A second writer still holding the old status loses the compare-and-set.
	rejected, _ := got.Reject(entities.RejectionByApprover, "no", now)
	if err := repo.Update(ctx, rejected, entities.IntentPendingApproval); !errors.Is(err, ports.ErrStaleIntent) {
		t.Fatalf("stale update: %v", err)
	}
	after, _ := repo.Get(ctx, it.ID)
	if after.Status != entities.IntentApproved || after.DecidedBy == nil || after.DecidedBy.ID != "owner" {
		t.Fatalf("after = %+v", after)
	}

	// The idempotency lookup returns the creation snapshot, not the current state.
	snap, err := repo.FindByIdempotencyKey(ctx, "mcp-gateway", "key-00000001")
	if err != nil || snap.Status != entities.IntentPendingApproval {
		t.Fatalf("snapshot = %+v err=%v", snap, err)
	}
	if _, err := repo.Get(ctx, uuid.New()); !errors.Is(err, ports.ErrIntentNotFound) {
		t.Fatalf("missing intent: %v", err)
	}
}

func TestIntentRepoUniqueness(t *testing.T) {
	ctx := context.Background()
	repo := NewIntentRepo(testPool(t))
	now := time.Now().UTC()
	first := pgIntent(t, "sig-1", "key-00000001", now)
	if err := repo.Create(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, pgIntent(t, "sig-2", "key-00000001", now)); !errors.Is(err, ports.ErrIdempotencyKeyExists) {
		t.Fatalf("same key: %v", err)
	}
	if err := repo.Create(ctx, pgIntent(t, "sig-1", "key-00000002", now)); !errors.Is(err, ports.ErrActiveIntentForSignal) {
		t.Fatalf("same active signal: %v", err)
	}
	// Once the first is terminal, the signal may be proposed again.
	canceled, _ := first.Decide(entities.IntentCanceled, first.CreatedBy, "", now)
	if err := repo.Update(ctx, canceled, entities.IntentPendingApproval); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, pgIntent(t, "sig-1", "key-00000003", now)); err != nil {
		t.Fatalf("terminal intent must free the signal: %v", err)
	}
}

func TestIntentRepoListAndExpiry(t *testing.T) {
	ctx := context.Background()
	repo := NewIntentRepo(testPool(t))
	now := time.Now().UTC()
	old := pgIntent(t, "sig-old", "key-00000001", now.Add(-10*time.Minute))
	fresh := pgIntent(t, "sig-new", "key-00000002", now)
	for _, it := range []*entities.OrderIntent{old, fresh} {
		if err := repo.Create(ctx, it); err != nil {
			t.Fatal(err)
		}
	}
	all, err := repo.List(ctx, ports.IntentFilter{Limit: 10})
	if err != nil || len(all) != 2 || all[0].ID != fresh.ID {
		t.Fatalf("list newest first: %v err=%v", all, err)
	}
	due, err := repo.ExpiredPending(ctx, now)
	if err != nil || len(due) != 1 || due[0].ID != old.ID {
		t.Fatalf("expired pending = %v err=%v", due, err)
	}
	none, err := repo.List(ctx, ports.IntentFilter{Status: entities.IntentFilled, Limit: 10})
	if err != nil || len(none) != 0 {
		t.Fatalf("status filter: %v err=%v", none, err)
	}
}
