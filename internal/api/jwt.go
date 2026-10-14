package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Verification errors. Coarse on purpose: callers must not distinguish
// "wrong signature" from "expired" in responses.
var (
	ErrMalformed      = errors.New("malformed token")
	ErrUnsupportedAlg = errors.New("unsupported algorithm")
	ErrBadSignature   = errors.New("bad signature")
	ErrExpired        = errors.New("token expired")
	ErrMissingSub     = errors.New("missing sub claim")
	ErrMissingExp     = errors.New("missing exp claim")
)

// Claims is the subset of JWT claims the dispatcher consumes. Bookmarks API
// is the sole issuer; anything beyond sub/exp is ignored.
type Claims struct {
	Sub string `json:"sub"`
	Exp int64  `json:"exp"`
}

// VerifyHS256 validates a JWT signed with HMAC-SHA256 and returns its claims.
func VerifyHS256(token string, secret []byte) (*Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, ErrMalformed
	}

	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, ErrMalformed
	}
	var header struct {
		Alg string `json:"alg"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return nil, ErrMalformed
	}
	// Reject anything but HS256. Prevents "alg:none" and RS256 confusion.
	if header.Alg != "HS256" {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedAlg, header.Alg)
	}

	// Verify signature before touching claims. Constant-time compare.
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(parts[0] + "." + parts[1]))
	want := mac.Sum(nil)
	got, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, ErrMalformed
	}
	if !hmac.Equal(want, got) {
		return nil, ErrBadSignature
	}

	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, ErrMalformed
	}
	var c Claims
	if err := json.Unmarshal(claimsJSON, &c); err != nil {
		return nil, ErrMalformed
	}
	if c.Sub == "" {
		return nil, ErrMissingSub
	}
	if c.Exp == 0 {
		return nil, ErrMissingExp
	}
	if time.Now().Unix() >= c.Exp {
		return nil, ErrExpired
	}
	return &c, nil
}
