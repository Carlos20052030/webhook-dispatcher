package queue

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	DefaultStream = "deliveries"
	DefaultGroup  = "workers"
)

// ErrNoMessage is returned by Consume when the block window expires
// without a new message. Callers loop on it.
var ErrNoMessage = errors.New("no message available")

type Client struct {
	rdb *redis.Client
}

func New(rdb *redis.Client) *Client {
	return &Client{rdb: rdb}
}

// Message is one stream entry. Only delivery_id matters to the
// dispatcher; everything else lives in Postgres.
type Message struct {
	StreamID   string // Redis entry ID, needed for XACK
	DeliveryID string
}

// EnsureGroup creates the consumer group if missing. Uses "$" so new
// consumers only see messages published after the group was created.
// Idempotent: safe to call on every boot.
func (c *Client) EnsureGroup(ctx context.Context, stream, group string) error {
	err := c.rdb.XGroupCreateMkStream(ctx, stream, group, "$").Err()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		return fmt.Errorf("xgroup create: %w", err)
	}
	return nil
}

// Publish appends a delivery to the stream. Uses server-generated IDs.
func (c *Client) Publish(ctx context.Context, stream, deliveryID string) error {
	err := c.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: stream,
		Values: map[string]any{"delivery_id": deliveryID},
	}).Err()
	if err != nil {
		return fmt.Errorf("xadd: %w", err)
	}
	return nil
}

// Consume reads one new message for consumer in group. Blocks up to
// block waiting. Returns ErrNoMessage on timeout.
func (c *Client) Consume(ctx context.Context, stream, group, consumer string, block time.Duration) (*Message, error) {
	res, err := c.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    group,
		Consumer: consumer,
		Streams:  []string{stream, ">"},
		Count:    1,
		Block:    block,
	}).Result()
	if errors.Is(err, redis.Nil) {
		return nil, ErrNoMessage
	}
	if err != nil {
		return nil, fmt.Errorf("xreadgroup: %w", err)
	}
	if len(res) == 0 || len(res[0].Messages) == 0 {
		return nil, ErrNoMessage
	}
	return parseMessage(res[0].Messages[0])
}

// Ack removes the message from the group's pending list.
func (c *Client) Ack(ctx context.Context, stream, group, streamID string) error {
	if err := c.rdb.XAck(ctx, stream, group, streamID).Err(); err != nil {
		return fmt.Errorf("xack: %w", err)
	}
	return nil
}

// ClaimStuck reclaims messages pending longer than minIdle. The caller
// (XAUTOCLAIM goroutine) decides the timing and what to do with them.
func (c *Client) ClaimStuck(ctx context.Context, stream, group, consumer string, minIdle time.Duration, count int64) ([]Message, error) {
	res, _, err := c.rdb.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream:   stream,
		Group:    group,
		Consumer: consumer,
		MinIdle:  minIdle,
		Start:    "0",
		Count:    count,
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("xautoclaim: %w", err)
	}
	out := make([]Message, 0, len(res))
	for _, m := range res {
		msg, err := parseMessage(m)
		if err != nil {
			return nil, err
		}
		out = append(out, *msg)
	}
	return out, nil
}

// ConsumeOwnPending reads the next message from this consumer's own PEL
// (already delivered, never acked). Used to drain messages recovered by
// XAUTOCLAIM before pulling new ones.
func (c *Client) ConsumeOwnPending(ctx context.Context, stream, group, consumer string) (*Message, error) {
	res, err := c.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    group,
		Consumer: consumer,
		Streams:  []string{stream, "0"},
		Count:    1,
	}).Result()
	if errors.Is(err, redis.Nil) {
		return nil, ErrNoMessage
	}
	if err != nil {
		return nil, fmt.Errorf("xreadgroup pending: %w", err)
	}
	if len(res) == 0 || len(res[0].Messages) == 0 {
		return nil, ErrNoMessage
	}
	return parseMessage(res[0].Messages[0])
}

func parseMessage(m redis.XMessage) (*Message, error) {
	v, ok := m.Values["delivery_id"]
	if !ok {
		return nil, fmt.Errorf("message %s missing delivery_id field", m.ID)
	}
	s, ok := v.(string)
	if !ok {
		return nil, fmt.Errorf("message %s delivery_id is not a string", m.ID)
	}
	return &Message{StreamID: m.ID, DeliveryID: s}, nil
}
