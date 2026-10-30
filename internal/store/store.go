package store

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store owns runtime queries. Migrations are package-level: they run
// once at boot and do not belong to the request-scoped object.
type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Pool returns the underlying connection pool for migrations.
func (s *Store) Pool() *pgxpool.Pool {
	return s.pool
}

// BeginTx starts a new transaction.
func (s *Store) BeginTx(ctx context.Context) (Tx, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &pgxTx{tx: tx}, nil
}

// Tx is the transaction interface used by the store.
type Tx interface {
	Exec(ctx context.Context, sql string, args ...interface{}) (commandTag, error)
	Query(ctx context.Context, sql string, args ...interface{}) (Rows, error)
	QueryRow(ctx context.Context, sql string, args ...interface{}) Row
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

type pgxTx struct {
	tx pgx.Tx
}

func (t *pgxTx) Exec(ctx context.Context, sql string, args ...interface{}) (commandTag, error) {
	return t.tx.Exec(ctx, sql, args...)
}

func (t *pgxTx) Query(ctx context.Context, sql string, args ...interface{}) (Rows, error) {
	return t.tx.Query(ctx, sql, args...)
}

func (t *pgxTx) QueryRow(ctx context.Context, sql string, args ...interface{}) Row {
	return t.tx.QueryRow(ctx, sql, args...)
}

func (t *pgxTx) Commit(ctx context.Context) error {
	return t.tx.Commit(ctx)
}

func (t *pgxTx) Rollback(ctx context.Context) error {
	return t.tx.Rollback(ctx)
}

// Rows is the rows interface used by the store.
type Rows interface {
	Next() bool
	Scan(dest ...interface{}) error
	Close()
	Err() error
}

// Row is the row interface used by the store.
type Row interface {
	Scan(dest ...interface{}) error
}

// commandTag is a minimal interface for command results.
type commandTag interface {
	RowsAffected() int64
}