// Command wbb-api is the WBB HTTP API server.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/impwy/wbb-live/wbb-backend/internal/auth"
	"github.com/impwy/wbb-live/wbb-backend/internal/broadcasts"
	"github.com/impwy/wbb-live/wbb-backend/internal/config"
	"github.com/impwy/wbb-live/wbb-backend/internal/httpapi"
	"github.com/impwy/wbb-live/wbb-backend/internal/platform/db"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(log)

	cfg, err := config.Load()
	if err != nil {
		log.Error("config", "error", err)
		os.Exit(1)
	}
	if cfg.EphemeralSecret() {
		log.Warn("WBB_JWT_SECRET not set; generated an ephemeral secret (dev only). Existing access tokens become invalid; refresh sessions can restore access.")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := connectWithRetry(ctx, log, cfg.DatabaseURL)
	if err != nil {
		log.Error("database connect", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool); err != nil {
		log.Error("migrate", "error", err)
		os.Exit(1)
	}
	log.Info("migrations applied", "latest", db.LatestEmbedded())

	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	defer rdb.Close()
	if err := pingWithRetry(ctx, log, rdb); err != nil {
		log.Error("redis connect", "error", err)
		os.Exit(1)
	}

	authSvc := auth.NewService(pool, rdb, auth.NewTokenManager(cfg.JWTSecret, cfg.AccessTokenTTL), cfg.RefreshTokenTTL)
	bcastSvc := broadcasts.NewService(pool)
	srv := httpapi.NewServer(cfg, authSvc, bcastSvc, rdb, pool, log)

	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		log.Info("listening", "addr", cfg.Addr, "env", cfg.Env)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("serve", "error", err)
			stop()
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Error("shutdown", "error", err)
	}
	log.Info("stopped")
}

func connectWithRetry(ctx context.Context, log *slog.Logger, url string) (*pgxpool.Pool, error) {
	var lastErr error
	for attempt := 0; attempt < 30; attempt++ {
		pool, err := db.Connect(ctx, url)
		if err == nil {
			return pool, nil
		}
		lastErr = err
		log.Warn("database not ready; retrying", "attempt", attempt+1, "error", err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}
	return nil, lastErr
}

func pingWithRetry(ctx context.Context, log *slog.Logger, rdb *redis.Client) error {
	var lastErr error
	for attempt := 0; attempt < 30; attempt++ {
		if err := rdb.Ping(ctx).Err(); err == nil {
			return nil
		} else {
			lastErr = err
		}
		log.Warn("redis not ready; retrying", "attempt", attempt+1, "error", lastErr)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}
	return lastErr
}
