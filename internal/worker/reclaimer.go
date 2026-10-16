package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/Carlos20052030/webhook-dispatcher/internal/queue"
)

// Reclaimer periodically scans the group's PEL for messages that have
// been idle longer than MinIdle. Those are from a worker that crashed
// mid-handle. Reclaiming them back into this worker's PEL means the
// next ConsumeOwnPending will return them.
type Reclaimer struct {
	Queue    *queue.Client
	Stream   string
	Group    string
	Consumer string
	MinIdle  time.Duration
	Tick     time.Duration
	Count    int64
}

func NewReclaimer(q *queue.Client, consumer string) *Reclaimer {
	return &Reclaimer{
		Queue:    q,
		Stream:   queue.DefaultStream,
		Group:    queue.DefaultGroup,
		Consumer: consumer,
		MinIdle:  60 * time.Second,
		Tick:     30 * time.Second,
		Count:    100,
	}
}

// Run blocks until ctx is done.
func (r *Reclaimer) Run(ctx context.Context) {
	t := time.NewTicker(r.Tick)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("reclaimer stopping")
			return
		case <-t.C:
			msgs, err := r.Queue.ClaimStuck(ctx, r.Stream, r.Group, r.Consumer, r.MinIdle, r.Count)
			if err != nil {
				slog.Error("reclaimer claim failed", "err", err)
				continue
			}
			if len(msgs) > 0 {
				slog.Info("reclaimer claimed", "count", len(msgs))
			}
		}
	}
}
