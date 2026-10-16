package queue

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// Uses Redis DB 15 to isolate from the app's DB 0. Skip when env missing.
func openTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("TEST_REDIS_ADDR not set; skipping Redis-backed test")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr, DB: 15})
	t.Cleanup(func() {
		_ = rdb.FlushDB(context.Background()).Err()
		_ = rdb.Close()
	})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("ping redis: %v", err)
	}
	return rdb
}

func uniqueStream(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("test-stream-%d", time.Now().UnixNano())
}

func TestEnsureGroup_Idempotent(t *testing.T) {
	rdb := openTestRedis(t)
	c := New(rdb)
	ctx := context.Background()
	stream := uniqueStream(t)

	if err := c.EnsureGroup(ctx, stream, "g"); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := c.EnsureGroup(ctx, stream, "g"); err != nil {
		t.Fatalf("second: %v", err)
	}
}

func TestPublishAndConsume(t *testing.T) {
	rdb := openTestRedis(t)
	c := New(rdb)
	ctx := context.Background()
	stream := uniqueStream(t)

	if err := c.EnsureGroup(ctx, stream, "g"); err != nil {
		t.Fatal(err)
	}
	if err := c.Publish(ctx, stream, "delivery-1"); err != nil {
		t.Fatal(err)
	}
	msg, err := c.Consume(ctx, stream, "g", "worker-1", 2*time.Second)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if msg.DeliveryID != "delivery-1" {
		t.Errorf("delivery id: got %q", msg.DeliveryID)
	}
	if msg.StreamID == "" {
		t.Errorf("stream id must be set")
	}
}

func TestConsume_TimeoutReturnsErrNoMessage(t *testing.T) {
	rdb := openTestRedis(t)
	c := New(rdb)
	ctx := context.Background()
	stream := uniqueStream(t)

	if err := c.EnsureGroup(ctx, stream, "g"); err != nil {
		t.Fatal(err)
	}
	_, err := c.Consume(ctx, stream, "g", "worker-1", 200*time.Millisecond)
	if !errors.Is(err, ErrNoMessage) {
		t.Errorf("got %v, want ErrNoMessage", err)
	}
}

func TestAck_RemovesFromPending(t *testing.T) {
	rdb := openTestRedis(t)
	c := New(rdb)
	ctx := context.Background()
	stream := uniqueStream(t)

	if err := c.EnsureGroup(ctx, stream, "g"); err != nil {
		t.Fatal(err)
	}
	if err := c.Publish(ctx, stream, "d1"); err != nil {
		t.Fatal(err)
	}
	msg, err := c.Consume(ctx, stream, "g", "w1", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Ack(ctx, stream, "g", msg.StreamID); err != nil {
		t.Fatalf("ack: %v", err)
	}
	pending, err := rdb.XPending(ctx, stream, "g").Result()
	if err != nil {
		t.Fatal(err)
	}
	if pending.Count != 0 {
		t.Errorf("pending after ack: got %d, want 0", pending.Count)
	}
}

func TestClaimStuck_ReclaimsAbandonedMessage(t *testing.T) {
	rdb := openTestRedis(t)
	c := New(rdb)
	ctx := context.Background()
	stream := uniqueStream(t)

	if err := c.EnsureGroup(ctx, stream, "g"); err != nil {
		t.Fatal(err)
	}
	if err := c.Publish(ctx, stream, "d1"); err != nil {
		t.Fatal(err)
	}
	// Consume as worker-1 but never ack — simulates a crash.
	if _, err := c.Consume(ctx, stream, "g", "w1", 2*time.Second); err != nil {
		t.Fatal(err)
	}
	// Claim with minIdle=0 so it's immediate for the test.
	msgs, err := c.ClaimStuck(ctx, stream, "g", "w2", 0, 10)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("claimed %d, want 1", len(msgs))
	}
	if msgs[0].DeliveryID != "d1" {
		t.Errorf("delivery id: got %q", msgs[0].DeliveryID)
	}
}
