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
)

// Config содержит проверенные параметры запуска приложения.
type Config struct {
	// HTTPAddress задаёт адрес, на котором HTTP-сервер принимает подключения.
	HTTPAddress string
	// HTTPReadHeaderTimeout ограничивает время чтения заголовков HTTP-запроса.
	HTTPReadHeaderTimeout time.Duration
	// HTTPShutdownTimeout ограничивает время корректного завершения HTTP-сервера.
	HTTPShutdownTimeout time.Duration
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

	return Config{
		HTTPAddress:           address,
		HTTPReadHeaderTimeout: readHeaderTimeout,
		HTTPShutdownTimeout:   shutdownTimeout,
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
