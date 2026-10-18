# Scope — Webhook Dispatcher

## Problem

Bookmarks API users have no way to be notified when their data changes.
Today, integrating with the API means polling. There is no push mechanism,
no event contract, and no delivery guarantee. Any consumer that wants to
react to `bookmark.created` must build its own polling loop, its own retry,
and its own deduplication — every consumer, every time.

## Objective

Build an event-driven service that receives domain events from the
Bookmarks API and delivers them to user-registered webhook endpoints with
at-least-once semantics, retry with exponential backoff, and idempotent
delivery. V1 proves the full loop end-to-end: event published → queued →
delivered → retried on failure → logged.

## In Scope (V1)

- Webhook endpoint registration per user: create, list, delete.
  Each endpoint has a URL and a set of subscribed event types stored in
  `endpoint_subscriptions`. HMAC secret generated server-side, shown once.
- Event ingestion endpoint (`POST /events`) authenticated with a static
  internal token. One event per call. Postgres-only write path.
- Only `bookmark.created` is emitted and dispatched in V1. Subscriptions
  accept other strings, but no other producer exists yet.
- Redis Streams consumer group with worker pool. Postgres holds retry
  state; Redis is transport only.
- Delivery via HTTP POST with HMAC-SHA256 signature (spec below).
- Retry with exponential backoff plus full jitter, driven by
  `deliveries.next_attempt_at` and a Postgres-backed scheduler.
- Dead-letter state after max attempts. Queryable via delivery log,
  never pruned.
- Persistent delivery log: one row per attempt (event ID, endpoint ID,
  timestamp, HTTP status, error, duration).
- Idempotency at ingestion: duplicate `event.id` is a no-op.
- Stuck-message recovery via `XAUTOCLAIM` for worker crashes.
- Bookmarks API integration: fire-and-forget with bounded queue and
  short timeout. Bookmark creation MUST NOT fail or block if the
  dispatcher is unreachable.

## Out of Scope (V1)

- Any UI, dashboard, or admin panel.
- Multiple queues, priorities, per-event-type routing.
- Scheduled or delayed delivery from the API surface.
- Per-endpoint rate limiting.
- Prometheus / Grafana metrics (Project 6).
- Automatic secret rotation.
- Replay of dead-lettered events.
- Payload-based filtering on the consumer side.
- SSRF protection beyond scheme validation (https and http only).
- Multi-region, HA, or horizontal scaling beyond one process.
- Backfill of historical events.
- `GET /webhooks/{id}` — list, create, delete cover V1.

## User

- Primary: Bookmarks API users who register webhook endpoints.
- Secondary: the Bookmarks API itself, as internal event producer.
- Tertiary (future, not V1): other portfolio services (e.g.
  CloudMonitor emitting `monitor.down`).

## Success Criteria

All verifiable by test or manual run.

1. `POST /webhooks` returns 201 with the HMAC secret shown exactly once.
2. `GET /webhooks` lists only the authenticated user's endpoints.
3. `DELETE /webhooks/{id}` returns 404 for another user's endpoint.
4. `POST /events` with valid token returns 202 within 100ms p99
   (no `XADD` on the request path).
5. `POST /events` with invalid or missing token returns 401.
6. Duplicate `POST /events` with the same `event.id` returns 200 with
   `{"status":"duplicate"}` and creates no new `deliveries` rows.
7. A `deliveries` row is created for every subscribed endpoint of the
   target user in a single transaction with `seen_events`.
8. Scheduler moves a due `pending` row to `queued` and enqueues it on
   the stream. `XADD` failure leaves the row `pending` for the next tick.
9. Worker picks up a `pending` or `queued` message, marks it
   `in_flight`, delivers, and acks. A second message for the same
   `delivery_id` is a no-op.
10. Delivery to a 2xx target sets `status='succeeded'`, writes an
    attempt row, and acks.
11. Delivery to a 5xx target or network error sets `status='pending'`,
    sets `next_attempt_at`, writes an attempt row, and acks.
12. Delivery to a 4xx target (except 408, 429) sets `status='dead'`
    directly, writes an attempt row, and acks.
13. `X-Delivery-ID` is byte-identical across all retries of the same
    (event, endpoint) pair.
14. Every attempt appears in `GET /deliveries` with timestamp, status,
    code, and error string.
15. After max attempts, delivery is in `status='dead'` and queryable.
    Dead rows are never pruned.
16. Killing a worker mid-delivery does not lose the message —
    `XAUTOCLAIM` reclaims it within 60s and resets `status='queued'`.
17. With the dispatcher stopped, `POST /bookmarks` on the Bookmarks API
    returns 201 within its normal p99 budget.
18. Backoff observed in `delivery_attempts.started_at` deltas follows
    `min(cap, base * 2^attempt)` with full jitter, verified with
    `RETRY_BASE=1ms`, `RETRY_CAP=5ms`.

## Decisions

### HMAC delivery signature

