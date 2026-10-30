package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Carlos20052030/webhook-dispatcher/internal/idgen"
)

// ErrDuplicate is returned when the event ID was already ingested.
var ErrDuplicate = errors.New("duplicate event")

// EventToIngest is the input of IngestEvent. Payload is stored as JSONB.
type EventToIngest struct {
	ID         string
	Type       string
	UserID     string
	OccurredAt time.Time
	Payload    json.RawMessage
}

// IngestEvent records the event in seen_events and creates one pending
// delivery per subscribed endpoint. Atomic: either everything lands or
// nothing does. Returns the number of deliveries created, or ErrDuplicate.
func (s *Store) IngestEvent(ctx context.Context, ev EventToIngest) (int, error) {
	tx, err := s.BeginTx(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	res, err := tx.Exec(ctx,
		`INSERT INTO seen_events (event_id) VALUES ($1) ON CONFLICT DO NOTHING`,
		ev.ID)
	if err != nil {
		return 0, fmt.Errorf("insert seen_events: %w", err)
	}
	n := res.RowsAffected()
	if n == 0 {
		return 0, ErrDuplicate
	}

	rows, err := tx.Query(ctx, `
		SELECT e.id
		FROM endpoints e
		JOIN endpoint_subscriptions es ON es.endpoint_id = e.id
		WHERE e.user_id = $1
		  AND e.active = true
		  AND es.event_type = $2`,
		ev.UserID, ev.Type)
	if err != nil {
		return 0, fmt.Errorf("query endpoints: %w", err)
	}

	var endpointIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		endpointIDs = append(endpointIDs, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	for _, epID := range endpointIDs {
		deliveryID, err := idgen.UUIDv7()
		if err != nil {
			return 0, fmt.Errorf("gen delivery id: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO deliveries (
				id, event_id, endpoint_id, user_id, event_type, payload,
				status, attempts, next_attempt_at
			) VALUES ($1, $2, $3, $4, $5, $6::jsonb, 'pending', 0, now())`,
			deliveryID, ev.ID, epID, ev.UserID, ev.Type, string(ev.Payload),
		); err != nil {
			return 0, fmt.Errorf("insert delivery for %s: %w", epID, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit: %w", err)
	}
	return len(endpointIDs), nil
}