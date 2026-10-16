# Architecture — Webhook Dispatcher

## Overview

Event-driven service that receives domain events from the Bookmarks API
and delivers them to user-registered webhook endpoints. At-least-once
delivery with exponential backoff, idempotency via stable delivery IDs,
and dead-letter after max attempts.

Postgres is the source of truth. Redis Streams is transport only.

## Components

```
                 +------------------+
   Bookmarks --->| POST /events     |
                 | (ingest token)   |
                 +--------+---------+
                          | tx: seen_events + deliveries(pending)
                          v
                 +------------------+
                 | Postgres         |
                 | seen_events      |
                 | deliveries       |<--+
                 | delivery_attempts|   |
                 | endpoints        |   |
                 +--------+---------+   |
                          ^             |
                          | UPDATE      | UPDATE
                          |             |
   +----------+    +------+------+   +--+-----------+
   |Scheduler |--->| Redis       |-->| Worker       |
   | (5s tick)|    | Stream      |   | (consumer)   |
   |          |    | deliveries  |   |              |
   +----------+    +-------------+   +------+-------+
                          ^                 |
                          | XAUTOCLAIM      | HTTP POST
                          |                 | + HMAC
                   +------+------+          v
                   | Reclaimer   |   +-------------+
                   | (30s tick)  |   | Webhook     |
                   +-------------+   | target      |
                                     +-------------+
```

## Delivery state machine

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

- Terminal: `succeeded`, `dead`.
- `in_flight` for > 60s without ack → Reclaimer pulls it back, worker
  drains own PEL, transitions back to `in_flight` on retry.

## Why Redis Streams (not RabbitMQ)

The explicit goal of Project 3 is to **learn** retry and idempotency.
RabbitMQ gives retry via TTL + DLX almost for free — the learner
configures instead of builds. Redis Streams forces:

- Consumer groups and PEL management
- Explicit `XACK` timing
- Manual `XAUTOCLAIM` for stuck-message recovery
- A scheduler in Postgres for delayed retries (Redis has no built-in
  delayed queue)

This is the same pattern AWS SQS and RabbitMQ use under the hood, just
built by hand.

## Why Postgres holds retry state

Redis could hold `next_attempt_at` in a ZSET, but then two writes
(`ZADD` + `XACK`) would need to be atomic. They are not. A crash
between them either loses the message or duplicates it.

Postgres keeps one source of truth. Redis is only the transport
between "delivery is due" and "worker is picking it up".

## Idempotency model

Two layers:

1. **Ingestion:** `seen_events(event_id)`. A `INSERT ... ON CONFLICT DO
   NOTHING` returns 0 rows for a duplicate event, and the dispatcher
   returns `200 {"status":"duplicate"}` without creating deliveries.

2. **Delivery:** `X-Webhook-Delivery-ID` header is the `deliveries.id`
   (UUIDv7), stable across every retry of the same (event, endpoint)
   pair. Consumers dedupe on it.

## HMAC signature

Every delivery carries:

- `X-Webhook-Timestamp: <unix_seconds>`
- `X-Webhook-Signature: v1=<hex_hmac_sha256>`
- `X-Webhook-Delivery-ID: <uuid_v7>`
- `X-Webhook-Event-Type: <event.type>`

Signed string: `<unix_seconds>.<raw_body>` (Stripe v1 style).

Per-endpoint secret: 32 random bytes, hex-encoded (64 chars), shown
once at creation and never returned again.

Consumer tolerance window: ±300 seconds.

## Retry policy

- Max attempts: 10 (1 initial + 9 retries).
- Base: 1s. Multiplier: 2. Cap: 1h.
- Full jitter: `sleep = rand(0, min(cap, base * 2^attempt))`.
- Classification:
    - 2xx → succeeded
    - 408, 429, 5xx, network errors → retry
    - 4xx (except 408, 429) → dead immediately
    - 3xx → permanent (worker never follows redirects)

## Component reference

| Component | Package | Tick | Notes |
|-----------|---------|------|-------|
| HTTP router | `internal/api` | — | JWT for `/webhooks`, ingest token for `/events` |
| Store | `internal/store` | — | `database/sql` + pgx driver, explicit SQL |
| Scheduler | `internal/worker` | 5s | Only writer to the stream |
| Worker | `internal/worker` | 2s block | Consumer group, HMAC delivery |
| Reclaimer | `internal/worker` | 30s | `XAUTOCLAIM`, MinIdle 60s |
| Retention | `internal/worker` | 1h | Prunes at 03:00 UTC |
| Queue | `internal/queue` | — | Thin Redis Streams wrapper |
| ID generation | `internal/idgen` | — | UUIDv7 |

## Database schema

Full schema is in `migrations/001_init.sql`. Key points:

- `deliveries.id` is UUIDv7, time-ordered, used as the cursor for
  `GET /deliveries` pagination and as the `X-Delivery-ID` header.
- `deliveries.user_id` is denormalized from `endpoints` to avoid a
  JOIN in the audit query.
- Indexes:
    - `deliveries_user_idx` (user_id, id DESC) — pagination.
    - `deliveries_endpoint_idx` (endpoint_id, id DESC) — per-endpoint view.
    - `deliveries_due_idx` (next_attempt_at) WHERE status = 'pending' —
      scheduler's hot path. Partial index keeps it small.
- Dead rows are never pruned automatically.

## Failure modes

| Failure | Detection | Recovery |
|---------|-----------|----------|
| Worker crashes mid-handle | `in_flight` stuck > 60s | Reclaimer → worker drains own PEL |
| Postgres down | healthcheck 503 | Scheduler and worker stop, HTTP still serves |
| Redis down | healthcheck 503 | Publish/consume fails, retried on next tick |
| Target 5xx forever | attempts >= 10 | Marked `dead`, queryable via `GET /deliveries` |
| Duplicate ingestion | `seen_events` conflict | 200 duplicate, no new deliveries |
| Scheduler fails post-XADD | `UPDATE` after `XADD` throws | Row stays pending, next tick re-XADDs; worker accepts `pending|queued` |