package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/redis/go-redis/v9"

	"github.com/Carlos20052030/webhook-dispatcher/internal/api"
	"github.com/Carlos20052030/webhook-dispatcher/internal/config"
	"github.com/Carlos20052030/webhook-dispatcher/internal/store"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: cfg.LogLevel,
	})))

	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open database/sql: %w", err)
	}
	defer func() { _ = db.Close() }()

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("ping postgres: %w", err)
	}
	slog.Info("postgres ok")

	if err := store.Migrate(ctx, db); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	slog.Info("migrations applied")

	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	defer func() { _ = rdb.Close() }()

	if err := rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("ping redis: %w", err)
	}
	slog.Info("redis ok")

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           newRouter(db, rdb, cfg),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("http listening", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil &&
			!errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		slog.Info("shutdown signal received")
	case err := <-errCh:
		return fmt.Errorf("http server: %w", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// newRouter wires the four public routes with their middlewares. Health
// is open; /webhooks needs a user JWT; /events needs the ingest token.
func newRouter(db *sql.DB, rdb *redis.Client, cfg *config.Config) http.Handler {
	s := store.New(db)
	wh := &api.WebhookHandler{Store: s}
	ev := &api.EventHandler{Store: s}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz(db, rdb))

	mux.Handle("POST /webhooks",
		api.RequireJWT(cfg.JWTSecret)(http.HandlerFunc(wh.Create)))
	mux.Handle("GET /webhooks",
		api.RequireJWT(cfg.JWTSecret)(http.HandlerFunc(wh.List)))
	mux.Handle("DELETE /webhooks/{id}",
		api.RequireJWT(cfg.JWTSecret)(http.HandlerFunc(wh.Delete)))

	mux.Handle("POST /events",
		api.RequireIngestToken(cfg.IngestToken)(http.HandlerFunc(ev.Ingest)))

	return mux
}

func healthz(db *sql.DB, rdb *redis.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		if err := db.PingContext(ctx); err != nil {
			http.Error(w, "postgres: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		if err := rdb.Ping(ctx).Err(); err != nil {
			http.Error(w, "redis: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}
}
