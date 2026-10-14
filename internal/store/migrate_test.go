package store

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// Opens a real Postgres. Skips unless TEST_DATABASE_URL is set, so plain
// `go test ./...` stays hermetic. The URL must point to a throwaway DB.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping DB-backed test")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func resetSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	// Drop everything the migration creates, plus the version table.
	_, err := db.Exec(`
		DROP TABLE IF EXISTS delivery_attempts, deliveries,
		                     endpoint_subscriptions, endpoints,
		                     seen_events, schema_migrations CASCADE;
	`)
	if err != nil {
		t.Fatalf("reset schema: %v", err)
	}
}

func TestMigrate_AppliesAndIsIdempotent(t *testing.T) {
	db := openTestDB(t)
	resetSchema(t, db)
	ctx := context.Background()

	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	if count != 1 {
		t.Fatalf("after first run: schema_migrations has %d rows, want 1", count)
	}

	// Second run must be a no-op, not an error.
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&count); err != nil {
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
		err := db.QueryRow(`
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
