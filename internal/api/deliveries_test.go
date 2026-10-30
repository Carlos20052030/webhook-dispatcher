package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Carlos20052030/webhook-dispatcher/internal/store"
)

type fakeDeliveryReader struct {
	rows    []store.Delivery
	lastF   store.DeliveryFilter
	lastUID string
	err     error
}

func (f *fakeDeliveryReader) ListDeliveries(_ context.Context, userID string, filter store.DeliveryFilter) ([]store.Delivery, error) {
	f.lastF = filter
	f.lastUID = userID
	if f.err != nil {
		return nil, f.err
	}
	return f.rows, nil
}

func deliveryRequest(t *testing.T, rawQuery string) *http.Request {
	t.Helper()
	req := httptest.NewRequest("GET", "/deliveries?"+rawQuery, nil)
	return withUser(req, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
}

func TestListDeliveries_HappyPath(t *testing.T) {
	now := time.Now().UTC()
	fake := &fakeDeliveryReader{
		rows: []store.Delivery{
			{ID: "c1", EventID: "e1", EndpointID: "ep1", EventType: "bookmark.created",
				Status: "succeeded", Attempts: 1, CreatedAt: now, UpdatedAt: now},
			{ID: "c2", EventID: "e2", EndpointID: "ep1", EventType: "bookmark.created",
				Status: "pending", Attempts: 0, CreatedAt: now, UpdatedAt: now},
		},
	}
	h := &DeliveryHandler{Store: fake}
	rr := httptest.NewRecorder()
	h.List(rr, deliveryRequest(t, ""))

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, body: %s", rr.Code, rr.Body)
	}
	var body struct {
		Deliveries []map[string]any `json:"deliveries"`
		NextCursor string           `json:"next_cursor"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Deliveries) != 2 {
		t.Errorf("deliveries: got %d", len(body.Deliveries))
	}
	if body.NextCursor != "c2" {
		t.Errorf("next_cursor: got %q, want c2", body.NextCursor)
	}
}

func TestListDeliveries_RejectsBadStatus(t *testing.T) {
	h := &DeliveryHandler{Store: &fakeDeliveryReader{}}
	rr := httptest.NewRecorder()
	h.List(rr, deliveryRequest(t, "status=exploded"))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rr.Code)
	}
}

func TestListDeliveries_RejectsBadLimit(t *testing.T) {
	h := &DeliveryHandler{Store: &fakeDeliveryReader{}}
	rr := httptest.NewRecorder()
	h.List(rr, deliveryRequest(t, "limit=0"))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rr.Code)
	}
}

func TestListDeliveries_RejectsBadFrom(t *testing.T) {
	h := &DeliveryHandler{Store: &fakeDeliveryReader{}}
	rr := httptest.NewRecorder()
	h.List(rr, deliveryRequest(t, "from=nao-e-data"))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rr.Code)
	}
}

func TestListDeliveries_PassesFiltersThrough(t *testing.T) {
	fake := &fakeDeliveryReader{}
	h := &DeliveryHandler{Store: fake}
	rr := httptest.NewRecorder()
	h.List(rr, deliveryRequest(t,
		"endpoint_id=ep1&status=dead&event_type=bookmark.created&limit=10&cursor=c9"))

	if fake.lastUID != "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa" {
		t.Errorf("user id: got %q", fake.lastUID)
	}
	if fake.lastF.EndpointID != "ep1" || fake.lastF.Status != "dead" ||
		fake.lastF.EventType != "bookmark.created" || fake.lastF.Limit != 10 ||
		fake.lastF.Cursor != "c9" {
		t.Errorf("filter not passed: %+v", fake.lastF)
	}
}

func TestListDeliveries_StoreError(t *testing.T) {
	fake := &fakeDeliveryReader{err: errors.New("boom")}
	h := &DeliveryHandler{Store: fake}
	rr := httptest.NewRecorder()
	h.List(rr, deliveryRequest(t, ""))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want 500", rr.Code)
	}
}

func TestListDeliveries_EmptyResult(t *testing.T) {
	fake := &fakeDeliveryReader{rows: nil}
	h := &DeliveryHandler{Store: fake}
	rr := httptest.NewRecorder()
	h.List(rr, deliveryRequest(t, ""))

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if _, has := body["next_cursor"]; has {
		t.Errorf("next_cursor should be absent on empty page")
	}
}

func TestListDeliveries_UnauthorizedWithoutJWT(t *testing.T) {
	fake := &fakeDeliveryReader{}
	h := &DeliveryHandler{Store: fake}
	rr := httptest.NewRecorder()
	// Request without going through RequireJWT middleware (no UserID in context)
	req := httptest.NewRequest("GET", "/deliveries", nil)
	h.List(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["error"] != "unauthorized" {
		t.Errorf("error message: got %q, want 'unauthorized'", body["error"])
	}
}
