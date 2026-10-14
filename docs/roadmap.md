# Roadmap — Webhook Dispatcher

Four phases, no calendar dates. Each phase ends with a verifiable
deliverable. Start of the next phase requires the previous one green.

## Phase 1 — Foundation (~3 days)

**Deliverable:** service boots, connects to Postgres and Redis, applies
schema, `GET /healthz` returns 200.

Tasks:

1. `go mod init`, directory layout (`cmd/dispatcher`,
   `internal/{config,api,queue,worker,delivery,store}`).
2. `internal/config`: load env, validate presence, fail-fast with a
   useful message.
3. `log/slog` with JSON handler, level via env.
4. `internal/store/migrations/*.sql` + runner with `//go:embed`,
   idempotent (`schema_migrations` table), each migration in a
   transaction.
5. Apply full schema (5 tables + indexes) on boot.
6. Local `docker-compose.yml`: Postgres 17 + Redis 7 (`appendonly yes`).
7. `cmd/dispatcher/main.go`: open `pgx/v5` pool, ping Postgres and
   Redis, start `http.Server` with `/healthz`.
8. Config test (missing env fails) and migration test (runs twice, no
   error).

**Verification:** `docker compose up`, `curl /healthz` → 200,
`psql \dt` shows the 5 tables.

## Phase 2 — Core HTTP (~4 days)

**Deliverable:** webhook CRUD and event ingestion working, with tests.

Tasks:

1. JWT middleware (validate shared `JWT_SECRET`, extract `sub`,
   inject into `context.Context`).
2. Ingest-token middleware (`subtle.ConstantTimeCompare` on
   `INGEST_TOKEN`).
3. `internal/store/endpoints.go`: `CreateEndpoint`, `ListEndpointsByUser`,
   `DeleteEndpoint`, `InsertSubscriptions`.
4. `POST /webhooks` — validate URL (http/https only), generate 32-byte
   hex secret, INSERT in a transaction, return 201 with secret.
5. `GET /webhooks` — list only the caller's `sub`, secret omitted.
6. `DELETE /webhooks/{id}` — 404 if `user_id` does not match.
7. `internal/store/events.go`: `IngestEvent` — transaction over
   `seen_events` + N `deliveries` (`status='pending'`,
   `next_attempt_at=now()`).
8. `POST /events` — `MaxBytesReader` 64 KiB, call `IngestEvent`,
   202 or 200 duplicate.
9. Hand-written store fakes for handler tests.
10. Tests covering scope criteria 1–7.

**Verification:** suite green; inserting an event leaves `deliveries`
rows with `next_attempt_at` set.

## Phase 3 — Delivery (~5 days)

**Deliverable:** message leaves Postgres, crosses the stream, is
delivered with signature, retried with backoff, and dead-letters after
the limit.

Tasks:

1. `internal/queue`: thin wrapper over `XADD`, `XREADGROUP`, `XACK`,
   `XAUTOCLAIM` using `redis/go-redis/v9`.
2. Scheduler goroutine (5s tick): query `FOR UPDATE SKIP LOCKED LIMIT 100`,
   `XADD` + `UPDATE status='queued'`.
3. Worker: `XREADGROUP` loop,
   `UPDATE ... WHERE status IN ('pending','queued')`, `XACK` on zero
   rows.
4. `internal/delivery`: HTTP client with
   `CheckRedirect = ErrUseLastResponse`, 10s timeout.
5. HMAC-SHA256 signature (`<ts>.<body>`, `X-Webhook-*` headers).
6. Status classification (2xx / 4xx≠408,429 / transient) and
   `delivery_attempts` write.
7. Backoff calc with full jitter, `RETRY_BASE` and `RETRY_CAP` via env.
8. `XAUTOCLAIM` goroutine (30s tick, 60s MIN-IDLE) → reset to `queued`.
9. Nightly retention goroutine (`RETENTION_*` envs, skips `dead`).
10. Tests with `RETRY_BASE=1ms`, `RETRY_CAP=5ms`, `httptest.Server`
    target.

**Verification:** criteria 8–16 pass. Always-500 target → 10 attempts in
`delivery_attempts`, `status='dead'`.

## Phase 4 — Integration & Production (~4 days)

**Deliverable:** Bookmarks API publishing, dispatcher running on the
VPS, `GET /deliveries` auditable.

Tasks:

1. `GET /deliveries` with filters (`endpoint_id`, `status`,
   `event_type`, `from`, `to`) and cursor (`user_id, id DESC`).
2. Fire-and-forget client in the Bookmarks API: buffered channel of 256,
   2 goroutines, `context.Background()` + 2s, drop + `slog.Warn` on full
   channel.
3. Call dispatcher from the bookmark creation handler (additive,
   non-blocking).
4. `Dockerfile` multi-stage (`CGO_ENABLED=0`, alpine).
5. New service in the VPS `docker-compose.yml`, shared network, `.env`
   with `JWT_SECRET` and `INGEST_TOKEN` (never in chat).
6. Deploy: bring up, apply migration, point Bookmarks at the dispatcher.
7. Manual E2E: create bookmark → `delivery_attempts` with 2xx → target
   receives a valid signature.
8. Docs: `README.md`, `docs/{scope,architecture,roadmap,deployment,postmortem}.md`
   in the same shape as Projects 1 and 2.

**Verification:** criteria 4, 17, 18 measured in production. Smoke test
from item 7 green.