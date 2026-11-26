package postgres

// These tests need a disposable Postgres: set TEST_DATABASE_URL, e.g.
//   docker run -d --rm -p 127.0.0.1:55432:5432 -e POSTGRES_PASSWORD=test postgres:16.6-alpine
//   TEST_DATABASE_URL=postgres://postgres:test@127.0.0.1:55432/postgres?sslmode=disable
// Each test runs in a fresh schema, so they never touch existing data.

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/trading-bot/bot-engine/internal/domain/entities"
	"github.com/trading-bot/bot-engine/internal/domain/ports"
	"github.com/trading-bot/bot-engine/internal/infrastructure/auditlog"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	admin, err := Open(ctx, url, "")
	if err != nil {
		t.Fatal(err)
	}
	schema := "t_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return pool
}

func TestLoadMigrationsIsOrderedAndValid(t *testing.T) {
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) < 2 || ms[0].version != 1 || ms[1].version != 2 {
		t.Fatalf("migrations = %+v", ms)
	}
}

func TestRedactHidesConnectionStrings(t *testing.T) {
	err := redact(errors.New(`cannot parse "postgres://u:hunter2@h/db"`))
	if strings.Contains(err.Error(), "hunter2") {
		t.Fatal(err)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	pool := testPool(t)
	applied, err := Migrate(context.Background(), pool)
	if err != nil || len(applied) != 0 {
		t.Fatalf("second run applied %v, err %v", applied, err)
	}
}

func TestOrderRepoRoundTripKeepsDecimals(t *testing.T) {
	ctx := context.Background()
	repo := NewOrderRepo(testPool(t))
	o, _ := entities.NewLimitOrder("AAPL", entities.OrderSideBuy, decimal.RequireFromString("33.333333"),
		decimal.RequireFromString("150.123456789"), "rsi", "sig-1")
	if err := repo.Save(ctx, o); err != nil {
		t.Fatal(err)
	}
	got, err := repo.FindByID(ctx, o.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Quantity.Equal(o.Quantity) || !got.LimitPrice.Equal(*o.LimitPrice) || got.Status != entities.OrderStatusPending ||
		got.BrokerID != "" || got.FilledPrice != nil || !got.CreatedAt.Equal(o.CreatedAt.Truncate(time.Microsecond)) {
		t.Fatalf("round trip: got %+v, want %+v", got, o)
	}

	price, at := decimal.RequireFromString("150.02"), time.Now().UTC()
	filled := o.ApplyBrokerState(entities.BrokerState{BrokerID: "b-1", Status: entities.OrderStatusFilled,
		FilledQty: o.Quantity, FilledAvgPrice: &price, FilledAt: &at})
	if err := repo.Update(ctx, filled); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.FindByID(ctx, o.ID)
	if got.Status != entities.OrderStatusFilled || got.BrokerID != "b-1" || !got.FilledPrice.Equal(price) || got.FilledAt == nil {
		t.Fatalf("after update: %+v", got)
	}

	byStatus, _ := repo.FindByStatus(ctx, entities.OrderStatusFilled)
	bySymbol, _ := repo.FindBySymbol(ctx, "AAPL")
	if len(byStatus) != 1 || len(bySymbol) != 1 {
		t.Fatalf("byStatus=%d bySymbol=%d", len(byStatus), len(bySymbol))
	}
}

func TestOrderRepoErrors(t *testing.T) {
	ctx := context.Background()
	repo := NewOrderRepo(testPool(t))
	if _, err := repo.FindByID(ctx, uuid.New()); !errors.Is(err, ErrOrderNotFound) {
		t.Fatalf("missing: %v", err)
	}
	o, _ := entities.NewMarketOrder("AAPL", entities.OrderSideBuy, decimal.NewFromInt(1), "", "")
	if err := repo.Update(ctx, o); !errors.Is(err, ErrOrderNotFound) {
		t.Fatalf("update missing: %v", err)
	}
	if err := repo.Save(ctx, o); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, o); err == nil {
		t.Fatal("duplicate ID accepted")
	}
	bad := *o
	bad.ID, bad.Status = uuid.New(), "teleported"
	if err := repo.Save(ctx, &bad); err == nil {
		t.Fatal("unknown status accepted by the CHECK constraint")
	}
}

func TestAuditEventsAreAppendOnly(t *testing.T) {
	ctx := ports.WithActor(context.Background(), ports.Actor{ID: "owner", Type: "human"})
	pool := testPool(t)
	log := NewAuditLog(pool)
	if err := log.Record(ctx, ports.AuditEvent{Action: "order.submitted", Target: "o-1", Result: "ok",
		Payload: map[string]any{"qty": "1.5", "symbol": "AAPL"}}); err != nil {
		t.Fatal(err)
	}
	events, err := log.Recent(ctx, 10)
	if err != nil || len(events) != 1 {
		t.Fatalf("events=%v err=%v", events, err)
	}
	if e := events[0]; e.Actor.ID != "owner" || e.Payload["symbol"] != "AAPL" {
		t.Fatalf("event = %+v", e)
	}
	_, wantHash, _ := auditlog.Canonical(map[string]any{"qty": "1.5", "symbol": "AAPL"})
	var stored string
	_ = pool.QueryRow(ctx, "SELECT payload_sha256 FROM audit_events").Scan(&stored)
	if stored != wantHash {
		t.Fatalf("hash %s, want %s", stored, wantHash)
	}

	for _, stmt := range []string{"UPDATE audit_events SET result = 'tampered'", "DELETE FROM audit_events", "TRUNCATE audit_events"} {
		if _, err := pool.Exec(ctx, stmt); err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Fatalf("%s: want refusal, got %v", stmt, err)
		}
	}
}

func TestKillSwitchStore(t *testing.T) {
	ctx := context.Background()
	ks := NewKillSwitchStore(testPool(t))
	s, err := ks.Load(ctx)
	if err != nil || s.Engaged {
		t.Fatalf("initial = %+v, %v", s, err)
	}
	want := ports.KillSwitchState{Engaged: true, Reason: "test", UpdatedBy: "owner", UpdatedAt: time.Now().UTC().Truncate(time.Microsecond)}
	if err := ks.Save(ctx, want); err != nil {
		t.Fatal(err)
	}
	if got, _ := ks.Load(ctx); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestMigrateConcurrently(t *testing.T) {
	pool := testPool(t) // already migrated once; concurrent reruns must not fail or double-apply
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		go func() { _, err := Migrate(context.Background(), pool); errs <- err }()
	}
	for i := 0; i < 4; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	var n int
	_ = pool.QueryRow(context.Background(), "SELECT count(*) FROM schema_migrations").Scan(&n)
	if want, _ := loadMigrations(); n != len(want) {
		t.Fatalf("schema_migrations has %d rows, want %d", n, len(want))
	}
}
