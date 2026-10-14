package api

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"
)

type ctxKey int

const userIDKey ctxKey = iota

// RequireJWT validates the bearer token and injects the sub claim into the
// request context. Rejects with 401 otherwise.
func RequireJWT(secret []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			token, ok := strings.CutPrefix(auth, "Bearer ")
			if !ok || token == "" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			claims, err := VerifyHS256(token, secret)
			if err != nil {
				// Never echo the specific failure: it would leak which
				// check tripped.
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			ctx := context.WithValue(r.Context(), userIDKey, claims.Sub)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// UserID returns the sub claim set by RequireJWT. ok is false when the
// request did not pass through the middleware.
func UserID(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(userIDKey).(string)
	return v, ok
}

// RequireIngestToken compares the X-Ingest-Token header against the static
// token in constant time.
func RequireIngestToken(token []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got := r.Header.Get("X-Ingest-Token")
			if got == "" || subtle.ConstantTimeCompare([]byte(got), token) != 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
