package httpserver

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Dyuzhovsergey/sup-rental/internal/equipment"
)

func TestStatus(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	newTestHandler(t, logger).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Errorf("status code = %d, want %d", response.Code, http.StatusOK)
	}

	const wantContentType = "text/html; charset=utf-8"
	if got := response.Header().Get("Content-Type"); got != wantContentType {
		t.Errorf("Content-Type = %q, want %q", got, wantContentType)
	}

	for _, want := range []string{
		"<h1>SUP Rental</h1>",
		"Приложение работает",
		`href="/health"`,
	} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("body = %q, want it to contain %q", response.Body.String(), want)
		}
	}
}

func TestStatusRejectsUnsupportedMethod(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	response := httptest.NewRecorder()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	newTestHandler(t, logger).ServeHTTP(response, request)

	if response.Code != http.StatusMethodNotAllowed {
		t.Errorf("status code = %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}

	if got := response.Header().Get("Allow"); got != http.MethodGet {
		t.Errorf("Allow = %q, want %q", got, http.MethodGet)
	}
}

func TestStatusLogsWriteError(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)

	const writeErrorText = "write response"

	response := newResponseRecorder()
	response.writeErr = errors.New(writeErrorText)

	var logOutput bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logOutput, nil)).With(
		slog.String("component", "httpserver"),
	)

	newTestHandler(t, logger).ServeHTTP(response, request)

	for _, want := range []string{
		`level=ERROR`,
		`msg="write status response"`,
		`component=httpserver`,
		`error="` + writeErrorText + `"`,
	} {
		if !strings.Contains(logOutput.String(), want) {
			t.Errorf("log output = %q, want it to contain %q", logOutput.String(), want)
		}
	}
}

func TestUnknownPathReturnsNotFound(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/unknown", nil)
	response := httptest.NewRecorder()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	newTestHandler(t, logger).ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Errorf("status code = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func TestHealth(t *testing.T) {
	request, err := http.NewRequest(http.MethodGet, "/health", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}

	response := newResponseRecorder()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	newTestHandler(t, logger).ServeHTTP(response, request)

	if response.statusCode != http.StatusOK {
		t.Errorf("status code = %d, want %d", response.statusCode, http.StatusOK)
	}

	const wantContentType = "text/plain; charset=utf-8"
	if got := response.header.Get("Content-Type"); got != wantContentType {
		t.Errorf("Content-Type = %q, want %q", got, wantContentType)
	}

	const wantBody = "ok\n"
	if got := response.body.String(); got != wantBody {
		t.Errorf("body = %q, want %q", got, wantBody)
	}
}

func TestHealthLogsWriteError(t *testing.T) {
	request, err := http.NewRequest(http.MethodGet, "/health", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}

	const writeErrorText = "write response"

	response := newResponseRecorder()
	response.writeErr = errors.New(writeErrorText)

	var logOutput bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logOutput, nil)).With(
		slog.String("component", "httpserver"),
	)

	newTestHandler(t, logger).ServeHTTP(response, request)

	for _, want := range []string{
		`level=ERROR`,
		`msg="write health response"`,
		`component=httpserver`,
		`error="` + writeErrorText + `"`,
	} {
		if !strings.Contains(logOutput.String(), want) {
			t.Errorf("log output = %q, want it to contain %q", logOutput.String(), want)
		}
	}
}

func newTestHandler(
	t *testing.T,
	logger *slog.Logger,
	services ...equipmentService,
) http.Handler {
	t.Helper()

	var service equipmentService = &equipmentServiceStub{}
	if len(services) > 0 {
		service = services[0]
	}

	handler, err := NewHandler(logger, service)
	if err != nil {
		t.Fatalf("create handler: %v", err)
	}

	return handler
}

type responseRecorder struct {
	header     http.Header
	body       bytes.Buffer
	statusCode int
	writeErr   error
}

func newResponseRecorder() *responseRecorder {
	return &responseRecorder{
		header: make(http.Header),
	}
}

func (r *responseRecorder) Header() http.Header {
	return r.header
}

func (r *responseRecorder) Write(body []byte) (int, error) {
	if r.statusCode == 0 {
		r.statusCode = http.StatusOK
	}

	if r.writeErr != nil {
		return 0, r.writeErr
	}

	return r.body.Write(body)
}

func (r *responseRecorder) WriteHeader(statusCode int) {
	r.statusCode = statusCode
}

type equipmentServiceStub struct {
	create       func(context.Context, equipment.CreateInput) (equipment.Item, error)
	list         func(context.Context) ([]equipment.Item, error)
	changeStatus func(context.Context, int64, equipment.Status) (equipment.Item, error)
}

func (s *equipmentServiceStub) Create(
	ctx context.Context,
	input equipment.CreateInput,
) (equipment.Item, error) {
	if s.create == nil {
		return equipment.Item{}, nil
	}

	return s.create(ctx, input)
}

func (s *equipmentServiceStub) List(ctx context.Context) ([]equipment.Item, error) {
	if s.list == nil {
		return []equipment.Item{}, nil
	}

	return s.list(ctx)
}

func (s *equipmentServiceStub) ChangeStatus(
	ctx context.Context,
	id int64,
	status equipment.Status,
) (equipment.Item, error) {
	if s.changeStatus == nil {
		return equipment.Item{}, nil
	}

	return s.changeStatus(ctx, id, status)
}
