package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func ingestSample(id, userID, eventType string) EventToIngest {
	return EventToIngest{
		ID:         id,
		Type:       eventType,
		UserID:     userID,
		OccurredAt: time.Now().UTC(),
		Payload:    json.RawMessage(`{"url":"https://example.com","title":"Ex"}`),
	}
}

func TestIngestEvent_CreatesDeliveryPerSubscribedEndpoint(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	user := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"

	// Two endpoints subscribed to bookmark.created, one to another event.
	e1 := sampleEndpoint("11111111-1111-1111-1111-111111111111", user)
	e1.Subscriptions = []string{"bookmark.created"}
	e2 := sampleEndpoint("22222222-2222-2222-2222-222222222222", user)
	e2.Subscriptions = []string{"bookmark.created"}
	e3 := sampleEndpoint("33333333-3333-3333-3333-333333333333", user)
	e3.Subscriptions = []string{"other.event"}

	for _, e := range []*Endpoint{e1, e2, e3} {
		if err := s.CreateEndpoint(ctx, e); err != nil {
			t.Fatalf("create endpoint: %v", err)
		}
	}

	n, err := s.IngestEvent(ctx, ingestSample(
		"aaaaaaaa-0000-0000-0000-000000000001", user, "bookmark.created"))
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if n != 2 {
		t.Fatalf("deliveries created: got %d, want 2", n)
	}

	var count int
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM deliveries WHERE event_id = $1`,
		"aaaaaaaa-0000-0000-0000-000000000001").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("deliveries in DB: got %d, want 2", count)
	}
}

func TestIngestEvent_DuplicateReturnsErrDuplicate(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	user := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	e := sampleEndpoint("11111111-1111-1111-1111-111111111111", user)
	if err := s.CreateEndpoint(ctx, e); err != nil {
		t.Fatal(err)
	}

	ev := ingestSample("aaaaaaaa-0000-0000-0000-000000000002", user, "bookmark.created")
	if _, err := s.IngestEvent(ctx, ev); err != nil {
		t.Fatalf("first ingest: %v", err)
	}

	_, err := s.IngestEvent(ctx, ev)
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("want ErrDuplicate, got %v", err)
	}

	var count int
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM deliveries WHERE event_id = $1`, ev.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("duplicate created extra deliveries: %d", count)
	}
}

func TestIngestEvent_NoSubscribersStillRecorded(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	user := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"

	n, err := s.IngestEvent(ctx, ingestSample(
		"aaaaaaaa-0000-0000-0000-000000000003", user, "bookmark.created"))
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if n != 0 {
		t.Fatalf("deliveries: got %d, want 0", n)
	}

	var count int
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM seen_events WHERE event_id = $1`,
		"aaaaaaaa-0000-0000-0000-000000000003").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("seen_events not recorded for no-subscriber event")
	}
}

func TestIngestEvent_IgnoresOtherUsersEndpoints(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	userA := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	userB := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"

	eA := sampleEndpoint("11111111-1111-1111-1111-111111111111", userA)
	eB := sampleEndpoint("22222222-2222-2222-2222-222222222222", userB)
	for _, e := range []*Endpoint{eA, eB} {
		if err := s.CreateEndpoint(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	n, err := s.IngestEvent(ctx, ingestSample(
		"aaaaaaaa-0000-0000-0000-000000000004", userA, "bookmark.created"))
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if n != 1 {
		t.Fatalf("deliveries: got %d, want 1 (only userA's)", n)
	}
}
