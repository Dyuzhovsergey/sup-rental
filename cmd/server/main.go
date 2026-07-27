package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/Dyuzhovsergey/sup-rental/internal/httpserver"
)

const serverAddress = ":8080"

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	server := &http.Server{
		Addr:              serverAddress,
		Handler:           httpserver.NewHandler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	slog.Info("starting HTTP server", "address", serverAddress)

	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("listen and serve: %w", err)
	}

	return nil
}
