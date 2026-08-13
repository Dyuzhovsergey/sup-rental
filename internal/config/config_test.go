package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name         string
		address      string
		timeout      string
		shutdown     string
		databaseURL  string
		dbTimeout    string
		cookieSecure string
		want         Config
		wantErrText  string
	}{
		{
			name:         "valid configuration",
			address:      "127.0.0.1:8080",
			timeout:      "5s",
			shutdown:     "10s",
			databaseURL:  "postgres://sup_rental:secret@localhost:5432/sup_rental",
			dbTimeout:    "5s",
			cookieSecure: "false",
			want: Config{
				HTTPAddress:           "127.0.0.1:8080",
				HTTPReadHeaderTimeout: 5 * time.Second,
				HTTPShutdownTimeout:   10 * time.Second,
				DatabaseURL:           "postgres://sup_rental:secret@localhost:5432/sup_rental",
				DBConnectTimeout:      5 * time.Second,
				SessionCookieSecure:   false,
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
		{
			name:        "missing shutdown timeout",
			address:     ":8080",
			timeout:     "5s",
			wantErrText: "HTTP_SHUTDOWN_TIMEOUT: environment variable is required",
		},
		{
			name:        "invalid shutdown timeout format",
			address:     ":8080",
			timeout:     "5s",
			shutdown:    "ten seconds",
			wantErrText: "HTTP_SHUTDOWN_TIMEOUT: parse duration",
		},
		{
			name:        "zero shutdown timeout",
			address:     ":8080",
			timeout:     "5s",
			shutdown:    "0s",
			wantErrText: "HTTP_SHUTDOWN_TIMEOUT: must be greater than zero",
		},
		{
			name:        "negative shutdown timeout",
			address:     ":8080",
			timeout:     "5s",
			shutdown:    "-1s",
			wantErrText: "HTTP_SHUTDOWN_TIMEOUT: must be greater than zero",
		},
		{
			name:        "missing database URL",
			address:     ":8080",
			timeout:     "5s",
			shutdown:    "10s",
			wantErrText: "DATABASE_URL: environment variable is required",
		},
		{
			name:        "missing database connect timeout",
			address:     ":8080",
			timeout:     "5s",
			shutdown:    "10s",
			databaseURL: "postgres://localhost/sup_rental",
			wantErrText: "DB_CONNECT_TIMEOUT: environment variable is required",
		},
		{
			name:        "invalid database connect timeout",
			address:     ":8080",
			timeout:     "5s",
			shutdown:    "10s",
			databaseURL: "postgres://localhost/sup_rental",
			dbTimeout:   "five seconds",
			wantErrText: "DB_CONNECT_TIMEOUT: parse duration",
		},
		{
			name:        "non-positive database connect timeout",
			address:     ":8080",
			timeout:     "5s",
			shutdown:    "10s",
			databaseURL: "postgres://localhost/sup_rental",
			dbTimeout:   "0s",
			wantErrText: "DB_CONNECT_TIMEOUT: must be greater than zero",
		},
		{
			name:        "missing session cookie secure",
			address:     "127.0.0.1:8080",
			timeout:     "5s",
			shutdown:    "10s",
			databaseURL: "postgres://localhost/sup_rental",
			dbTimeout:   "5s",
			wantErrText: "SESSION_COOKIE_SECURE: environment variable is required",
		},
		{
			name:         "invalid session cookie secure",
			address:      "127.0.0.1:8080",
			timeout:      "5s",
			shutdown:     "10s",
			databaseURL:  "postgres://localhost/sup_rental",
			dbTimeout:    "5s",
			cookieSecure: "sometimes",
			wantErrText:  "SESSION_COOKIE_SECURE: parse boolean",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(httpAddressEnv, tt.address)
			t.Setenv(httpReadHeaderTimeoutEnv, tt.timeout)
			t.Setenv(httpShutdownTimeoutEnv, tt.shutdown)
			t.Setenv(databaseURLEnv, tt.databaseURL)
			t.Setenv(dbConnectTimeoutEnv, tt.dbTimeout)
			t.Setenv(sessionCookieSecureEnv, tt.cookieSecure)

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

func TestLoadDatabase(t *testing.T) {
	tests := []struct {
		name        string
		databaseURL string
		timeout     string
		want        DatabaseConfig
		wantErrText string
	}{
		{
			name:        "valid database configuration",
			databaseURL: "postgres://sup_rental:secret@localhost:5432/sup_rental",
			timeout:     "5s",
			want: DatabaseConfig{
				DatabaseURL:      "postgres://sup_rental:secret@localhost:5432/sup_rental",
				DBConnectTimeout: 5 * time.Second,
			},
		},
		{name: "missing database URL", timeout: "5s", wantErrText: "DATABASE_URL: environment variable is required"},
		{
			name:        "missing timeout",
			databaseURL: "postgres://localhost/sup_rental",
			wantErrText: "DB_CONNECT_TIMEOUT: environment variable is required",
		},
		{
			name:        "invalid timeout",
			databaseURL: "postgres://localhost/sup_rental",
			timeout:     "five seconds",
			wantErrText: "DB_CONNECT_TIMEOUT: parse duration",
		},
		{
			name:        "non-positive timeout",
			databaseURL: "postgres://localhost/sup_rental",
			timeout:     "0s",
			wantErrText: "DB_CONNECT_TIMEOUT: must be greater than zero",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(databaseURLEnv, tt.databaseURL)
			t.Setenv(dbConnectTimeoutEnv, tt.timeout)

			got, err := LoadDatabase()
			if tt.wantErrText != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrText) {
					t.Fatalf("LoadDatabase() error = %v, want containing %q", err, tt.wantErrText)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadDatabase() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("LoadDatabase() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
