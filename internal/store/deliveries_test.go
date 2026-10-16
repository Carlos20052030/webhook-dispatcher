package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func seedDelivery(t *testing.T, s *Store, user, eventID, deliveryID string) {
	t.Helper()
	ctx := context.Background()

	ep := sampleEndpoint("11111111-1111-1111-1111-111111111111", user)
	if err := s.CreateEndpoint(ctx, ep); err != nil {
		t.Fatalf("create endpoint: %v", err)
	}
	if _, err := s.IngestEvent(ctx, EventToIngest{
		ID: eventID, Type: "bookmark.created", UserID: user,
		OccurredAt: time.Now().UTC(),
		Payload:    []byte(`{}`),
	}); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	// Ingest creates the delivery with its own id. Overwrite to control.
	if _, err := s.db.ExecContext(ctx,
		`UPDATE deliveries SET id = $1 WHERE event_id = $2`, deliveryID, eventID,
	); err != nil {
		t.Fatalf("rewrite delivery id: %v", err)
	}
}

func TestGetDeliveryForWork_NotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.GetDeliveryForWork(context.Background(),
		"99999999-9999-9999-9999-999999999999")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestMarkInFlight_OnlyFromPendingOrQueued(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	user := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"

	seedDelivery(t, s, user,
		"aaaaaaaa-0000-0000-0000-000000000010",
		"cccccccc-1111-1111-1111-111111111111")

	n, ok, err := s.MarkInFlight(ctx, "cccccccc-1111-1111-1111-111111111111")
	if err != nil {
		t.Fatal(err)
	}
	if !ok || n != 1 {
		t.Fatalf("first claim: ok=%v attempts=%d", ok, n)
	}

	// Second call must not claim it again.
	_, ok, err = s.MarkInFlight(ctx, "cccccccc-1111-1111-1111-111111111111")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatalf("second claim should fail: already in_flight")
	}
}

func TestMarkSucceeded(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	user := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	seedDelivery(t, s, user,
		"aaaaaaaa-0000-0000-0000-000000000011",
		"cccccccc-2222-2222-2222-222222222222")

	if _, _, err := s.MarkInFlight(ctx, "cccccccc-2222-2222-2222-222222222222"); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkSucceeded(ctx, "cccccccc-2222-2222-2222-222222222222"); err != nil {
		t.Fatal(err)
	}

	var status string
	if err := s.db.QueryRowContext(ctx,
		`SELECT status FROM deliveries WHERE id = $1`,
		"cccccccc-2222-2222-2222-222222222222").Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "succeeded" {
		t.Errorf("status: got %q", status)
	}
}

func TestRecordAttempt(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	user := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	seedDelivery(t, s, user,
		"aaaaaaaa-0000-0000-0000-000000000012",
		"cccccccc-3333-3333-3333-333333333333")

	now := time.Now().UTC()
	err := s.RecordAttempt(ctx, Attempt{
		DeliveryID:    "cccccccc-3333-3333-3333-333333333333",
		AttemptNumber: 1,
		StartedAt:     now.Add(-100 * time.Millisecond),
		FinishedAt:    now,
		StatusCode:    200,
		DurationMs:    100,
	})
	if err != nil {
		t.Fatal(err)
	}

	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM delivery_attempts WHERE delivery_id = $1`,
		"cccccccc-3333-3333-3333-333333333333").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("attempts: got %d, want 1", n)
	}
}
