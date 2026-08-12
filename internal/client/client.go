// Package client содержит доменную модель и правила проверки данных клиента.
package client

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxFullNameLength = 200

var (
	// ErrFullNameRequired означает, что ФИО клиента не указано.
	ErrFullNameRequired = errors.New("client full name is required")
	// ErrFullNameTooLong означает, что ФИО превышает допустимую длину.
	ErrFullNameTooLong = errors.New("client full name is too long")
	// ErrInvalidFullName означает, что ФИО содержит недопустимые данные.
	ErrInvalidFullName = errors.New("invalid client full name")
	// ErrPhoneRequired означает, что номер телефона клиента не указан.
	ErrPhoneRequired = errors.New("client phone is required")
	// ErrInvalidPhone означает, что номер нельзя привести к поддерживаемому
	// каноническому формату.
	ErrInvalidPhone = errors.New("invalid client phone")
	// ErrClientNotFound означает, что клиент не найден в постоянном хранилище.
	ErrClientNotFound = errors.New("client not found")
	// ErrPhoneExists означает, что нормализованный телефон уже принадлежит
	// другому клиенту.
	ErrPhoneExists = errors.New("client phone already exists")
)

// Client представляет клиента проката с нормализованными контактными данными.
// Нулевой ID означает, что клиент ещё не сохранён в постоянном хранилище.
type Client struct {
	// ID — внутренний идентификатор клиента.
	ID int64
	// FullName — ФИО с нормализованными пробелами.
	FullName string
	// Phone — телефон в каноническом международном формате.
	Phone Phone
}

// New проверяет ФИО и телефон и создаёт ещё не сохранённого клиента.
func New(fullName, rawPhone string) (Client, error) {
	normalizedName, err := normalizeFullName(fullName)
	if err != nil {
		return Client{}, err
	}

	phone, err := NormalizePhone(rawPhone)
	if err != nil {
		return Client{}, err
	}

	return Client{FullName: normalizedName, Phone: phone}, nil
}

func normalizeFullName(value string) (string, error) {
	if !utf8.ValidString(value) {
		return "", ErrInvalidFullName
	}
	for _, symbol := range value {
		if unicode.IsControl(symbol) {
			return "", ErrInvalidFullName
		}
	}

	normalized := strings.Join(strings.Fields(value), " ")
	if normalized == "" {
		return "", ErrFullNameRequired
	}
	if utf8.RuneCountInString(normalized) > maxFullNameLength {
		return "", ErrFullNameTooLong
	}
	return normalized, nil
}
