package store

import (
	"context"
	"errors"
	"fmt"
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

func TestListDeliveries_FiltersAndCursor(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	user := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	other := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"

	// Create one endpoint per user (once).
	if err := s.CreateEndpoint(ctx,
		sampleEndpoint("11111111-1111-1111-1111-111111111111", user)); err != nil {
		t.Fatalf("create user endpoint: %v", err)
	}
	if err := s.CreateEndpoint(ctx,
		sampleEndpoint("22222222-2222-2222-2222-222222222222", other)); err != nil {
		t.Fatalf("create other endpoint: %v", err)
	}

	// Ingest 5 events for user, 1 for other. Each creates one delivery.
	for i := 0; i < 5; i++ {
		if _, err := s.IngestEvent(ctx, EventToIngest{
			ID:         fmt.Sprintf("aaaaaaaa-0000-0000-0000-%012d", i),
			Type:       "bookmark.created",
			UserID:     user,
			OccurredAt: time.Now().UTC(),
			Payload:    []byte(`{}`),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.IngestEvent(ctx, EventToIngest{
		ID:         "bbbbbbbb-0000-0000-0000-000000000001",
		Type:       "bookmark.created",
		UserID:     other,
		OccurredAt: time.Now().UTC(),
		Payload:    []byte(`{}`),
	}); err != nil {
		t.Fatal(err)
	}

	// Page 1: limit 2.
	page1, err := s.ListDeliveries(ctx, user, DeliveryFilter{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page1) != 2 {
		t.Fatalf("page1: got %d, want 2", len(page1))
	}

	// Page 2: cursor from page 1.
	page2, err := s.ListDeliveries(ctx, user, DeliveryFilter{
		Limit:  2,
		Cursor: page1[len(page1)-1].ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) != 2 {
		t.Fatalf("page2: got %d, want 2", len(page2))
	}
	if page2[0].ID >= page1[len(page1)-1].ID {
		t.Errorf("cursor not applied: %s >= %s", page2[0].ID, page1[len(page1)-1].ID)
	}

	// Page 3: only 1 left.
	page3, err := s.ListDeliveries(ctx, user, DeliveryFilter{
		Limit:  2,
		Cursor: page2[len(page2)-1].ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page3) != 1 {
		t.Fatalf("page3: got %d, want 1", len(page3))
	}

	// Other user's delivery must never appear.
	for _, d := range append(append(page1, page2...), page3...) {
		if d.EventID == "bbbbbbbb-0000-0000-0000-000000000001" {
			t.Errorf("other user's delivery leaked")
		}
	}
}

func TestListDeliveries_StatusFilter(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	user := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"

	seedDelivery(t, s, user,
		"aaaaaaaa-0000-0000-0000-000000000020",
		"cccccccc-0000-0000-0000-000000000020")

	got, err := s.ListDeliveries(ctx, user, DeliveryFilter{Status: "succeeded"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("succeeded filter should be empty, got %d", len(got))
	}

	got, err = s.ListDeliveries(ctx, user, DeliveryFilter{Status: "pending"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Errorf("pending filter: got %d, want 1", len(got))
	}
}

func TestListDeliveries_LimitCapped(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	user := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"

	// Limit 1000 must not error; effective limit is 200.
	_, err := s.ListDeliveries(ctx, user, DeliveryFilter{Limit: 1000})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
