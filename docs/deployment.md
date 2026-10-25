# Deployment — Webhook Dispatcher

How to deploy the dispatcher to production on a Linux VPS using Docker Compose, integrated with the Nuntix ecosystem.

## Overview

The dispatcher runs on the same Hetzner VPS as the Nuntix Proxy, Bookmarks API, and CloudMonitor.

To optimize resources and simplify maintenance, **the Dispatcher does not run a dedicated Postgres container**. Instead, it connects to a logical database named `webhook_dispatcher` inside the existing `bookmarks-api-db-1` container. It runs on the shared `bookmarks-api_default` Docker network and exposes its HTTP port only to `127.0.0.1:8082` (not to the public internet).

## Prerequisites

- Hetzner CX23 VPS (2 vCPU, 4 GB RAM), Ubuntu 26.04
- Docker Engine and Docker Compose plugin installed
- SSH access to the VPS
- The Bookmarks API stack must be running (for the shared Postgres container).

## 1. Clone the repository

```bash
cd ~
git clone https://github.com/Carlos20052030/webhook-dispatcher.git
cd webhook-dispatcher
```

## 2. Configure environment variables

Copy the example file and fill in the real production values (from KeePassXC):

```bash
cp .env.example .env
vim .env
```

Required variables for production:
- `DATABASE_URL`: Must point to the Bookmarks Postgres container and the `webhook_dispatcher` database.
  ```env
  DATABASE_URL=postgres://bookmarks:<SUA_SENHA_AQUI>@bookmarks-api-db-1:5432/webhook_dispatcher?sslmode=disable
  ```
- `REDIS_ADDR`: `redis:6379`
- `JWT_SECRET`: Must be identical to the Bookmarks API secret.
- `INGEST_TOKEN`: Must be identical to the Bookmarks API token.
- `HTTP_ADDR`: `:8080`

## 3. Configure Docker Compose

Open `docker-compose.yml` and ensure it uses the shared network and maps the port to localhost only:

```yaml
services:
  dispatcher:
    build: .
    container_name: webhook-dispatcher-dispatcher-1
    restart: unless-stopped
    env_file:
      - .env
    ports:
      - "127.0.0.1:8082:8080" # Secure mapping: only accessible from the host
    networks:
      - bookmarks-api_default
    depends_on:
      redis:
        condition: service_healthy

  redis:
    image: redis:7-alpine
    container_name: webhook-dispatcher-redis-1
    restart: unless-stopped
    command: >
      redis-server --appendonly yes --appendfsync everysec
    volumes:
      - redisdata:/data
    networks:
      - bookmarks-api_default
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 5s
      timeout: 3s
      retries: 10

volumes:
  redisdata:

networks:
  bookmarks-api_default:
    external: true
```

## 4. Start the services

```bash
docker compose up -d --build
```

Verify the containers are healthy:

```bash
docker ps
```

Check the logs to ensure migrations ran and the worker started:

```bash
docker logs -f webhook-dispatcher-dispatcher-1
```

Expected output:
```json
{"msg":"migrations applied"}
{"msg":"redis ok"}
{"msg":"http listening","addr":":8080"}
{"msg":"worker started"}
```

*(Press `Ctrl+C` to exit the log viewer).*

## 5. Integration with Bookmarks API

Because both containers are on the same Docker network (`bookmarks-api_default`), the Bookmarks API can reach the Dispatcher using its internal container name, avoiding external ports entirely.

In the `~/bookmarks-api/.env` file, ensure these variables are set:

```env
# In ~/bookmarks-api/.env
DISPATCHER_URL=http://webhook-dispatcher-dispatcher-1:8080
INGEST_TOKEN=<same-token-as-dispatcher>
```

**Important Docker cache warning:** After changing the `.env` file, simply running `docker compose up -d` might not reload the new variables. You must force a recreate:

```bash
cd ~/bookmarks-api
docker compose up -d --force-recreate
```

## 6. Verification (End-to-End)

Create a bookmark in the Bookmarks API (running behind the Nuntix Proxy) and check the dispatcher logs:

```bash
# Using the public URL via Nuntix Proxy
curl -i -X POST https://api.nuntix.com.br/bookmarks \
  -H "Authorization: Bearer <your-jwt-token>" \
  -H "Content-Type: application/json" \
  -d '{"url":"https://example.com","title":"Production Test"}'
```

The dispatcher should log `scheduler dispatched count=1`. If the target webhook is reachable, the delivery will be marked `succeeded` in the `deliveries` table.

## Rollback

If a deployment goes wrong:

```bash
git checkout <previous-commit>
docker compose up -d --build
```
```