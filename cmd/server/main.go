package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/Dyuzhovsergey/sup-rental/internal/config"
	"github.com/Dyuzhovsergey/sup-rental/internal/httpserver"
	"github.com/Dyuzhovsergey/sup-rental/internal/postgres"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	if err := run(ctx, logger); err != nil {
		logger.Error("server stopped", slog.Any("error", err))
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	dbCtx, cancelDB := context.WithTimeout(ctx, cfg.DBConnectTimeout)
	pool, err := postgres.Open(dbCtx, cfg.DatabaseURL)
	cancelDB()
	if err != nil {
		return fmt.Errorf("open PostgreSQL: %w", err)
	}
	defer pool.Close()

	logger.Info("connected to PostgreSQL")

	httpLogger := logger.With(
		slog.String("component", "httpserver"),
	)

	handler, err := httpserver.NewHandler(httpLogger)
	if err != nil {
		return fmt.Errorf("create HTTP handler: %w", err)
	}

	server := &http.Server{
		Addr:              cfg.HTTPAddress,
		Handler:           handler,
		ReadHeaderTimeout: cfg.HTTPReadHeaderTimeout,
	}

	logger.Info(
		"starting HTTP server",
		slog.String("address", cfg.HTTPAddress),
	)

	serverErr := make(chan error, 1)

	go func() {
		serverErr <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("listen and serve: %w", err)
		}

		return nil

	case <-ctx.Done():
		logger.Info(
			"shutting down HTTP server",
			slog.Duration("timeout", cfg.HTTPShutdownTimeout),
		)
	}

	// Signal context уже отменён, поэтому Shutdown получает новый context,
	// ограниченный отдельным timeout.
	shutdownCtx, cancel := context.WithTimeout(
		context.Background(),
		cfg.HTTPShutdownTimeout,
	)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown HTTP server: %w", err)
	}

	if err := <-serverErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("listen and serve after shutdown: %w", err)
	}

	logger.Info("HTTP server stopped")

	return nil
}
