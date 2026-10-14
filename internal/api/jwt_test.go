package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func signToken(t *testing.T, alg string, sub string, exp int64, secret []byte) string {
	t.Helper()
	hb, _ := json.Marshal(map[string]any{"alg": alg, "typ": "JWT"})
	cb, _ := json.Marshal(map[string]any{"sub": sub, "exp": exp})
	h := base64.RawURLEncoding.EncodeToString(hb)
	c := base64.RawURLEncoding.EncodeToString(cb)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(h + "." + c))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return h + "." + c + "." + sig
}

var testSecret = []byte("test-secret-test-secret-test-32b!")

func TestVerifyHS256_Valid(t *testing.T) {
	tok := signToken(t, "HS256", "user-1", time.Now().Add(time.Hour).Unix(), testSecret)
	claims, err := VerifyHS256(tok, testSecret)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if claims.Sub != "user-1" {
		t.Errorf("sub: got %q, want user-1", claims.Sub)
	}
}

func TestVerifyHS256_RejectsAlgNone(t *testing.T) {
	tok := signToken(t, "none", "user-1", time.Now().Add(time.Hour).Unix(), testSecret)
	if _, err := VerifyHS256(tok, testSecret); err == nil {
		t.Fatal("expected error for alg=none")
	}
}

func TestVerifyHS256_RejectsWrongSecret(t *testing.T) {
	tok := signToken(t, "HS256", "user-1", time.Now().Add(time.Hour).Unix(),
		[]byte("other-secret-other-secret-32bytes!!"))
	if _, err := VerifyHS256(tok, testSecret); err == nil {
		t.Fatal("expected error for wrong secret")
	}
}

func TestVerifyHS256_RejectsExpired(t *testing.T) {
	tok := signToken(t, "HS256", "user-1", time.Now().Add(-time.Hour).Unix(), testSecret)
	if _, err := VerifyHS256(tok, testSecret); err == nil {
		t.Fatal("expected error for expired token")
	}
}

func TestVerifyHS256_RejectsMissingSub(t *testing.T) {
	hb, _ := json.Marshal(map[string]any{"alg": "HS256"})
	cb, _ := json.Marshal(map[string]any{"exp": time.Now().Add(time.Hour).Unix()})
	h := base64.RawURLEncoding.EncodeToString(hb)
	c := base64.RawURLEncoding.EncodeToString(cb)
	mac := hmac.New(sha256.New, testSecret)
	mac.Write([]byte(h + "." + c))
	tok := h + "." + c + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	if _, err := VerifyHS256(tok, testSecret); err == nil {
		t.Fatal("expected error for missing sub")
	}
}

func TestVerifyHS256_RejectsMalformed(t *testing.T) {
	for _, tok := range []string{"", "abc", "a.b", "a.b.c.d", ".."} {
		if _, err := VerifyHS256(tok, testSecret); err == nil {
			t.Errorf("token %q: expected error", tok)
		}
	}
}
