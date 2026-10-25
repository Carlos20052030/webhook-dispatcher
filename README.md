# Webhook Dispatcher

**An event-driven, fault-tolerant webhook delivery service built with Go and Redis Streams.**

## The Problem

Sending webhooks reliably is hard. When a user's server is down, you need retries with exponential backoff. If your system crashes, you need to ensure messages aren't lost. And if you send the same event twice, the receiver needs to know it's a duplicate.

This project solves that. It acts as a middleman between your application (like the Nuntix Bookmarks API) and your users' webhook endpoints, ensuring at-least-once delivery.

## Features

- **At-Least-Once Delivery:** Powered by Redis Streams and Consumer Groups.
- **Exponential Backoff with Jitter:** Prevents thundering herd problems.
- **Idempotency:** Every delivery carries a stable `X-Webhook-Delivery-ID` header.
- **Fault Tolerance:** A background reclaimer uses `XAUTOCLAIM` to recover messages from crashed workers.
- **Security:** HMAC-SHA256 signatures (`X-Webhook-Signature`) to verify payload authenticity.
- **Audit Log:** Persistent PostgreSQL log of every attempt (status code, duration, error).

## Architecture

The system follows a Fire-and-Forget ingestion pattern. The API receives an event, saves it to PostgreSQL (source of truth), and a background scheduler pushes it to a Redis Stream. Workers consume the stream, execute the HTTP POST with HMAC signing, and handle retries.

*(Note: The Dispatcher shares the PostgreSQL container with the Bookmarks API, utilizing a separate logical database named `webhook_dispatcher`).*

```text
[Client API] -> (Fire and Forget) -> [Shared Postgres]
                                          |
                                     [Scheduler]
                                          |
                                          v
[Target Webhook] <- (HMAC POST) <- [Worker] <- [Redis Stream]
```

## Stack

- **Language:** Go 1.27 (no frameworks, standard library `net/http`)
- **Database:** PostgreSQL 17 (shared container with Bookmarks API)
- **Transport:** Redis 7 (AOF enabled, consumer groups)
- **Deployment:** Docker & Docker Compose (connected to the `bookmarks-api_default` network)

## Local Development

Because the database is shared, ensure the `bookmarks-api` stack is running (or start a standalone Postgres on port 5432).

1. Configure the environment:
   ```bash
   cp .env.example .env
   # Fill in DATABASE_URL (pointing to the Bookmarks Postgres), JWT_SECRET, INGEST_TOKEN, REDIS_ADDR
   ```

2. Start the local Redis and the Dispatcher:
   ```bash
   docker compose up -d redis
   go run ./cmd/dispatcher
   ```

3. Verify it's running:
   ```bash
   curl http://localhost:8080/healthz
   ```

## Testing

The project has 65+ tests covering the state machine, HMAC signatures, retry logic, and database transactions.

```bash
make test
```

## Docs

- [Scope](docs/scope.md) — problem, decisions, success criteria
- [Architecture](docs/architecture.md) — component diagram, state machine, failure modes
- [Deployment](docs/deployment.md) — how to run in production
- [Postmortem](docs/postmortem.md) — issues found during development and deployment

## License

MIT — see [LICENSE](LICENSE).
```
