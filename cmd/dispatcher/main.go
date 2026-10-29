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
	"strings"
	"sync"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/redis/go-redis/v9"

	"github.com/Carlos20052030/webhook-dispatcher/internal/api"
	"github.com/Carlos20052030/webhook-dispatcher/internal/config"
	"github.com/Carlos20052030/webhook-dispatcher/internal/queue"
	"github.com/Carlos20052030/webhook-dispatcher/internal/store"
	"github.com/Carlos20052030/webhook-dispatcher/internal/worker"
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

	s := store.New(db)
	q := queue.New(rdb)

	var wg sync.WaitGroup

	sched := worker.NewScheduler(db, q)
	wg.Add(1)
	go func() {
		defer wg.Done()
		sched.Run(ctx)
	}()

	wk := worker.NewWorker(s, q)
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := wk.Run(ctx); err != nil {
			slog.Error("worker stopped", "err", err)
			stop()
		}
	}()

	rec := worker.NewReclaimer(q, wk.ID)
	wg.Add(1)
	go func() {
		defer wg.Done()
		rec.Run(ctx)
	}()

	ret := worker.NewRetention(db)
	wg.Add(1)
	go func() {
		defer wg.Done()
		ret.Run(ctx)
	}()

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           newRouter(s, db, rdb, cfg),
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

	srvErr := srv.Shutdown(shutdownCtx)
	wg.Wait()
	return srvErr
}

func newRouter(s *store.Store, db *sql.DB, rdb *redis.Client, cfg *config.Config) http.Handler {
	wh := &api.WebhookHandler{Store: s}
	ev := &api.EventHandler{Store: s}
	dh := &api.DeliveryHandler{Store: s}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz(db, rdb))

	mux.Handle("POST /webhooks",
		api.RequireJWT(cfg.JWTSecret)(http.HandlerFunc(wh.Create)))
	mux.Handle("GET /webhooks",
		api.RequireJWT(cfg.JWTSecret)(http.HandlerFunc(wh.List)))
	mux.Handle("DELETE /webhooks/{id}",
		api.RequireJWT(cfg.JWTSecret)(http.HandlerFunc(wh.Delete)))
	mux.Handle("GET /deliveries",
		api.RequireJWT(cfg.JWTSecret)(http.HandlerFunc(dh.List)))

	mux.Handle("POST /events",
		api.RequireIngestToken(cfg.IngestToken)(http.HandlerFunc(ev.Ingest)))

	// Wrap with CORS middleware
	return corsMiddleware(mux, cfg.AllowedOrigins)
}

// corsMiddleware adds CORS headers and handles preflight requests
func corsMiddleware(next http.Handler, allowedOrigins string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")

		// Parse allowed origins (comma-separated)
		allowed := strings.Split(allowedOrigins, ",")
		allowedMap := make(map[string]bool)
		for _, o := range allowed {
			allowedMap[strings.TrimSpace(o)] = true
		}

		// Check if origin is allowed
		if origin != "" && allowedMap[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}

		// Handle preflight requests
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
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
