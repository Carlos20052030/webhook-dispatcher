# Postmortem — Webhook Dispatcher V1.1

This document records the real issues encountered during development
and integration, and what was learned from them.

## 1. Docker networking: `host.docker.internal` does not work on Linux

**Problem:** During integration, the Bookmarks API could not reach the
dispatcher. Logs showed `dial tcp: lookup host.docker.internal: no such host`.

**Root cause:** `host.docker.internal` is a feature of Docker Desktop
(Mac/Windows). On native Linux, Docker does not resolve this hostname
by default.

**Fix:** Added `extra_hosts: - "host.docker.internal:host-gateway"` to
the Bookmarks API `docker-compose.yml`. This maps the hostname to the
Docker bridge gateway, allowing the container to reach services running
on the host.
*(Later updated in V1.1 to use the native Docker network DNS, which is much cleaner).*

**Lesson:** Never assume Docker Desktop features exist on Linux. Always
test networking on the target OS.

## 2. Port conflict (8080)

**Problem:** Running both the dispatcher and the Bookmarks API locally
caused a `port is already allocated` error.

**Root cause:** Both services were configured to listen on port 8080 on
the host.

**Fix:** Mapped the Bookmarks API to host port 8081 (`"8081:8080"` in
Compose) while keeping the internal container port at 8080. The Dispatcher was mapped to 8082.

**Lesson:** Container ports and host ports are independent. Use host
port mapping to resolve conflicts without changing application code.

## 3. JWT expired during manual testing

**Problem:** Creating a bookmark returned `401 Unauthorized` even with
a valid token.

**Root cause:** The JWT access token has a 15-minute TTL (`expires_in:
900`). By the time the `curl` was executed, the token had expired.

**Fix:** Regenerated the token via `POST /auth/login` immediately before
testing.

**Lesson:** Access tokens are short-lived by design. For long testing
sessions, automate the login step or use a refresh token.

## 4. GitHub rejected push due to email privacy

**Problem:** `git push` failed with `GH007: Your push would publish a
private email address`.

**Root cause:** Local commits were authored with a real email, but the
GitHub account had "Block command line pushes that expose my email"
enabled.

**Fix:** Configured `git config --local user.email` to use the GitHub
`noreply` address, then ran `git commit --amend --reset-author` to
rewrite the commit.

**Lesson:** Always set the `noreply` email in Git config when privacy
protection is enabled on GitHub. The amend command fixes commits
retroactively.

## 5. The Abuse Report (Redis Exposed to the Internet)

**Problem:** We received an alert from Hetzner (originating from the German CERT-Bund) stating that our Redis container was openly accessible from the internet and vulnerable to data theft.

**Root cause:** The `docker-compose.yml` was mapping Redis to `0.0.0.0:6379`. Docker bypasses the UFW firewall entirely when using the `ports:` directive, meaning the port was exposed to the whole world despite the firewall being "active".

**Fix:**
1. Immediately blocked the port using `sudo ufw deny 6379`.
2. Changed the Docker Compose mapping to bind only to the localhost interface: `127.0.0.1:6379:6379`.
3. Purged the Redis data (`docker compose down -v`) to ensure no malicious payloads were left behind.

**Lesson:** **Never** map a database or cache port without specifying `127.0.0.1` on the left side. Docker's port mapping is a security trap that bypasses standard Linux firewalls.

## 6. Database Consolidation (The end of over-engineering)

**Problem:** The VPS was running two separate Postgres containers (one for Bookmarks, one for the Dispatcher). This was wasting RAM, duplicating backup routines, and violating the `scope.md`.

**Root cause:** The initial setup assumed every microservice needed its own database container.

**Fix:**
1. Dumped the Dispatcher database using `pg_dump`.
2. Created a logical database named `webhook_dispatcher` inside the existing `bookmarks-api-db-1` container.
3. Restored the dump into the new logical database.
4. Updated the `docker-compose.yml` to remove the dedicated Postgres container and connect the Dispatcher to the `bookmarks-api_default` network.

**Lesson:** For small-to-medium SaaS, a "Shared Database" (logical isolation within a single container) is much more cost-effective and easier to maintain than one database container per service.

## 7. Docker Networking Evolution (Host vs. Bridge)

**Problem:** The Dispatcher and the Bookmarks API needed to communicate, but they were in different network contexts. The Proxy runs in `network_mode: "host"`, while the others run in a Docker bridge network.

**Root cause:** Mixing host networking and bridge networking complicates DNS resolution. The `host.docker.internal` workaround was brittle.

**Fix:** We moved all internal communication to the `bookmarks-api_default` Docker network.
- The Proxy (host) reaches the database via `127.0.0.1:5432` (mapped to localhost).
- The Dispatcher (bridge) reaches the database via internal DNS: `bookmarks-api-db-1:5432`.
- The Bookmarks API reaches the Dispatcher via internal DNS: `http://webhook-dispatcher-dispatcher-1:8080`.

**Lesson:** Understand the difference between `network_mode: "host"` (shares the host's loopback) and standard bridge networks (isolated, uses internal DNS). Always prefer internal Docker DNS when containers are on the same network.