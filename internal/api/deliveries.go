package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Carlos20052030/webhook-dispatcher/internal/store"
)

// DeliveryReader is what the handler needs.
type DeliveryReader interface {
	ListDeliveries(ctx context.Context, userID string, f store.DeliveryFilter) ([]store.Delivery, error)
}

type DeliveryHandler struct {
	Store DeliveryReader
}

// List serves GET /deliveries. Requires RequireJWT upstream.
//
// Query params:
//
//	endpoint_id  filter by endpoint
//	status       pending|queued|in_flight|succeeded|dead
//	event_type   filter by event type
//	from         RFC3339 lower bound on created_at (inclusive)
//	to           RFC3339 upper bound on created_at (inclusive)
//	cursor       last ID from previous page
//	limit        1..200, default 50
func (h *DeliveryHandler) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := UserID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	f, err := parseDeliveryFilter(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	rows, err := h.Store.ListDeliveries(r.Context(), userID, f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	out := make([]map[string]any, 0, len(rows))
	for _, d := range rows {
		out = append(out, deliveryJSON(d))
	}

	resp := map[string]any{"deliveries": out}
	if len(rows) > 0 {
		// next_cursor is the ID of the last row; caller sends it back
		// to fetch the next page. Absent when the page is empty.
		resp["next_cursor"] = rows[len(rows)-1].ID
	}
	writeJSON(w, http.StatusOK, resp)
}

func parseDeliveryFilter(r *http.Request) (store.DeliveryFilter, error) {
	q := r.URL.Query()
	f := store.DeliveryFilter{
		EndpointID: q.Get("endpoint_id"),
		Status:     q.Get("status"),
		EventType:  q.Get("event_type"),
		Cursor:     q.Get("cursor"),
	}

	if f.Status != "" && !validStatus(f.Status) {
		return f, errors.New("invalid status")
	}

	if s := q.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 {
			return f, errors.New("invalid limit")
		}
		f.Limit = n
	}

	if s := q.Get("from"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return f, errors.New("invalid from, want RFC3339")
		}
		f.From = &t
	}
	if s := q.Get("to"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return f, errors.New("invalid to, want RFC3339")
		}
		f.To = &t
	}

	return f, nil
}

func validStatus(s string) bool {
	switch s {
	case "pending", "queued", "in_flight", "succeeded", "dead":
		return true
	}
	return false
}

func deliveryJSON(d store.Delivery) map[string]any {
	m := map[string]any{
		"id":          d.ID,
		"event_id":    d.EventID,
		"endpoint_id": d.EndpointID,
		"event_type":  d.EventType,
		"status":      d.Status,
		"attempts":    d.Attempts,
		"created_at":  d.CreatedAt,
		"updated_at":  d.UpdatedAt,
	}
	if d.NextAttemptAt != nil {
		m["next_attempt_at"] = *d.NextAttemptAt
	}
	return m
}
