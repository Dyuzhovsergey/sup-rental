package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name        string
		address     string
		timeout     string
		want        Config
		wantErrText string
	}{
		{
			name:    "valid configuration",
			address: "127.0.0.1:8080",
			timeout: "5s",
			want: Config{
				HTTPAddress:           "127.0.0.1:8080",
				HTTPReadHeaderTimeout: 5 * time.Second,
			},
		},
		{
			name:        "missing address",
			timeout:     "5s",
			wantErrText: "HTTP_ADDRESS: environment variable is required",
		},
		{
			name:        "invalid address format",
			address:     "localhost",
			timeout:     "5s",
			wantErrText: "HTTP_ADDRESS: must use host:port format",
		},
		{
			name:        "invalid address port",
			address:     ":70000",
			timeout:     "5s",
			wantErrText: "HTTP_ADDRESS: port must be between 1 and 65535",
		},
		{
			name:        "missing timeout",
			address:     ":8080",
			wantErrText: "HTTP_READ_HEADER_TIMEOUT: environment variable is required",
		},
		{
			name:        "invalid timeout format",
			address:     ":8080",
			timeout:     "five seconds",
			wantErrText: "HTTP_READ_HEADER_TIMEOUT: parse duration",
		},
		{
			name:        "non-positive timeout",
			address:     ":8080",
			timeout:     "0s",
			wantErrText: "HTTP_READ_HEADER_TIMEOUT: must be greater than zero",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(httpAddressEnv, tt.address)
			t.Setenv(httpReadHeaderTimeoutEnv, tt.timeout)

			got, err := Load()
			if tt.wantErrText != "" {
				if err == nil {
					t.Fatalf("Load() error = nil, want error containing %q", tt.wantErrText)
				}
				if !strings.Contains(err.Error(), tt.wantErrText) {
					t.Fatalf("Load() error = %q, want error containing %q", err, tt.wantErrText)
				}
				return
			}

			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}

			if got != tt.want {
				t.Errorf("Load() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
