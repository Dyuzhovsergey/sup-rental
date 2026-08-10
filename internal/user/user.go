// Package user описывает учётную запись и правила пользовательского login.
package user

import (
	"errors"
	"strings"
	"time"
)

const (
	// MinLoginLength задаёт минимальную длину нормализованного login.
	MinLoginLength = 3
	// MaxLoginLength задаёт максимальную длину нормализованного login.
	MaxLoginLength = 32
)

// Role определяет функциональную роль пользователя приложения.
type Role string

const (
	// RoleAdmin обозначает единственного администратора приложения.
	RoleAdmin Role = "admin"
	// RoleOperator обозначает оператора проката.
	RoleOperator Role = "operator"
)

var (
	// ErrLoginRequired означает, что login не указан после удаления внешних пробелов.
	ErrLoginRequired = errors.New("login is required")
	// ErrLoginTooShort означает, что login короче допустимого ограничения.
	ErrLoginTooShort = errors.New("login is too short")
	// ErrLoginTooLong означает, что login длиннее допустимого ограничения.
	ErrLoginTooLong = errors.New("login is too long")
	// ErrInvalidLogin означает, что login содержит недопустимые символы.
	ErrInvalidLogin = errors.New("login contains invalid characters")
	// ErrInvalidRole означает, что роль пользователя не поддерживается.
	ErrInvalidRole = errors.New("invalid user role")
	// ErrUserNotFound означает, что пользователь не найден.
	ErrUserNotFound = errors.New("user not found")
	// ErrLoginExists означает, что нормализованный login уже занят.
	ErrLoginExists = errors.New("login already exists")
	// ErrAdminExists означает, что единственная учётная запись admin уже существует.
	ErrAdminExists = errors.New("admin already exists")
)

// User содержит безопасные доменные сведения об учётной записи.
// Открытый password и password hash намеренно не являются частью этой структуры.
type User struct {
	// ID однозначно идентифицирует пользователя в постоянном хранилище.
	ID int64
	// Login содержит нормализованное уникальное имя для входа.
	Login string
	// Role определяет доступные пользователю функции приложения.
	Role Role
	// Active сообщает, разрешён ли пользователю вход в приложение.
	Active bool
	// LastLoginAt содержит время последней успешной аутентификации.
	LastLoginAt *time.Time
}

// New создаёт активного пользователя с нормализованным login и допустимой ролью.
func New(login string, role Role) (User, error) {
	normalizedLogin, err := NormalizeLogin(login)
	if err != nil {
		return User{}, err
	}
	if !role.Valid() {
		return User{}, ErrInvalidRole
	}

	return User{
		Login:  normalizedLogin,
		Role:   role,
		Active: true,
	}, nil
}

// NormalizeLogin удаляет внешние пробелы, приводит ASCII-буквы к нижнему
// регистру и проверяет согласованный формат login.
func NormalizeLogin(login string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(login))
	if normalized == "" {
		return "", ErrLoginRequired
	}
	if len(normalized) < MinLoginLength {
		return "", ErrLoginTooShort
	}
	if len(normalized) > MaxLoginLength {
		return "", ErrLoginTooLong
	}
	if !isASCIILetterOrDigit(normalized[0]) {
		return "", ErrInvalidLogin
	}

	for index := 1; index < len(normalized); index++ {
		character := normalized[index]
		if isASCIILetterOrDigit(character) ||
			character == '.' || character == '_' || character == '-' {
			continue
		}

		return "", ErrInvalidLogin
	}

	return normalized, nil
}

// Valid сообщает, поддерживается ли роль моделью доступа приложения.
func (r Role) Valid() bool {
	return r == RoleAdmin || r == RoleOperator
}

func isASCIILetterOrDigit(character byte) bool {
	return character >= 'a' && character <= 'z' ||
		character >= '0' && character <= '9'
}
