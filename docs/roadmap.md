# Roadmap — Webhook Dispatcher

> **Status:** V1.0 is complete and live in production.

This document records the delivery of V1 and outlines the roadmap for future product versions.

## V1 — Completed

### Phase 1 — Foundation (~3 days) ✅
**Deliverable:** Service boots, connects to Postgres and Redis, applies schema, `GET /healthz` returns 200.
- [x] `go mod init`, directory layout.
- [x] `internal/config`: load env, fail-fast.
- [x] `log/slog` with JSON handler.
- [x] Migration runner with `//go:embed`.
- [x] `docker-compose.yml` for local dev.
- [x] `main.go` with `/healthz` and graceful shutdown.

### Phase 2 — Core HTTP (~4 days) ✅
**Deliverable:** Webhook CRUD and event ingestion working, with tests.
- [x] JWT middleware and Ingest-token middleware.
- [x] `POST /webhooks`, `GET /webhooks`, `DELETE /webhooks/{id}`.
- [x] `IngestEvent` (idempotent transaction over `seen_events` + `deliveries`).
- [x] `POST /events` with `MaxBytesReader`.

### Phase 3 — Delivery (~5 days) ✅
**Deliverable:** Message leaves Postgres, crosses the stream, is delivered with signature, retried, and dead-lettered.
- [x] Redis Streams wrapper (`XADD`, `XREADGROUP`, `XACK`, `XAUTOCLAIM`).
- [x] Scheduler goroutine (tick 5s, `FOR UPDATE SKIP LOCKED`).
- [x] Worker loop with state machine.
- [x] HMAC-SHA256 signature.
- [x] Exponential backoff with jitter.
- [x] Reclaimer for crashed workers.

### Phase 4 — Integration & Production (~4 days) ✅
**Deliverable:** Bookmarks API publishing, dispatcher running on the VPS, `GET /deliveries` auditable.
- [x] `GET /deliveries` with cursor pagination.
- [x] Fire-and-forget client in the Bookmarks API.
- [x] Multi-stage Dockerfile.
- [x] Deployed to VPS via Docker Compose (Port 8082).
- [x] Manual E2E tested in production.

---

## V2 — Product Vision (Future Work)

The goal for V2 is to evolve the Dispatcher from an internal service to a multi-tenant SaaS product.

### Phase 5 — Multi-Tenant Core
- [ ] Tenant provisioning API (create/delete tenants, issue specific API keys).
- [ ] Database isolation (schema-per-tenant or row-level security).
- [ ] Dynamic rate limiting per tenant.

### Phase 6 — Observability & Dashboard
- [ ] Prometheus metrics (`/metrics` endpoint).
- [ ] Web dashboard for users to view delivery logs and replay failed events.
- [ ] Alerting (email/Slack) when a tenant's endpoint is consistently failing.

### Phase 7 — Commercial Features
- [ ] Billing integration (Stripe).
- [ ] API usage quotas.
- [ ] SLA guarantees.