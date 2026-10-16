# webhook-dispatcher

An event-driven service that receives domain events from the
[bookmarks-api](https://github.com/Carlos20052030/bookmarks-api) and
delivers them to user-registered webhook endpoints with at-least-once
semantics, retry with exponential backoff, and idempotent delivery.

**Status:** under active development. Phase 3 of 4 (delivery).

## Stack

- Go 1.27, `net/http`, `database/sql` + `pgx/v5`, `log/slog`
- Postgres 17 (state), Redis Streams (transport)
- No framework, no ORM, no mock library

## Docs

- [scope](docs/scope.md) — problem, decisions, success criteria
- [roadmap](docs/roadmap.md) — phases and tasks
- [architecture](docs/architecture.md) — TBD, written during Phase 3
- [deployment](docs/deployment.md) — TBD, written during Phase 4

## Local development

```bash
cp .env.example .env       # fill in JWT_SECRET and INGEST_TOKEN
docker compose up -d       # Postgres 17, Redis 7 (AOF)
go run ./cmd/dispatcher
curl http://localhost:8080/healthz
```

## License

MIT — see [LICENSE](LICENSE).
