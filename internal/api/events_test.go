package api

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Carlos20052030/webhook-dispatcher/internal/store"
)

// FakeEventStore is a hand-written double. Keeps the test independent of
// Postgres and of the real Store.
type FakeEventStore struct {
	Last     store.EventToIngest
	Count    int
	ForceErr error
}

func (f *FakeEventStore) IngestEvent(_ context.Context, ev store.EventToIngest) (int, error) {
	if f.ForceErr != nil {
		return 0, f.ForceErr
	}
	f.Last = ev
	f.Count++
	return 1, nil
}

func validEventBody() []byte {
	return []byte(`{
		"id": "01931a4c-1234-7abc-89ab-000000000001",
		"type": "bookmark.created",
		"user_id": "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		"occurred_at": "2026-10-16T10:00:00Z",
		"data": {"url": "https://example.com"}
	}`)
}

func TestIngestEvent_HappyPath(t *testing.T) {
	fake := &FakeEventStore{}
	h := &EventHandler{Store: fake}

	req := httptest.NewRequest("POST", "/events", bytes.NewReader(validEventBody()))
	rr := httptest.NewRecorder()
	h.Ingest(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: got %d, want 202. body: %s", rr.Code, rr.Body)
	}
	if fake.Count != 1 {
		t.Errorf("store called %d times, want 1", fake.Count)
	}
	if fake.Last.ID != "01931a4c-1234-7abc-89ab-000000000001" {
		t.Errorf("id passed through: got %q", fake.Last.ID)
	}
}

func TestIngestEvent_DuplicateReturns200(t *testing.T) {
	fake := &FakeEventStore{ForceErr: store.ErrDuplicate}
	h := &EventHandler{Store: fake}

	req := httptest.NewRequest("POST", "/events", bytes.NewReader(validEventBody()))
	rr := httptest.NewRecorder()
	h.Ingest(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "duplicate") {
		t.Errorf("body should mention duplicate, got %s", rr.Body)
	}
}

func TestIngestEvent_RejectsBadJSON(t *testing.T) {
	fake := &FakeEventStore{}
	h := &EventHandler{Store: fake}

	req := httptest.NewRequest("POST", "/events", strings.NewReader(`{not json`))
	rr := httptest.NewRecorder()
	h.Ingest(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rr.Code)
	}
	if fake.Count != 0 {
		t.Errorf("store should not be called on bad JSON")
	}
}

func TestIngestEvent_RejectsMissingFields(t *testing.T) {
	fake := &FakeEventStore{}
	h := &EventHandler{Store: fake}

	cases := []string{
		`{"type":"x","user_id":"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}`,
		`{"id":"x","user_id":"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}`,
		`{"id":"x","type":"x"}`,
	}
	for _, body := range cases {
		req := httptest.NewRequest("POST", "/events", strings.NewReader(body))
		rr := httptest.NewRecorder()
		h.Ingest(rr, req)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("body %q: got %d, want 400", body, rr.Code)
		}
	}
}

func TestIngestEvent_StoreErrorReturns500(t *testing.T) {
	fake := &FakeEventStore{ForceErr: errors.New("boom")}
	h := &EventHandler{Store: fake}

	req := httptest.NewRequest("POST", "/events", bytes.NewReader(validEventBody()))
	rr := httptest.NewRecorder()
	h.Ingest(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want 500", rr.Code)
	}
}

func TestIngestEvent_DefaultsOccurredAt(t *testing.T) {
	fake := &FakeEventStore{}
	h := &EventHandler{Store: fake}

	body := []byte(`{"id":"x","type":"t","user_id":"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}`)
	req := httptest.NewRequest("POST", "/events", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.Ingest(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("status: got %d, want 202", rr.Code)
	}
	if fake.Last.OccurredAt.IsZero() {
		t.Errorf("OccurredAt should default to now, got zero")
	}
}