- Signed string: `<unix_seconds>.<raw_body>` (Stripe v1 style).
- `X-Webhook-Timestamp: <unix_seconds>` (decimal).
- `X-Webhook-Signature: v1=<hex_hmac_sha256>`. Prefix `v1=` kept for
  future scheme rotation.
- `X-Webhook-Delivery-ID: <uuid_v7>`, stable across retries.
  Equals `deliveries.id`.
- `X-Webhook-Event-Type: <event.type>`.
- Per-endpoint secret: 32 random bytes, hex-encoded (64 chars).
- Consumer tolerance window: ±300 seconds.

### `/events` payload

```json
{
  "id": "UUIDv7",
  "type": "bookmark.created",
  "user_id": "UUID",
  "occurred_at": "RFC3339 UTC",
  "data": {
    "id": "UUID",
    "url": "string",
    "title": "string",
    "tags": ["string"],
    "created_at": "RFC3339 UTC"
  }
}
```

- One event per request. Body limit 64 KiB via `http.MaxBytesReader`.
- `user_id` comes from the request body.
- `occurred_at` is set by the Bookmarks API. Dispatcher propagates.
- `data` mirrors the public Bookmarks payload for `bookmark.created`.

### Delivery state machine

```
pending ──scheduler──> queued ──worker──> in_flight
   ▲                                            │
   │                          ┌─────────────────┼─────────────────┐
   │                          │                 │                 │
   │              2xx         │      5xx/408/429│        4xx/other│
   │                          ▼                 ▼                 ▼
   │                     succeeded         pending(with       dead
   │                                       next_attempt_at)
   │                                          │
   └──────────── scheduler (due) ─────────────┘
```

- `deliveries.status ∈ {pending, queued, in_flight, succeeded, dead}`.
- Terminal states: `succeeded`, `dead`.
- `in_flight` without heartbeat for 60s → `XAUTOCLAIM` resets to `queued`.

### Ingestion

- Single transaction: `INSERT seen_events ON CONFLICT DO NOTHING`, then
  `INSERT deliveries` for each subscribed endpoint of `user_id` with
  `status='pending'` and `next_attempt_at=now()`.
- 0 rows from `seen_events` insert → duplicate, 200
  `{"status":"duplicate"}`, no `deliveries` rows written.
- Commit, return 202. No `XADD` on the request path.

### Scheduler

- Goroutine, tick every 5s.
- Query:
  ```sql
  SELECT id FROM deliveries
   WHERE status = 'pending'
     AND next_attempt_at IS NOT NULL
     AND next_attempt_at <= now()
   FOR UPDATE SKIP LOCKED
   LIMIT 100;
  ```
- For each row: `XADD streams:deliveries * delivery_id <id>`. On success,
  `UPDATE deliveries SET status='queued' WHERE id=$1`. On `XADD` error,
  log, leave the row `pending` — the next tick retries.
- Postgres is the source of truth. Redis is transport.
- If the `UPDATE` after a successful `XADD` fails, the row stays
  `pending` and the next tick re-XADDs. The worker accepts both
  `pending` and `queued`, so the duplicate message is a no-op.

### Worker

- Consumer group `workers` on `streams:deliveries`.
- Per message:
    1. `UPDATE deliveries
        SET status='in_flight', attempts=attempts+1, updated_at=now()
       WHERE id=$1 AND status IN ('pending','queued')
       RETURNING ...`.
       Zero rows → duplicate or terminal, `XACK`, next.
    2. Build signed request, POST with 10s timeout.
    3. Classify response (below).
    4. Write `delivery_attempts` row.
    5. Update `deliveries.status` per classification.
    6. `XACK`.

### XAUTOCLAIM

- Goroutine, every 30s, `XAUTOCLAIM ... MIN-IDLE-TIME 60000`.
- On reclaim, reset the corresponding `deliveries` row to `queued`
  if `status='in_flight'`, then process as normal.

### Backoff testability

- `RETRY_BASE`, `RETRY_CAP` env-configurable.
- Tests use `RETRY_BASE=1ms`, `RETRY_CAP=5ms`. Real `time.Timer`,
  short values. No fake clock.

### Retry defaults

- Max attempts: 10 (1 initial + 9 retries).
- Base: 1s. Multiplier: 2. Cap: 1h.
- Full jitter: `sleep = rand(0, min(cap, base * 2^attempt))`.
- With base=1s and 10 attempts, the largest scheduled delay is 512s.
  The 1h cap is a safety net for future higher attempt counts or
  larger bases — it does not bind in V1.

### HTTP status handling

- `http.Client` with `CheckRedirect` returning `ErrUseLastResponse`.
  Redirects never followed.
- 2xx = success → `succeeded`.
- 408, 429, 5xx, network errors → retry → `pending`.
- 4xx other than 408/429 → permanent → `dead`.

### Retention

- Prune `delivery_attempts` older than 30 days
  (`RETENTION_ATTEMPTS_DAYS=30`).
