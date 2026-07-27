package httpserver

import (
	"bytes"
	"net/http"
	"testing"
)

func TestHealth(t *testing.T) {
	request, err := http.NewRequest(http.MethodGet, "/health", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}

	response := newResponseRecorder()

	NewHandler().ServeHTTP(response, request)

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

type responseRecorder struct {
	header     http.Header
	body       bytes.Buffer
	statusCode int
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

	return r.body.Write(body)
}

func (r *responseRecorder) WriteHeader(statusCode int) {
	r.statusCode = statusCode
}
