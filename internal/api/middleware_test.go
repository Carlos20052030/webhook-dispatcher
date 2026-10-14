package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRequireJWT_NoHeader(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not run")
	})
	h := RequireJWT(testSecret)(next)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rr.Code)
	}
}

func TestRequireJWT_Valid(t *testing.T) {
	tok := signToken(t, "HS256", "user-1", time.Now().Add(time.Hour).Unix(), testSecret)

	var gotSub string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSub, _ = UserID(r.Context())
	})
	h := RequireJWT(testSecret)(next)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rr.Code)
	}
	if gotSub != "user-1" {
		t.Errorf("sub: got %q, want user-1", gotSub)
	}
}

func TestRequireJWT_WrongSecret(t *testing.T) {
	tok := signToken(t, "HS256", "u", time.Now().Add(time.Hour).Unix(),
		[]byte("other-secret-other-secret-32bytes!!"))
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not run")
	})
	h := RequireJWT(testSecret)(next)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rr.Code)
	}
}

func TestRequireIngestToken_Valid(t *testing.T) {
	token := []byte("static-ingest-token")
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := RequireIngestToken(token)(next)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/events", nil)
	req.Header.Set("X-Ingest-Token", "static-ingest-token")
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rr.Code)
	}
}

func TestRequireIngestToken_Wrong(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not run")
	})
	h := RequireIngestToken([]byte("static-ingest-token"))(next)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/events", nil)
	req.Header.Set("X-Ingest-Token", "wrong")
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rr.Code)
	}
}
