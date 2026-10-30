package store

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Opens a real Postgres. Skips unless TEST_DATABASE_URL is set, so plain
// `go test ./...` stays hermetic. The URL must point to a throwaway DB.
func openTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB-backed test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool
}

func resetSchema(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	// Drop everything the migration creates, plus the version table.
	_, err := pool.Exec(context.Background(), `
		DROP TABLE IF EXISTS delivery_attempts, deliveries,
		                     endpoint_subscriptions, endpoints,
		                     seen_events, schema_migrations CASCADE;
	`)
	if err != nil {
		t.Fatalf("reset schema: %v", err)
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	pool := openTestPool(t)
	resetSchema(t, pool)
	if err := Migrate(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return New(pool)
}

func TestMigrate_AppliesAndIsIdempotent(t *testing.T) {
	pool := openTestPool(t)
	resetSchema(t, pool)
	ctx := context.Background()

	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	if count != 1 {
		t.Fatalf("after first run: schema_migrations has %d rows, want 1", count)
	}

	// Second run must be a no-op, not an error.
	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("count schema_migrations (2): %v", err)
	}
	if count != 1 {
		t.Fatalf("after second run: schema_migrations has %d rows, want 1", count)
	}

	// Spot-check that the schema actually exists.
	for _, tbl := range []string{
		"endpoints", "endpoint_subscriptions", "deliveries",
		"delivery_attempts", "seen_events",
	} {
		var exists bool
		err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM information_schema.tables
				WHERE table_schema = 'public' AND table_name = $1
			)`, tbl).Scan(&exists)
		if err != nil {
			t.Fatalf("check table %s: %v", tbl, err)
		}
		if !exists {
			t.Errorf("table %s missing after Migrate", tbl)
		}
	}
}