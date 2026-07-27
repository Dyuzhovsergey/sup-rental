package httpserver

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
)

func TestHealth(t *testing.T) {
	request, err := http.NewRequest(http.MethodGet, "/health", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}

	response := newResponseRecorder()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	NewHandler(logger).ServeHTTP(response, request)

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

	NewHandler(logger).ServeHTTP(response, request)

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
