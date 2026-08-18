// Package config загружает и проверяет конфигурацию приложения.
package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	httpAddressEnv           = "HTTP_ADDRESS"
	httpReadHeaderTimeoutEnv = "HTTP_READ_HEADER_TIMEOUT"
	httpShutdownTimeoutEnv   = "HTTP_SHUTDOWN_TIMEOUT"
	databaseURLEnv           = "DATABASE_URL"
	dbConnectTimeoutEnv      = "DB_CONNECT_TIMEOUT"
	sessionCookieSecureEnv   = "SESSION_COOKIE_SECURE"
	trustProxyHeadersEnv     = "TRUST_PROXY_HEADERS"
)

// Config содержит проверенные параметры запуска приложения.
type Config struct {
	// HTTPAddress задаёт адрес, на котором HTTP-сервер принимает подключения.
	HTTPAddress string
	// HTTPReadHeaderTimeout ограничивает время чтения заголовков HTTP-запроса.
	HTTPReadHeaderTimeout time.Duration
	// HTTPShutdownTimeout ограничивает время корректного завершения HTTP-сервера.
	HTTPShutdownTimeout time.Duration
	// DatabaseURL содержит connection string для подключения к PostgreSQL.
	DatabaseURL string
	// DBConnectTimeout ограничивает время подключения к PostgreSQL.
	DBConnectTimeout time.Duration
	// SessionCookieSecure включает передачу session cookie только через HTTPS.
	SessionCookieSecure bool
	// TrustProxyHeaders разрешает HTTP-слою доверять proxy-заголовкам от
	// заранее доверенного reverse proxy.
	TrustProxyHeaders bool
}

// DatabaseConfig содержит проверенные параметры подключения к PostgreSQL.
type DatabaseConfig struct {
	// DatabaseURL содержит connection string для подключения к PostgreSQL.
	DatabaseURL string
	// DBConnectTimeout ограничивает время подключения к PostgreSQL.
	DBConnectTimeout time.Duration
}

// Load загружает конфигурацию из переменных окружения.
//
// Load возвращает ошибку, если обязательная переменная отсутствует или содержит
// некорректное значение.
func Load() (Config, error) {
	address, err := requiredEnv(httpAddressEnv)
	if err != nil {
		return Config{}, err
	}

	if err := validateHTTPAddress(address); err != nil {
		return Config{}, fmt.Errorf("%s: %w", httpAddressEnv, err)
	}

	readHeaderTimeout, err := positiveDurationEnv(httpReadHeaderTimeoutEnv)
	if err != nil {
		return Config{}, err
	}

	shutdownTimeout, err := positiveDurationEnv(httpShutdownTimeoutEnv)
	if err != nil {
		return Config{}, err
	}

	databaseConfig, err := LoadDatabase()
	if err != nil {
		return Config{}, err
	}

	sessionCookieSecure, err := booleanEnv(sessionCookieSecureEnv)
	if err != nil {
		return Config{}, err
	}

	trustProxyHeaders, err := booleanEnv(trustProxyHeadersEnv)
	if err != nil {
		return Config{}, err
	}

	return Config{
		HTTPAddress:           address,
		HTTPReadHeaderTimeout: readHeaderTimeout,
		HTTPShutdownTimeout:   shutdownTimeout,
		DatabaseURL:           databaseConfig.DatabaseURL,
		DBConnectTimeout:      databaseConfig.DBConnectTimeout,
		SessionCookieSecure:   sessionCookieSecure,
		TrustProxyHeaders:     trustProxyHeaders,
	}, nil
}

func booleanEnv(name string) (bool, error) {
	value, err := requiredEnv(name)
	if err != nil {
		return false, err
	}

	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s: parse boolean: %w", name, err)
	}

	return parsed, nil
}

// LoadDatabase загружает только параметры PostgreSQL, необходимые server и
// административному CLI.
func LoadDatabase() (DatabaseConfig, error) {
	databaseURL, err := requiredEnv(databaseURLEnv)
	if err != nil {
		return DatabaseConfig{}, err
	}

	dbConnectTimeout, err := positiveDurationEnv(dbConnectTimeoutEnv)
	if err != nil {
		return DatabaseConfig{}, err
	}

	return DatabaseConfig{
		DatabaseURL:      databaseURL,
		DBConnectTimeout: dbConnectTimeout,
	}, nil
}

func requiredEnv(name string) (string, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return "", fmt.Errorf("%s: environment variable is required", name)
	}

	return value, nil
}

func positiveDurationEnv(name string) (time.Duration, error) {
	value, err := requiredEnv(name)
	if err != nil {
		return 0, err
	}

	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s: parse duration: %w", name, err)
	}

	if duration <= 0 {
		return 0, fmt.Errorf("%s: must be greater than zero", name)
	}

	return duration, nil
}

func validateHTTPAddress(address string) error {
	_, portValue, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("must use host:port format: %w", err)
	}

	port, err := strconv.Atoi(portValue)
	if err != nil {
		return fmt.Errorf("port must be a number: %w", err)
	}

	if port < 1 || port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}

	return nil
}
