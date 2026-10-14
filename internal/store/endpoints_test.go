package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db := openTestDB(t)
	resetSchema(t, db)
	if err := Migrate(context.Background(), db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return New(db)
}

func sampleEndpoint(id, userID string) *Endpoint {
	return &Endpoint{
		ID:            id,
		UserID:        userID,
		URL:           "https://example.com/hook",
		Secret:        "deadbeef",
		Active:        true,
		Subscriptions: []string{"bookmark.created"},
		CreatedAt:     time.Now().UTC(),
	}
}

func TestCreateEndpoint_RoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	want := sampleEndpoint("11111111-1111-1111-1111-111111111111", "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	if err := s.CreateEndpoint(ctx, want); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := s.ListEndpointsByUser(ctx, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d endpoints, want 1", len(got))
	}
	if got[0].ID != want.ID || got[0].URL != want.URL {
		t.Errorf("roundtrip mismatch: %+v", got[0])
	}
	if len(got[0].Subscriptions) != 1 || got[0].Subscriptions[0] != "bookmark.created" {
		t.Errorf("subscriptions: got %v", got[0].Subscriptions)
	}
}

func TestCreateEndpoint_NoSubscriptions(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	e := sampleEndpoint("22222222-2222-2222-2222-222222222222", "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	e.Subscriptions = nil
	if err := s.CreateEndpoint(ctx, e); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := s.ListEndpointsByUser(ctx, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d endpoints, want 1", len(got))
	}
	if len(got[0].Subscriptions) != 0 {
		t.Errorf("subscriptions: got %v, want empty", got[0].Subscriptions)
	}
}

func TestListEndpointsByUser_IsolatesUsers(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.CreateEndpoint(ctx,
		sampleEndpoint("11111111-1111-1111-1111-111111111111", "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")); err != nil {
		t.Fatalf("create user-1: %v", err)
	}
	if err := s.CreateEndpoint(ctx,
		sampleEndpoint("22222222-2222-2222-2222-222222222222", "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")); err != nil {
		t.Fatalf("create user-2: %v", err)
	}

	got, err := s.ListEndpointsByUser(ctx, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].UserID != "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa" {
		t.Errorf("user-1 should see only its own endpoint, got %+v", got)
	}
}

func TestDeleteEndpoint_OtherUserIsNotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	e := sampleEndpoint("11111111-1111-1111-1111-111111111111", "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	if err := s.CreateEndpoint(ctx, e); err != nil {
		t.Fatalf("create: %v", err)
	}

	err := s.DeleteEndpoint(ctx, "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", e.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	got, err := s.ListEndpointsByUser(ctx, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("row was deleted by other user")
	}
}

func TestDeleteEndpoint_HappyPath(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	e := sampleEndpoint("11111111-1111-1111-1111-111111111111", "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	if err := s.CreateEndpoint(ctx, e); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.DeleteEndpoint(ctx, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", e.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	got, err := s.ListEndpointsByUser(ctx, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("endpoint still present after delete")
	}

	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM endpoint_subscriptions WHERE endpoint_id = $1`,
		e.ID).Scan(&n); err != nil {
		t.Fatalf("count subscriptions: %v", err)
	}
	if n != 0 {
		t.Errorf("subscriptions not cascaded: %d rows", n)
	}
}
