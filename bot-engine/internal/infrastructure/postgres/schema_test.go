package postgres

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// DATABASE_SCHEMA (Supabase): every engine object lands in the dedicated schema.
func TestOpenWithDedicatedSchema(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	schema := "s_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	pool, err := Open(ctx, url, schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		pool.Close()
	})
	if _, err := Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables
		WHERE table_schema = $1 AND table_name IN ('orders', 'audit_events', 'order_intents', 'schema_migrations')`, schema).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Fatalf("want 4 engine tables in %s, got %d", schema, n)
	}
	// Opening again is idempotent (schema exists, migrations already applied).
	again, err := Open(ctx, url, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if applied, err := Migrate(ctx, again); err != nil || len(applied) != 0 {
		t.Fatalf("second migrate applied %v (err %v)", applied, err)
	}
}
