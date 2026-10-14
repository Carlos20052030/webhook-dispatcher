package store

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var ErrNotFound = errors.New("not found")

type Endpoint struct {
	ID            string
	UserID        string
	URL           string
	Secret        string
	Active        bool
	Subscriptions []string
	CreatedAt     time.Time
}

// CreateEndpoint inserts the endpoint and its subscriptions atomically.
// A partial insert would leave a webhook that never fires.
func (s *Store) CreateEndpoint(ctx context.Context, e *Endpoint) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO endpoints (id, user_id, url, secret, active, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		e.ID, e.UserID, e.URL, e.Secret, e.Active, e.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert endpoint: %w", err)
	}

	for _, evt := range e.Subscriptions {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO endpoint_subscriptions (endpoint_id, event_type)
			 VALUES ($1, $2)`, e.ID, evt,
		); err != nil {
			return fmt.Errorf("insert subscription %q: %w", evt, err)
		}
	}

	return tx.Commit()
}

// ListEndpointsByUser hydrates subscriptions in one query. Secret is
// returned: the API layer decides when to expose it.
func (s *Store) ListEndpointsByUser(ctx context.Context, userID string) ([]Endpoint, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT e.id, e.user_id, e.url, e.secret, e.active, e.created_at,
		       COALESCE(
		         array_agg(es.event_type) FILTER (WHERE es.event_type IS NOT NULL),
		         '{}'
		       )
		FROM endpoints e
		LEFT JOIN endpoint_subscriptions es ON es.endpoint_id = e.id
		WHERE e.user_id = $1
		GROUP BY e.id
		ORDER BY e.created_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("query endpoints: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Endpoint
	for rows.Next() {
		var e Endpoint
		if err := rows.Scan(&e.ID, &e.UserID, &e.URL, &e.Secret,
			&e.Active, &e.CreatedAt, &e.Subscriptions); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// DeleteEndpoint scopes the DELETE to userID. ErrNotFound is returned when
// the row is missing OR owned by another user, so the handler can 404 both
// without leaking ownership.
func (s *Store) DeleteEndpoint(ctx context.Context, userID, id string) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM endpoints WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("delete endpoint: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
