# Postmortem — Webhook Dispatcher

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

**Lesson:** Never assume Docker Desktop features exist on Linux. Always
test networking on the target OS.

## 2. Port conflict (8080)

**Problem:** Running both the dispatcher and the Bookmarks API locally
caused a `port is already allocated` error.

**Root cause:** Both services were configured to listen on port 8080 on
the host.

**Fix:** Mapped the Bookmarks API to host port 8081 (`"8081:8080"` in
Compose) while keeping the internal container port at 8080.

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