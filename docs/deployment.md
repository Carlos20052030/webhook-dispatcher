# Deployment — Webhook Dispatcher

## Overview

The dispatcher runs on the same Hetzner VPS as the Bookmarks API and
CloudMonitor. It uses Docker Compose and shares the host network with
the Bookmarks API so they can communicate internally without exposing
the dispatcher to the public internet.

## Prerequisites

- Hetzner CX23 VPS (2 vCPU, 4 GB RAM), Ubuntu 26.04
- Docker Engine and Docker Compose plugin installed
- SSH access to the VPS

## 1. Clone the repository

```bash
git clone git@github.com:Carlos20052030/webhook-dispatcher.git
cd webhook-dispatcher
```

## 2. Configure environment variables

Copy the example file and fill in the real values:

```bash
cp .env.example .env
nano .env
```

Required variables for production:
- `DATABASE_URL`: Postgres connection string
- `REDIS_ADDR`: Redis address (usually `redis:6379` inside Docker)
- `JWT_SECRET`: Must be identical to the Bookmarks API secret
- `INGEST_TOKEN`: Must be identical to the Bookmarks API token
- `HTTP_ADDR`: `:8080`

## 3. Start the services

```bash
docker compose up -d --build
```

Verify the containers are healthy:

```bash
docker compose ps
```

Check the logs to ensure migrations ran and the worker started:

```bash
docker compose logs -f dispatcher
```

Expected output:
```json
{"msg":"postgres ok"}
{"msg":"migrations applied"}
{"msg":"redis ok"}
{"msg":"http listening","addr":":8080"}
{"msg":"worker started"}
```

## 4. Integration with Bookmarks API

To allow the Bookmarks API to send events to the dispatcher, the
`DISPATCHER_URL` variable in the Bookmarks API `.env` must point to the
dispatcher container.

Since both run on the same host, the easiest way is to expose the
dispatcher port on the host machine and configure the Bookmarks API to
use the Docker bridge IP (usually `172.17.0.1`):

```env
# In bookmarks-api/.env
DISPATCHER_URL=http://172.17.0.1:8080
INGEST_TOKEN=<same-token-as-dispatcher>
```

*Note: If both services are in the same Docker Compose network, use
`http://dispatcher:8080` instead.*

## 5. Verification

Create a bookmark and check the dispatcher logs:

```bash
# On the Bookmarks API
curl -X POST http://localhost:8080/bookmarks \
  -H "Authorization: Bearer <jwt>" \
  -H "Content-Type: application/json" \
  -d '{"url":"https://example.com","title":"Test"}'
```

The dispatcher should log `scheduler dispatched count=1`. If the target
webhook is reachable, the delivery will be marked `succeeded` in the
`deliveries` table.

## Rollback

If a deployment goes wrong:

```bash
git checkout <previous-commit>
docker compose up -d --build
```