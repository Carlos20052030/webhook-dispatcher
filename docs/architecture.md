# Architecture — Webhook Dispatcher

<!-- TODO: written during Phase 2–3, once decisions leave the paper.
Expected contents:
- component diagram (dispatcher, worker, scheduler, XAUTOCLAIM, Redis, Postgres)
- delivery state machine (reference scope.md)
- HMAC signature contract
- retry/backoff flow, Postgres as source of truth
- why Redis Streams over RabbitMQ
- idempotency model (seen_events + X-Delivery-ID)
-->