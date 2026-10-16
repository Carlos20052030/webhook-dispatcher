package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/Carlos20052030/webhook-dispatcher/internal/store"
)

// EventStore is what the events handler needs. Consumer-side interface,
// same pattern as EndpointStore.
type EventStore interface {
	IngestEvent(ctx context.Context, ev store.EventToIngest) (int, error)
}

// EventHandler serves POST /events. Requires RequireIngestToken upstream.
type EventHandler struct {
	Store EventStore
}

// eventPayload mirrors the shape the Bookmarks API sends. data is passed
// through as opaque JSON — the dispatcher never inspects it.
type eventPayload struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	UserID     string          `json:"user_id"`
	OccurredAt time.Time       `json:"occurred_at"`
	Data       json.RawMessage `json:"data"`
}

func (h *EventHandler) Ingest(w http.ResponseWriter, r *http.Request) {
	// 64 KiB cap. Bookmarks sends small payloads; anything larger is
	// a client bug.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil {
		writeError(w, http.StatusBadRequest, "body too large or unreadable")
		return
	}

	var p eventPayload
	if err := json.Unmarshal(body, &p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if p.ID == "" || p.Type == "" || p.UserID == "" {
		writeError(w, http.StatusBadRequest, "id, type and user_id are required")
		return
	}
	if p.OccurredAt.IsZero() {
		p.OccurredAt = time.Now().UTC()
	}

	// data is optional; default to {} so payload column is never NULL.
	if len(p.Data) == 0 {
		p.Data = json.RawMessage(`{}`)
	}

	ev := store.EventToIngest{
		ID:         p.ID,
		Type:       p.Type,
		UserID:     p.UserID,
		OccurredAt: p.OccurredAt,
		Payload:    p.Data,
	}

	n, err := h.Store.IngestEvent(r.Context(), ev)
	switch {
	case errors.Is(err, store.ErrDuplicate):
		writeJSON(w, http.StatusOK, map[string]string{"status": "duplicate"})
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal error")
	default:
		writeJSON(w, http.StatusAccepted, map[string]any{
			"status":             "accepted",
			"deliveries_created": n,
		})
	}
}
