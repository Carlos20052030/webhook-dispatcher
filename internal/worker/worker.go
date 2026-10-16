package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/Carlos20052030/webhook-dispatcher/internal/queue"
	"github.com/Carlos20052030/webhook-dispatcher/internal/store"
)

// Store is what the worker needs. Consumer-side interface — the real
// *store.Store satisfies it, and so does a hand-written fake.
type Store interface {
	GetDeliveryForWork(ctx context.Context, id string) (*store.DeliveryJob, error)
	MarkInFlight(ctx context.Context, id string) (int, bool, error)
	RecordAttempt(ctx context.Context, a store.Attempt) error
	MarkSucceeded(ctx context.Context, id string) error
	MarkPendingRetry(ctx context.Context, id string, next time.Time) error
	MarkDead(ctx context.Context, id string) error
}

type Worker struct {
	Store       Store
	Queue       *queue.Client
	Client      *Client
	Stream      string
	Group       string
	ID          string // consumer name inside the group
	MaxAttempts int

	RetryBase time.Duration
	RetryCap  time.Duration

	Block time.Duration
}

func NewWorker(s Store, q *queue.Client) *Worker {
	host, _ := hostname()
	return &Worker{
		Store:       s,
		Queue:       q,
		Client:      NewClient(),
		Stream:      queue.DefaultStream,
		Group:       queue.DefaultGroup,
		ID:          "worker-" + host,
		MaxAttempts: 10,
		RetryBase:   time.Second,
		RetryCap:    time.Hour,
		Block:       2 * time.Second,
	}
}

// Run blocks until ctx is done, consuming one message at a time.
// Returns the first fatal queue error.
func (w *Worker) Run(ctx context.Context) error {
	if err := w.Queue.EnsureGroup(ctx, w.Stream, w.Group); err != nil {
		return fmt.Errorf("ensure group: %w", err)
	}
	slog.Info("worker started", "consumer", w.ID)

	for {
		if ctx.Err() != nil {
			slog.Info("worker stopping", "consumer", w.ID)
			return nil
		}
		msg, err := w.Queue.Consume(ctx, w.Stream, w.Group, w.ID, w.Block)
		if errors.Is(err, queue.ErrNoMessage) {
			continue
		}
		if err != nil {
			return fmt.Errorf("consume: %w", err)
		}
		w.handle(ctx, msg)
	}
}

// handle processes one message. Every path must end with either an
// XACK (message handled, including intentional skip) or a returned
// error that stops the worker. Leaving it pending makes XAUTOCLAIM
// pick it up later.
func (w *Worker) handle(ctx context.Context, msg *queue.Message) {
	attempt, ok, err := w.Store.MarkInFlight(ctx, msg.DeliveryID)
	if err != nil {
		slog.Error("mark in_flight failed", "delivery_id", msg.DeliveryID, "err", err)
		return // leave pending; XAUTOCLAIM will retry
	}
	if !ok {
		// Duplicate message or terminal delivery. Ack and move on.
		if err := w.Queue.Ack(ctx, w.Stream, w.Group, msg.StreamID); err != nil {
			slog.Error("ack on skip failed", "stream_id", msg.StreamID, "err", err)
		}
		return
	}

	job, err := w.Store.GetDeliveryForWork(ctx, msg.DeliveryID)
	if err != nil {
		slog.Error("load delivery failed", "delivery_id", msg.DeliveryID, "err", err)
		// Delivery is now in_flight but no target — mark dead to unblock.
		_ = w.Store.MarkDead(ctx, msg.DeliveryID)
		_ = w.Queue.Ack(ctx, w.Stream, w.Group, msg.StreamID)
		return
	}

	res := w.Client.Send(ctx, DeliveryPayload{
		DeliveryID: job.DeliveryID,
		EventType:  job.EventType,
		Body:       json.RawMessage(job.Payload),
		Secret:     []byte(job.Secret),
		TargetURL:  job.TargetURL,
	})

	startedAt := time.Now().Add(-res.Duration)
	attemptRow := store.Attempt{
		DeliveryID:    job.DeliveryID,
		AttemptNumber: attempt,
		StartedAt:     startedAt,
		FinishedAt:    time.Now().UTC(),
		StatusCode:    res.StatusCode,
		Err:           errString(res.Err),
		DurationMs:    int(res.Duration.Milliseconds()),
	}
	if err := w.Store.RecordAttempt(ctx, attemptRow); err != nil {
		slog.Error("record attempt failed", "delivery_id", job.DeliveryID, "err", err)
	}

	class := Classify(res)
	switch class {
	case ClassSuccess:
		if err := w.Store.MarkSucceeded(ctx, job.DeliveryID); err != nil {
			slog.Error("mark succeeded failed", "delivery_id", job.DeliveryID, "err", err)
		}
	case ClassPermanent:
		if err := w.Store.MarkDead(ctx, job.DeliveryID); err != nil {
			slog.Error("mark dead failed", "delivery_id", job.DeliveryID, "err", err)
		}
	case ClassTransient:
		if attempt >= w.MaxAttempts {
			if err := w.Store.MarkDead(ctx, job.DeliveryID); err != nil {
				slog.Error("mark dead (max attempts) failed", "delivery_id", job.DeliveryID, "err", err)
			}
		} else {
			next := time.Now().UTC().Add(Backoff(w.RetryBase, w.RetryCap, attempt))
			if err := w.Store.MarkPendingRetry(ctx, job.DeliveryID, next); err != nil {
				slog.Error("mark retry failed", "delivery_id", job.DeliveryID, "err", err)
			}
		}
	}

	if err := w.Queue.Ack(ctx, w.Stream, w.Group, msg.StreamID); err != nil {
		slog.Error("ack failed", "stream_id", msg.StreamID, "err", err)
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func hostname() (string, error) {
	h, err := os.Hostname()
	if err != nil {
		return "unknown", err
	}
	return h, nil
}
