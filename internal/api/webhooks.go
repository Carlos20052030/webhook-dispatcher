package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/Carlos20052030/webhook-dispatcher/internal/store"
)

// EndpointStore is what the handler needs. Defined here (consumer side),
// not in the store package, so the fake and the real store both satisfy
// the same shape without coupling.
type EndpointStore interface {
	CreateEndpoint(ctx context.Context, e *store.Endpoint) error
	ListEndpointsByUser(ctx context.Context, userID string) ([]store.Endpoint, error)
	DeleteEndpoint(ctx context.Context, userID, id string) error
}

// WebhookHandler serves /webhooks routes. Requires RequireJWT upstream.
type WebhookHandler struct {
	Store EndpointStore
}

func (h *WebhookHandler) Create(w http.ResponseWriter, r *http.Request) {
	userID, ok := UserID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req struct {
		URL           string   `json:"url"`
		Subscriptions []string `json:"subscriptions"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if err := validateWebhookURL(req.URL); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(req.Subscriptions) == 0 {
		writeError(w, http.StatusBadRequest, "subscriptions must not be empty")
		return
	}

	id, err := newUUIDv7()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	secret, err := newSecret()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	ep := &store.Endpoint{
		ID:            id,
		UserID:        userID,
		URL:           req.URL,
		Secret:        secret,
		Active:        true,
		Subscriptions: req.Subscriptions,
		CreatedAt:     time.Now().UTC(),
	}
	if err := h.Store.CreateEndpoint(r.Context(), ep); err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Secret is returned only here, never on GET.
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":            ep.ID,
		"url":           ep.URL,
		"subscriptions": ep.Subscriptions,
		"secret":        ep.Secret,
		"created_at":    ep.CreatedAt,
	})
}

func (h *WebhookHandler) List(w http.ResponseWriter, r *http.Request) {
	userID, ok := UserID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	eps, err := h.Store.ListEndpointsByUser(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	out := make([]map[string]any, 0, len(eps))
	for _, e := range eps {
		out = append(out, map[string]any{
			"id":            e.ID,
			"url":           e.URL,
			"subscriptions": e.Subscriptions,
			"active":        e.Active,
			"created_at":    e.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *WebhookHandler) Delete(w http.ResponseWriter, r *http.Request) {
	userID, ok := UserID(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id := r.PathValue("id")

	err := h.Store.DeleteEndpoint(r.Context(), userID, id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal error")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func validateWebhookURL(raw string) error {
	if raw == "" {
		return errors.New("url is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return errors.New("url is not a valid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("url must use http or https")
	}
	if u.Host == "" {
		return errors.New("url must have a host")
	}
	return nil
}

func newSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// newUUIDv7 is a placeholder until a real v7 generator is wired in
// Phase 3 (deliveries.id also uses v7). For now, v4 is fine — the format
// is identical, only the time-ordering property is missing.
func newUUIDv7() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
