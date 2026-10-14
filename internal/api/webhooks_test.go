package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Carlos20052030/webhook-dispatcher/internal/store"
)

func withUser(req *http.Request, sub string) *http.Request {
	ctx := context.WithValue(req.Context(), userIDKey, sub)
	return req.WithContext(ctx)
}

func TestCreateWebhook_HappyPath(t *testing.T) {
	fake := &FakeEndpointStore{}
	h := &WebhookHandler{Store: fake}

	body := []byte(`{"url":"https://example.com/hook","subscriptions":["bookmark.created"]}`)
	req := httptest.NewRequest("POST", "/webhooks", bytes.NewReader(body))
	req = withUser(req, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	rr := httptest.NewRecorder()

	h.Create(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201. body: %s", rr.Code, rr.Body)
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["secret"] == "" || resp["secret"] == nil {
		t.Errorf("secret must be returned on create")
	}
	if len(fake.endpoints) != 1 {
		t.Errorf("store got %d rows, want 1", len(fake.endpoints))
	}
}

func TestCreateWebhook_RejectsBadURL(t *testing.T) {
	cases := []string{
		"",
		"ftp://example.com",
		"not a url",
		"https://",
	}
	for _, bad := range cases {
		t.Run(bad, func(t *testing.T) {
			fake := &FakeEndpointStore{}
			h := &WebhookHandler{Store: fake}

			body, _ := json.Marshal(map[string]any{
				"url": bad, "subscriptions": []string{"bookmark.created"},
			})
			req := httptest.NewRequest("POST", "/webhooks", bytes.NewReader(body))
			req = withUser(req, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
			rr := httptest.NewRecorder()

			h.Create(rr, req)
			if rr.Code != http.StatusBadRequest {
				t.Errorf("url %q: got %d, want 400", bad, rr.Code)
			}
			if len(fake.endpoints) != 0 {
				t.Errorf("url %q: store should be untouched", bad)
			}
		})
	}
}

func TestCreateWebhook_RejectsEmptySubscriptions(t *testing.T) {
	fake := &FakeEndpointStore{}
	h := &WebhookHandler{Store: fake}

	body := []byte(`{"url":"https://example.com","subscriptions":[]}`)
	req := httptest.NewRequest("POST", "/webhooks", bytes.NewReader(body))
	req = withUser(req, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	rr := httptest.NewRecorder()

	h.Create(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rr.Code)
	}
}

func TestListWebhooks_OnlyOwn(t *testing.T) {
	fake := &FakeEndpointStore{
		endpoints: []store.Endpoint{
			{ID: "11111111-1111-1111-1111-111111111111", UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", URL: "https://a"},
			{ID: "22222222-2222-2222-2222-222222222222", UserID: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", URL: "https://b"},
		},
	}
	h := &WebhookHandler{Store: fake}

	req := httptest.NewRequest("GET", "/webhooks", nil)
	req = withUser(req, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	rr := httptest.NewRecorder()

	h.List(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d", rr.Code)
	}
	var resp []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp) != 1 {
		t.Fatalf("got %d endpoints, want 1", len(resp))
	}
	if _, has := resp[0]["secret"]; has {
		t.Errorf("secret must not be returned by list")
	}
}

func TestDeleteWebhook_NotFound(t *testing.T) {
	fake := &FakeEndpointStore{}
	h := &WebhookHandler{Store: fake}

	req := httptest.NewRequest("DELETE", "/webhooks/33333333-3333-3333-3333-333333333333", nil)
	req.SetPathValue("id", "33333333-3333-3333-3333-333333333333")
	req = withUser(req, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	rr := httptest.NewRecorder()

	h.Delete(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", rr.Code)
	}
}

func TestDeleteWebhook_OtherUserIsNotFound(t *testing.T) {
	fake := &FakeEndpointStore{
		endpoints: []store.Endpoint{
			{ID: "11111111-1111-1111-1111-111111111111", UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"},
		},
	}
	h := &WebhookHandler{Store: fake}

	req := httptest.NewRequest("DELETE", "/webhooks/11111111-1111-1111-1111-111111111111", nil)
	req.SetPathValue("id", "11111111-1111-1111-1111-111111111111")
	req = withUser(req, "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	rr := httptest.NewRecorder()

	h.Delete(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("got %d, want 404", rr.Code)
	}
	if len(fake.endpoints) != 1 {
		t.Errorf("row was deleted by wrong user")
	}
}

func TestDeleteWebhook_HappyPath(t *testing.T) {
	fake := &FakeEndpointStore{
		endpoints: []store.Endpoint{
			{ID: "11111111-1111-1111-1111-111111111111", UserID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"},
		},
	}
	h := &WebhookHandler{Store: fake}

	req := httptest.NewRequest("DELETE", "/webhooks/11111111-1111-1111-1111-111111111111", nil)
	req.SetPathValue("id", "11111111-1111-1111-1111-111111111111")
	req = withUser(req, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	rr := httptest.NewRecorder()

	h.Delete(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("got %d, want 204", rr.Code)
	}
}

func TestCreateWebhook_StoreError(t *testing.T) {
	fake := &FakeEndpointStore{ForceErr: errors.New("boom")}
	h := &WebhookHandler{Store: fake}

	body := []byte(`{"url":"https://example.com","subscriptions":["x"]}`)
	req := httptest.NewRequest("POST", "/webhooks", bytes.NewReader(body))
	req = withUser(req, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	rr := httptest.NewRecorder()

	h.Create(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("got %d, want 500", rr.Code)
	}
}
