package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/Dyuzhovsergey/sup-rental/internal/config"
	"github.com/Dyuzhovsergey/sup-rental/internal/httpserver"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	if err := run(logger); err != nil {
		logger.Error("server stopped", slog.Any("error", err))
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	httpLogger := logger.With(
		slog.String("component", "httpserver"),
	)

	server := &http.Server{
		Addr:              cfg.HTTPAddress,
		Handler:           httpserver.NewHandler(httpLogger),
		ReadHeaderTimeout: cfg.HTTPReadHeaderTimeout,
	}

	logger.Info(
		"starting HTTP server",
		slog.String("address", cfg.HTTPAddress),
	)

	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("listen and serve: %w", err)
	}

	return nil
}
