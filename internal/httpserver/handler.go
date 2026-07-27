// Package httpserver предоставляет HTTP-обработчики приложения.
package httpserver

import (
	"log/slog"
	"net/http"
)

// NewHandler создаёт HTTP-обработчик со всеми маршрутами приложения.
//
// Logger используется для записи ошибок HTTP-слоя и должен быть создан
// точкой входа приложения.
func NewHandler(logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		health(logger, w, r)
	})

	return mux
}

func health(logger *slog.Logger, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	if _, err := w.Write([]byte("ok\n")); err != nil {
		logger.Error(
			"write health response",
			slog.Any("error", err),
		)
	}
}
