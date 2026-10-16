package worker

import (
	"context"
	"database/sql"
	"log/slog"
	"time"
)

// Retention prunes old rows on a nightly schedule. delivery_attempts
// go after AttemptDays; deliveries go after DeliveryDays *unless* their
// status is 'dead' — dead rows are the audit trail and never expire
// automatically.
type Retention struct {
	DB           *sql.DB
	AttemptDays  int
	DeliveryDays int
	RunAt        string // "HH:MM" in UTC, e.g. "03:00"
	Tick         time.Duration
}

func NewRetention(db *sql.DB) *Retention {
	return &Retention{
		DB:           db,
		AttemptDays:  30,
		DeliveryDays: 90,
		RunAt:        "03:00",
		Tick:         time.Hour,
	}
}

// Run blocks until ctx is done. Wakes every Tick and prunes only when
// the current UTC hour:minute matches RunAt.
func (r *Retention) Run(ctx context.Context) {
	t := time.NewTicker(r.Tick)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("retention stopping")
			return
		case <-t.C:
			now := time.Now().UTC()
			if now.Format("15:04") != r.RunAt {
				continue
			}
			if err := r.prune(ctx); err != nil {
				slog.Error("retention failed", "err", err)
			}
		}
	}
}

func (r *Retention) prune(ctx context.Context) error {
	attemptsCutoff := time.Now().UTC().AddDate(0, 0, -r.AttemptDays)
	deliveriesCutoff := time.Now().UTC().AddDate(0, 0, -r.DeliveryDays)

	resA, err := r.DB.ExecContext(ctx,
		`DELETE FROM delivery_attempts WHERE finished_at < $1`, attemptsCutoff)
	if err != nil {
		return err
	}
	nA, _ := resA.RowsAffected()

	resD, err := r.DB.ExecContext(ctx,
		`DELETE FROM deliveries
		  WHERE status <> 'dead' AND updated_at < $1`, deliveriesCutoff)
	if err != nil {
		return err
	}
	nD, _ := resD.RowsAffected()

	slog.Info("retention pruned",
		"attempts", nA, "deliveries", nD)
	return nil
}
