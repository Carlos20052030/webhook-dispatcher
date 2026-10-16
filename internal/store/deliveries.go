package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
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
	err := s.db.QueryRowContext(ctx, `
		SELECT d.id, d.event_id, d.endpoint_id, d.user_id, d.event_type,
		       d.payload, d.attempts,
		       e.url, e.secret
		  FROM deliveries d
		  JOIN endpoints e ON e.id = d.endpoint_id
		 WHERE d.id = $1`, deliveryID).
		Scan(&j.DeliveryID, &j.EventID, &j.EndpointID, &j.UserID, &j.EventType,
			&payload, &j.Attempts, &j.TargetURL, &j.Secret)
	if errors.Is(err, sql.ErrNoRows) {
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
	err := s.db.QueryRowContext(ctx, `
		UPDATE deliveries
		   SET status = 'in_flight',
		       attempts = attempts + 1,
		       updated_at = now()
		 WHERE id = $1
		   AND status IN ('pending', 'queued')
		 RETURNING attempts`, deliveryID).Scan(&attempts)
	if errors.Is(err, sql.ErrNoRows) {
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
	_, err := s.db.ExecContext(ctx, `
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
	_, err := s.db.ExecContext(ctx, `
		UPDATE deliveries SET status = 'succeeded', updated_at = now()
		 WHERE id = $1`, deliveryID)
	return err
}

func (s *Store) MarkPendingRetry(ctx context.Context, deliveryID string, nextAttemptAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE deliveries
		   SET status = 'pending', next_attempt_at = $2, updated_at = now()
		 WHERE id = $1`, deliveryID, nextAttemptAt)
	return err
}

func (s *Store) MarkDead(ctx context.Context, deliveryID string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE deliveries SET status = 'dead', updated_at = now()
		 WHERE id = $1`, deliveryID)
	return err
}

// nullableInt and nullableString build sql.Null for optional columns.
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
