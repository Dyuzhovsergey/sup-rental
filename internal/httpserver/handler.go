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

//go:embed static/app.css
var appStyles []byte

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
	mux.HandleFunc("/static/app.css", func(w http.ResponseWriter, r *http.Request) {
		stylesheet(logger, w, r)
	})
	mux.HandleFunc("/equipment", func(w http.ResponseWriter, r *http.Request) {
		equipmentPage(logger, equipmentService, pageTemplates, w, r)
	})
	mux.HandleFunc("/equipment/{id}", func(w http.ResponseWriter, r *http.Request) {
		showEquipmentDetailPage(logger, equipmentService, pageTemplates, w, r)
	})
	mux.HandleFunc("/equipment/{id}/retire", func(w http.ResponseWriter, r *http.Request) {
		equipmentRetirement(logger, equipmentService, pageTemplates, w, r)
	})
	mux.HandleFunc("/equipment/{id}/delete", func(w http.ResponseWriter, r *http.Request) {
		equipmentDeletion(logger, equipmentService, pageTemplates, w, r)
	})
	mux.HandleFunc("GET /equipment/{id}/edit", func(w http.ResponseWriter, r *http.Request) {
		showEquipmentEditPage(logger, equipmentService, pageTemplates, w, r)
	})
	mux.HandleFunc("POST /equipment/{id}/edit", func(w http.ResponseWriter, r *http.Request) {
		updateEquipment(logger, equipmentService, pageTemplates, w, r)
	})

	return mux, nil
}

func stylesheet(logger *slog.Logger, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.WriteHeader(http.StatusOK)

	if _, err := w.Write(appStyles); err != nil {
		logger.Error(
			"write application stylesheet",
			slog.Any("error", err),
		)
	}
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
