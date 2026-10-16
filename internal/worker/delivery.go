package worker

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// DeliveryPayload is what the worker sends on the wire. It is fully
// detached from internal types so the delivery client can be tested
// without touching the store.
type DeliveryPayload struct {
	DeliveryID string
	EventType  string
	Body       []byte
	Secret     []byte
	TargetURL  string
}

// DeliveryResult is the outcome of one attempt.
type DeliveryResult struct {
	StatusCode int
	Body       string // truncated response body for logging
	Duration   time.Duration
	Err        error // non-nil on transport error (timeout, DNS, etc.)
}

// Classify tells the caller what to do with the result.
type Classification int

const (
	ClassSuccess   Classification = iota // 2xx
	ClassTransient                       // 408, 429, 5xx, network
	ClassPermanent                       // other 4xx
)

func (c Classification) String() string {
	switch c {
	case ClassSuccess:
		return "success"
	case ClassTransient:
		return "transient"
	case ClassPermanent:
		return "permanent"
	}
	return "unknown"
}

// Client performs one HTTP delivery with HMAC signature.
type Client struct {
	HTTP    *http.Client
	Timeout time.Duration
}

func NewClient() *Client {
	return &Client{
		HTTP: &http.Client{
			// Never follow redirects. 3xx becomes a failure.
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		Timeout: 10 * time.Second,
	}
}

// Send performs one POST with signature. Network errors are returned
// inside DeliveryResult.Err, not as a Go error — the caller classifies.
func (c *Client) Send(ctx context.Context, p DeliveryPayload) DeliveryResult {
	ts := time.Now().Unix()
	sig := sign(ts, p.Body, p.Secret)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.TargetURL, bytes.NewReader(p.Body))
	if err != nil {
		return DeliveryResult{Err: fmt.Errorf("build request: %w", err)}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Webhook-Timestamp", strconv.FormatInt(ts, 10))
	req.Header.Set("X-Webhook-Signature", "v1="+sig)
	req.Header.Set("X-Webhook-Delivery-ID", p.DeliveryID)
	req.Header.Set("X-Webhook-Event-Type", p.EventType)
	req.Header.Set("User-Agent", "webhook-dispatcher/1.0")

	start := time.Now()
	resp, err := c.HTTP.Do(req)
	elapsed := time.Since(start)
	if err != nil {
		return DeliveryResult{Duration: elapsed, Err: err}
	}
	defer func() { _ = resp.Body.Close() }()

	// Cap response body reading; a hostile target should not be able
	// to grow our memory.
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))

	return DeliveryResult{
		StatusCode: resp.StatusCode,
		Body:       string(body),
		Duration:   elapsed,
	}
}

// sign produces hex(HMAC-SHA256(secret, "<ts>.<body>")). Stripe v1 style.
func sign(ts int64, body, secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(strconv.FormatInt(ts, 10)))
	mac.Write([]byte("."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// Classify maps a result to what the worker should do next.
func Classify(r DeliveryResult) Classification {
	if r.Err != nil {
		return ClassTransient
	}
	if r.StatusCode >= 200 && r.StatusCode < 300 {
		return ClassSuccess
	}
	if r.StatusCode == 408 || r.StatusCode == 429 {
		return ClassTransient
	}
	if r.StatusCode >= 500 {
		return ClassTransient
	}
	if r.StatusCode >= 400 {
		return ClassPermanent
	}
	// 3xx: we never follow, so it's permanent. Misconfigured target.
	return ClassPermanent
}
