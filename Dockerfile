# ---- build ----
FROM golang:1.27-alpine AS builder

WORKDIR /app

# Copy only what's needed to download deps (leverages layer cache).
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO off ensures a static binary. ldflags strips debug info (-s -w).
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w" \
    -o /out/dispatcher ./cmd/dispatcher

# ---- runtime ----
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY --from=builder /out/dispatcher /app/dispatcher

# Non-root user. Alpine creates UID 10001 without a shell.
RUN adduser -D -u 10001 appuser
USER appuser

EXPOSE 8080

ENTRYPOINT ["/app/dispatcher"]