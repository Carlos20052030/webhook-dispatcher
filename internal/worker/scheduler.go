package worker

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Carlos20052030/webhook-dispatcher/internal/queue"
)

// Scheduler moves due pending deliveries from Postgres onto the stream.
// It is the only component that publishes to the stream — workers only
// consume. Postgres remains the source of truth.
type Scheduler struct {
	DB        *pgxpool.Pool
	Queue     *queue.Client
	Stream    string
	Tick      time.Duration
	BatchSize int
}

func NewScheduler(db *pgxpool.Pool, q *queue.Client) *Scheduler {
	return &Scheduler{
		DB:        db,
		Queue:     q,
		Stream:    queue.DefaultStream,
		Tick:      5 * time.Second,
		BatchSize: 100,
	}
}

// Run blocks until ctx is done, ticking on the configured interval.
func (s *Scheduler) Run(ctx context.Context) {
	t := time.NewTicker(s.Tick)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("scheduler stopping")
			return
		case <-t.C:
			if n, err := s.tick(ctx); err != nil {
				slog.Error("scheduler tick failed", "err", err)
			} else if n > 0 {
				slog.Info("scheduler dispatched", "count", n)
			}
		}
	}
}

// tick processes at most BatchSize due rows and returns how many were
// successfully enqueued.
func (s *Scheduler) tick(ctx context.Context) (int, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, `
		SELECT id FROM deliveries
		 WHERE status = 'pending'
		   AND next_attempt_at IS NOT NULL
		   AND next_attempt_at <= now()
		 ORDER BY next_attempt_at
		 FOR UPDATE SKIP LOCKED
		 LIMIT $1`, s.BatchSize)
	if err != nil {
		return 0, fmt.Errorf("select due: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	if len(ids) == 0 {
		return 0, tx.Commit(ctx)
	}

	// Clear next_attempt_at so a re-tick does not pick these rows again
	// while we still hold the lock. If we crash after commit but before
	// XADD, the worker will pick them from the stream once they land —
	// but if XADD never happens, they'd be stuck. To avoid that, do the
	// XADD *inside* the transaction, then update status. If XADD fails,
	// the tx rolls back and the row stays pending for the next tick.
	enqueued := 0
	for _, id := range ids {
		if err := s.Queue.Publish(ctx, s.Stream, id); err != nil {
			// Leave the whole batch unmarked; next tick retries.
			return enqueued, fmt.Errorf("publish %s: %w", id, err)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE deliveries SET status = 'queued', updated_at = now()
			 WHERE id = $1`, id,
		); err != nil {
			return enqueued, fmt.Errorf("mark queued %s: %w", id, err)
		}
		enqueued++
	}

	if err := tx.Commit(ctx); err != nil {
		return enqueued, fmt.Errorf("commit: %w", err)
	}
	return enqueued, nil
}