package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/efehidir/ddo/backend/internal/config"
	"github.com/efehidir/ddo/backend/internal/database"
	"github.com/efehidir/ddo/backend/internal/server"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	cfg := config.Load()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pgPool, err := database.NewPostgresPool(ctx, cfg.DatabaseURL, cfg.PostgresMaxConns, cfg.PostgresMinConns)
	if err != nil {
		slog.Error("failed to initialize PostgreSQL 16 connection pool", "error", err)
		os.Exit(1)
	}
	defer pgPool.Close()

	slog.Info("connected to PostgreSQL 16 via pgx/v5",
		"host", cfg.PostgresHost,
		"db", cfg.PostgresDB,
		"max_conns", cfg.PostgresMaxConns,
	)

	app := server.New(cfg, pgPool)

	errCh := make(chan error, 1)
	go func() {
		addr := ":" + cfg.Port
		slog.Info("starting Go Fiber HTTP server", "addr", addr, "env", cfg.AppEnv, "version", cfg.AppVersion)
		if listenErr := app.Listen(addr); listenErr != nil {
			errCh <- listenErr
		}
	}()

	select {
	case <-ctx.Done():
		slog.Info("shutdown signal received, gracefully draining HTTP connections...")
	case listenErr := <-errCh:
		slog.Error("HTTP server error", "error", listenErr)
		os.Exit(1)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := app.ShutdownWithContext(shutdownCtx); err != nil {
		slog.Error("graceful shutdown failed", "error", err)
		os.Exit(1)
	}

	slog.Info("server stopped cleanly")
}

