// Package httpserver предоставляет HTTP-обработчики приложения.
package httpserver

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
)

//go:embed templates/*.html
var templateFiles embed.FS

// NewHandler создаёт HTTP-обработчик со всеми маршрутами приложения.
//
// Logger используется для записи ошибок HTTP-слоя, а equipmentService
// предоставляет сценарии учёта оборудования. Обе зависимости должны быть
// созданы точкой входа приложения. NewHandler возвращает ошибку, если
// встроенные HTML-шаблоны невозможно разобрать.
func NewHandler(
	logger *slog.Logger,
	equipmentService equipmentService,
) (http.Handler, error) {
	pageTemplates, err := template.ParseFS(templateFiles, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse HTML templates: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		status(logger, pageTemplates, w, r)
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		health(logger, w, r)
	})
	mux.HandleFunc("/equipment", func(w http.ResponseWriter, r *http.Request) {
		equipmentPage(logger, equipmentService, pageTemplates, w, r)
	})
	mux.HandleFunc("POST /equipment/{id}/status", func(w http.ResponseWriter, r *http.Request) {
		changeEquipmentStatus(logger, equipmentService, pageTemplates, w, r)
	})

	return mux, nil
}

type statusPageData struct {
	Title      string
	Status     string
	HealthPath string
}

func status(
	logger *slog.Logger,
	statusTemplate *template.Template,
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	data := statusPageData{
		Title:      "SUP Rental",
		Status:     "Приложение работает",
		HealthPath: "/health",
	}

	var body bytes.Buffer
	if err := statusTemplate.ExecuteTemplate(&body, "status.html", data); err != nil {
		logger.Error(
			"render status page",
			slog.Any("error", err),
		)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	if _, err := w.Write(body.Bytes()); err != nil {
		logger.Error(
			"write status response",
			slog.Any("error", err),
		)
	}
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
