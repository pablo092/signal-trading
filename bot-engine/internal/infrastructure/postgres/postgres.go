// Package postgres holds the Postgres adapters: connection, embedded schema
// migrations, the order repository, the audit log and the kill-switch store (BL-14).
package postgres

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrationLockID serializes concurrent migrators (pg_advisory_lock key).
const migrationLockID = 4_210_014

// Open connects to url and checks the connection within 10 s. A non-empty
// schema becomes the only search_path entry and is created if missing, so
// every table, function and schema_migrations row lives there (DATABASE_SCHEMA).
// The caller validates schema (config.Validate); it is quoted here regardless.
func Open(ctx context.Context, url, schema string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("postgres: invalid DATABASE_URL: %w", redact(err))
	}
	cfg.MaxConns = 5
	if schema != "" {
		cfg.ConnConfig.RuntimeParams["search_path"] = pgx.Identifier{schema}.Sanitize()
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: connect: %w", redact(err))
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping: %w", redact(err))
	}
	if schema != "" {
		if _, err := pool.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+pgx.Identifier{schema}.Sanitize()); err != nil {
			pool.Close()
			return nil, fmt.Errorf("postgres: create schema: %w", redact(err))
		}
	}
	return pool, nil
}

// redact drops the error text when it might echo the connection string.
func redact(err error) error {
	if strings.Contains(err.Error(), "://") || strings.Contains(strings.ToLower(err.Error()), "password") {
		return fmt.Errorf("connection failed (details withheld: they may contain credentials)")
	}
	return err
}

type migration struct {
	version int
	name    string
	sql     string
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return nil, err
	}
	var out []migration
	seen := map[int]string{}
	for _, e := range entries {
		name := e.Name()
		prefix, _, ok := strings.Cut(name, "_")
		v, convErr := strconv.Atoi(prefix)
		if !ok || convErr != nil || v <= 0 || !strings.HasSuffix(name, ".sql") {
			return nil, fmt.Errorf("postgres: bad migration file name %q (want NNNN_name.sql)", name)
		}
		if other, dup := seen[v]; dup {
			return nil, fmt.Errorf("postgres: migrations %q and %q share version %d", other, name, v)
		}
		seen[v] = name
		body, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			return nil, err
		}
		out = append(out, migration{version: v, name: name, sql: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

// Migrate applies every embedded migration not yet recorded in
// schema_migrations, each in its own transaction, under an advisory lock.
// It returns the names of the migrations it applied.
func Migrate(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	migrations, err := loadMigrations()
	if err != nil {
		return nil, err
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres: acquire: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationLockID); err != nil {
		return nil, fmt.Errorf("postgres: migration lock: %w", err)
	}
	defer conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", migrationLockID) //nolint:errcheck

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER     PRIMARY KEY,
		name       TEXT        NOT NULL,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		return nil, fmt.Errorf("postgres: schema_migrations: %w", err)
	}

	applied := map[int]bool{}
	rows, err := conn.Query(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return nil, fmt.Errorf("postgres: read schema_migrations: %w", err)
	}
	versions, err := pgx.CollectRows(rows, pgx.RowTo[int32])
	if err != nil {
		return nil, fmt.Errorf("postgres: read schema_migrations: %w", err)
	}
	for _, v := range versions {
		applied[int(v)] = true
	}

	var done []string
	for _, m := range migrations {
		if applied[m.version] {
			continue
		}
		err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, m.sql); err != nil { // no args: simple protocol, multi-statement
				return err
			}
			_, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version, name) VALUES ($1, $2)", m.version, m.name)
			return err
		})
		if err != nil {
			return done, fmt.Errorf("postgres: migration %s: %w", m.name, err)
		}
		done = append(done, m.name)
	}
	return done, nil
}
