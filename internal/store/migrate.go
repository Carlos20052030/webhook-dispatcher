package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Carlos20052030/webhook-dispatcher/migrations"
	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// Migrate applies all pending migrations using golang-migrate.
// Safe to call on every boot.
// Uses a temporary database/sql connection for migrations since golang-migrate requires it.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	// Get the connection string from the pool config
	connStr := pool.Config().ConnString()

	// Create a temporary sql.DB for migrations
	db, err := sql.Open("pgx", connStr)
	if err != nil {
		return fmt.Errorf("open database/sql for migrations: %w", err)
	}
	defer db.Close()

	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("create iofs source: %w", err)
	}

	driver, err := postgres.WithInstance(db, &postgres.Config{})
	if err != nil {
		return fmt.Errorf("create postgres driver: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", src, "postgres", driver)
	if err != nil {
		return fmt.Errorf("create migrate instance: %w", err)
	}
	defer func() { _, _ = m.Close() }()

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("apply migrations: %w", err)
	}

	return nil
}