# Deployment — Webhook Dispatcher

How to deploy the dispatcher to production on a Linux VPS using Docker Compose.

## Overview

The dispatcher runs on the same Hetzner VPS as the Bookmarks API and CloudMonitor. Because the Bookmarks API already uses port 8080, the dispatcher is exposed on port **8082** externally to avoid port conflicts.

## Prerequisites

- Hetzner CX23 VPS (2 vCPU, 4 GB RAM), Ubuntu 26.04
- Docker Engine and Docker Compose plugin installed
- SSH access to the VPS

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
- `DATABASE_URL`: Postgres connection string (inside Docker: `postgres:5432`)
- `REDIS_ADDR`: Redis address (inside Docker: `redis:6379`)
- `JWT_SECRET`: Must be identical to the Bookmarks API secret.
- `INGEST_TOKEN`: Must be identical to the Bookmarks API token.
- `HTTP_ADDR`: `:8080`

## 3. Configure Docker Compose Port Mapping

Because the Bookmarks API is already using port 8080 on the host, you must change the external port mapping for the dispatcher.

Edit `docker-compose.yml` and change the `ports` section for the `dispatcher` service:

```yaml
  dispatcher:
    # ...
    ports:
      - "8082:8080" # Maps host port 8082 to container port 8080
```

## 4. Start the services

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

*(Press `Ctrl+C` to exit the log viewer).*

## 5. Integration with Bookmarks API

To allow the Bookmarks API to send events to the dispatcher, the `DISPATCHER_URL` variable in the Bookmarks API `.env` must point to the dispatcher's external port.

Since both run on the same host, use the Docker bridge gateway IP or the `host.docker.internal` alias, pointing to port **8082**:

```env
# In ~/bookmarks-api/.env
DISPATCHER_URL=http://host.docker.internal:8082
INGEST_TOKEN=<same-token-as-dispatcher>
```

**Important Docker cache warning:** After changing the `.env` file, simply running `docker compose up -d` might not reload the new variables. You must force a recreate:

```bash
cd ~/bookmarks-api
docker compose down
docker compose up -d
```

## 6. Verification (End-to-End)

Create a bookmark in the Bookmarks API (running on port 8081) and check the dispatcher logs:

```bash
# On the Bookmarks API
curl -i -X POST http://localhost:8081/bookmarks \
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
