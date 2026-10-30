package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// DeliveryJob is everything the worker needs to attempt one delivery.
type DeliveryJob struct {
	DeliveryID string
	EventID    string
	EndpointID string
	UserID     string
	EventType  string
	Payload    json.RawMessage
	Attempts   int // attempts already made before this one
	TargetURL  string
	Secret     string
}

// GetDeliveryForWork loads the delivery joined with its endpoint. Returns
// ErrNotFound when the delivery does not exist or its endpoint was deleted
// (cascade).
func (s *Store) GetDeliveryForWork(ctx context.Context, deliveryID string) (*DeliveryJob, error) {
	var j DeliveryJob
	var payload []byte
	err := s.pool.QueryRow(ctx, `
		SELECT d.id, d.event_id, d.endpoint_id, d.user_id, d.event_type,
		       d.payload, d.attempts,
		       e.url, e.secret
		  FROM deliveries d
		  JOIN endpoints e ON e.id = d.endpoint_id
		 WHERE d.id = $1`, deliveryID).
		Scan(&j.DeliveryID, &j.EventID, &j.EndpointID, &j.UserID, &j.EventType,
			&payload, &j.Attempts, &j.TargetURL, &j.Secret)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get delivery: %w", err)
	}
	j.Payload = json.RawMessage(payload)
	return &j, nil
}

// MarkInFlight atomically transitions pending|queued -> in_flight and
// returns the new attempt number. Returns (0, false, nil) when the row
// is not in a claimable state (duplicate message, terminal status, or
// already claimed by another worker). The caller must XACK and skip.
func (s *Store) MarkInFlight(ctx context.Context, deliveryID string) (int, bool, error) {
	var attempts int
	err := s.pool.QueryRow(ctx, `
		UPDATE deliveries
		   SET status = 'in_flight',
		       attempts = attempts + 1,
		       updated_at = now()
		 WHERE id = $1
		   AND status IN ('pending', 'queued')
		 RETURNING attempts`, deliveryID).Scan(&attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("mark in_flight: %w", err)
	}
	return attempts, true, nil
}

// Attempt records one delivery attempt.
type Attempt struct {
	DeliveryID    string
	AttemptNumber int
	StartedAt     time.Time
	FinishedAt    time.Time
	StatusCode    int
	Err           string
	DurationMs    int
}

func (s *Store) RecordAttempt(ctx context.Context, a Attempt) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO delivery_attempts (
			delivery_id, attempt_number, started_at, finished_at,
			status_code, error, duration_ms
		) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		a.DeliveryID, a.AttemptNumber, a.StartedAt, a.FinishedAt,
		nullableInt(a.StatusCode), nullableString(a.Err), a.DurationMs)
	if err != nil {
		return fmt.Errorf("record attempt: %w", err)
	}
	return nil
}

func (s *Store) MarkSucceeded(ctx context.Context, deliveryID string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE deliveries SET status = 'succeeded', updated_at = now()
		 WHERE id = $1`, deliveryID)
	return err
}

func (s *Store) MarkPendingRetry(ctx context.Context, deliveryID string, nextAttemptAt time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE deliveries
		   SET status = 'pending', next_attempt_at = $2, updated_at = now()
		 WHERE id = $1`, deliveryID, nextAttemptAt)
	return err
}

func (s *Store) MarkDead(ctx context.Context, deliveryID string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE deliveries SET status = 'dead', updated_at = now()
		 WHERE id = $1`, deliveryID)
	return err
}

// nullableInt and nullableString build pgtype for optional columns.
func nullableInt(v int) any {
	if v == 0 {
		return nil
	}
	return v
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// DeliveryFilter scopes ListDeliveries. Zero values mean "no filter".
type DeliveryFilter struct {
	EndpointID string
	Status     string
	EventType  string
	From       *time.Time
	To         *time.Time
	Cursor     string // last ID from previous page
	Limit      int    // 0 → 50, capped at 200
}

// Delivery is one row of the audit log. Payload and secret are omitted
// on purpose — this is read-only metadata.
type Delivery struct {
	ID            string
	EventID       string
	EndpointID    string
	EventType     string
	Status        string
	Attempts      int
	NextAttemptAt *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// ListDeliveries returns a page of the user's deliveries, newest first.
// Cursor pagination on UUIDv7 keeps the ordering stable and time-ordered.
func (s *Store) ListDeliveries(ctx context.Context, userID string, f DeliveryFilter) ([]Delivery, error) {
	var (
		where []string
		args  []any
	)
	args = append(args, userID)
	where = append(where, fmt.Sprintf("user_id = $%d", len(args)))

	if f.EndpointID != "" {
		args = append(args, f.EndpointID)
		where = append(where, fmt.Sprintf("endpoint_id = $%d", len(args)))
	}
	if f.Status != "" {
		args = append(args, f.Status)
		where = append(where, fmt.Sprintf("status = $%d", len(args)))
	}
	if f.EventType != "" {
		args = append(args, f.EventType)
		where = append(where, fmt.Sprintf("event_type = $%d", len(args)))
	}
	if f.From != nil {
		args = append(args, *f.From)
		where = append(where, fmt.Sprintf("created_at >= $%d", len(args)))
	}
	if f.To != nil {
		args = append(args, *f.To)
		where = append(where, fmt.Sprintf("created_at <= $%d", len(args)))
	}
	if f.Cursor != "" {
		args = append(args, f.Cursor)
		where = append(where, fmt.Sprintf("id < $%d", len(args)))
	}

	limit := effectiveLimit(f.Limit)
	// Fetch one extra row to detect whether there is a next page.
	args = append(args, limit+1)

	query := `
		SELECT id, event_id, endpoint_id, event_type, status, attempts,
		       next_attempt_at, created_at, updated_at
		  FROM deliveries
		 WHERE ` + strings.Join(where, " AND ") + `
		 ORDER BY id DESC
		 LIMIT $` + strconv.Itoa(len(args))

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query deliveries: %w", err)
	}
	defer rows.Close()

	var out []Delivery
	for rows.Next() {
		var (
			d    Delivery
			next pgtype.Timestamptz
		)
		if err := rows.Scan(&d.ID, &d.EventID, &d.EndpointID, &d.EventType,
			&d.Status, &d.Attempts, &next, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		if next.Valid {
			t := next.Time
			d.NextAttemptAt = &t
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Drop the sentinel row (used only to detect more pages).
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func effectiveLimit(l int) int {
	if l <= 0 {
		return 50
	}
	if l > 200 {
		return 200
	}
	return l
}