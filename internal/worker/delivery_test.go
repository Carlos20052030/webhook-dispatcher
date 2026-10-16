package worker

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSend_ValidSignature(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	body := []byte(`{"hello":"world"}`)

	var gotSig, gotTs, gotDeliveryID, gotEventType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("X-Webhook-Signature")
		gotTs = r.Header.Get("X-Webhook-Timestamp")
		gotDeliveryID = r.Header.Get("X-Webhook-Delivery-ID")
		gotEventType = r.Header.Get("X-Webhook-Event-Type")

		// Verify signature matches what we expect.
		mac := hmac.New(sha256.New, secret)
		mac.Write([]byte(gotTs))
		mac.Write([]byte("."))
		mac.Write(body)
		want := "v1=" + hex.EncodeToString(mac.Sum(nil))
		if gotSig != want {
			t.Errorf("signature mismatch: got %s, want %s", gotSig, want)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient()
	r := c.Send(context.Background(), DeliveryPayload{
		DeliveryID: "d1",
		EventType:  "bookmark.created",
		Body:       body,
		Secret:     secret,
		TargetURL:  srv.URL,
	})
	if r.Err != nil {
		t.Fatalf("send: %v", r.Err)
	}
	if r.StatusCode != 200 {
		t.Errorf("status: got %d", r.StatusCode)
	}
	if gotDeliveryID != "d1" || gotEventType != "bookmark.created" {
		t.Errorf("headers: got id=%q type=%q", gotDeliveryID, gotEventType)
	}
	// Sanity: timestamp is a valid unix seconds value close to now.
	ts, _ := strconv.ParseInt(gotTs, 10, 64)
	if time.Since(time.Unix(ts, 0)) > time.Minute {
		t.Errorf("timestamp too old: %d", ts)
	}
}

func TestSend_DoesNotFollowRedirects(t *testing.T) {
	var targetHit bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHit = true
	}))
	defer target.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer srv.Close()

	c := NewClient()
	r := c.Send(context.Background(), DeliveryPayload{
		DeliveryID: "d1",
		EventType:  "x",
		Body:       []byte(`{}`),
		Secret:     []byte("k"),
		TargetURL:  srv.URL,
	})
	if r.StatusCode != 302 {
		t.Errorf("status: got %d, want 302", r.StatusCode)
	}
	if targetHit {
		t.Errorf("redirect target must not be hit")
	}
}

func TestSend_TruncatesLongResponseBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 100_000)))
	}))
	defer srv.Close()

	c := NewClient()
	r := c.Send(context.Background(), DeliveryPayload{
		DeliveryID: "d1", EventType: "x",
		Body: []byte(`{}`), Secret: []byte("k"), TargetURL: srv.URL,
	})
	if len(r.Body) > 4096 {
		t.Errorf("body not truncated: %d bytes", len(r.Body))
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		in   DeliveryResult
		want Classification
	}{
		{"200", DeliveryResult{StatusCode: 200}, ClassSuccess},
		{"201", DeliveryResult{StatusCode: 201}, ClassSuccess},
		{"299", DeliveryResult{StatusCode: 299}, ClassSuccess},
		{"400", DeliveryResult{StatusCode: 400}, ClassPermanent},
		{"404", DeliveryResult{StatusCode: 404}, ClassPermanent},
		{"408", DeliveryResult{StatusCode: 408}, ClassTransient},
		{"429", DeliveryResult{StatusCode: 429}, ClassTransient},
		{"500", DeliveryResult{StatusCode: 500}, ClassTransient},
		{"503", DeliveryResult{StatusCode: 503}, ClassTransient},
		{"302", DeliveryResult{StatusCode: 302}, ClassPermanent},
		{"network", DeliveryResult{Err: context.DeadlineExceeded}, ClassTransient},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Classify(tc.in)
			if got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}