- Prune `deliveries` older than 90 days where `status <> 'dead'`
  (`RETENTION_DELIVERIES_DAYS=90`). Dead rows are the audit trail
  and are never pruned automatically.
- Nightly goroutine at `RETENTION_RUN_AT=03:00` (UTC).

### Fire-and-forget in Bookmarks API

- Bounded channel of 256 events, 2 worker goroutines.
- Each worker: `context.Background()` + 2s timeout. NOT the request
  context — the request may already be done.
- On full channel: drop, `slog.Warn` with event ID. Bookmark creation
  never blocks.

### User identity

- Dispatcher trusts the `sub` (UUID) claim from the Bookmarks JWT.
- No local `users` table, no foreign key. `endpoints.user_id` and
  `deliveries.user_id` are raw UUIDs. Users live in the Bookmarks DB.
- Shared `JWT_SECRET` via environment. Dispatcher validates, never
  issues.

### Database schema

```sql
CREATE TABLE endpoints (
  id           UUID PRIMARY KEY,
  user_id      UUID NOT NULL,
  url          TEXT NOT NULL,
  secret       TEXT NOT NULL,
  active       BOOLEAN NOT NULL DEFAULT true,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX endpoints_user_idx ON endpoints (user_id);

CREATE TABLE endpoint_subscriptions (
  endpoint_id  UUID NOT NULL REFERENCES endpoints(id) ON DELETE CASCADE,
  event_type   TEXT NOT NULL,
  PRIMARY KEY (endpoint_id, event_type)
);

CREATE TABLE deliveries (
  id                UUID PRIMARY KEY,           -- UUIDv7, = X-Delivery-ID
  event_id          UUID NOT NULL,
  endpoint_id       UUID NOT NULL REFERENCES endpoints(id) ON DELETE CASCADE,
  user_id           UUID NOT NULL,              -- denormalized for queries
  event_type        TEXT NOT NULL,
  payload           JSONB NOT NULL,
  status            TEXT NOT NULL
                      CHECK (status IN
                        ('pending','queued','in_flight','succeeded','dead')),
  attempts          INT NOT NULL DEFAULT 0,
  next_attempt_at   TIMESTAMPTZ,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX deliveries_user_idx     ON deliveries (user_id, id DESC);
CREATE INDEX deliveries_endpoint_idx ON deliveries (endpoint_id, id DESC);
CREATE INDEX deliveries_due_idx      ON deliveries (next_attempt_at)
  WHERE status = 'pending' AND next_attempt_at IS NOT NULL;

CREATE TABLE delivery_attempts (
  id              BIGSERIAL PRIMARY KEY,
  delivery_id     UUID NOT NULL REFERENCES deliveries(id) ON DELETE CASCADE,
  attempt_number  INT NOT NULL,
  started_at      TIMESTAMPTZ NOT NULL,
  finished_at     TIMESTAMPTZ NOT NULL,
  status_code     INT,
  error           TEXT,
  duration_ms     INT NOT NULL
);
CREATE INDEX delivery_attempts_delivery_idx
  ON delivery_attempts (delivery_id, attempt_number);

CREATE TABLE seen_events (
  event_id     UUID PRIMARY KEY,
  received_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

## Constraints

- Go 1.27. `net/http`, `database/sql` + `pgx/v5`, `log/slog`. No
  framework, no ORM, no mock library.
- Queue: Redis Streams. Chosen over RabbitMQ to force building retry
  and dead-letter semantics rather than configuring them.
- Redis runs with AOF enabled: `appendonly yes`,
  `appendfsync everysec`. Bounded loss window of ~1s on hard crash.
- Storage: Postgres 17, separate database (`webhook_dispatcher`) on
  the existing Bookmarks container. No new Postgres container.
- Auth: shared `JWT_SECRET` with Bookmarks API. Same `users` identity.
- Internal ingestion auth: static token in `.env`, 32 random bytes,
  compared with `subtle.ConstantTimeCompare`.
- Deploy: Docker Compose on the existing Hetzner CX23, coexisting
  with CloudMonitor (systemd) and Bookmarks API (Compose).
- Public code (comments, errors, tests, README, docs) in English.
- Bookmarks API is in production. Any change to it must be additive
  and non-blocking.

## Assumptions

- The Bookmarks API can be modified to call `POST /events` on the
  dispatcher. Confirmed before writing this document.
- Dispatcher and Bookmarks run on the same Docker network, so the
  internal call never crosses the public internet.
- Redis is added as a new Compose service on the same VPS.
- Users obtain a valid Bookmarks JWT before calling the dispatcher.
- Webhook targets are HTTPS or HTTP; no assumption about availability.
- Consumers dedupe on `X-Delivery-ID` to make at-least-once safe.
- The up-to-5s delay between `202 Accepted` and stream enqueue is
  acceptable for V1 (scheduler tick).

## Version

V1.0 — Deployed to production (18/10/2026).