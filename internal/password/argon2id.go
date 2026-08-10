// Package password проверяет password и создаёт безопасные Argon2id hashes.
package password

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const (
	// MinLength задаёт минимальную длину password в Unicode-символах.
	MinLength = 6
	// MaxLength задаёт максимальную длину password в Unicode-символах.
	MaxLength = 128

	argon2Memory      uint32 = 19 * 1024
	argon2Iterations  uint32 = 2
	argon2Parallelism uint8  = 1
	saltLength               = 16
	keyLength         uint32 = 32

	maxEncodedHashLength = 1024
	maxMemory            = 256 * 1024
	maxIterations        = 10
	maxParallelism       = 16
	maxSaltLength        = 64
	maxKeyLength         = 64
)

var (
	// ErrTooShort означает, что password короче согласованного ограничения.
	ErrTooShort = errors.New("password is too short")
	// ErrTooLong означает, что password длиннее согласованного ограничения.
	ErrTooLong = errors.New("password is too long")
	// ErrInvalidUTF8 означает, что password не является корректной UTF-8 строкой.
	ErrInvalidUTF8 = errors.New("password is not valid UTF-8")
	// ErrInvalidHash означает, что encoded hash повреждён или имеет неподдерживаемый формат.
	ErrInvalidHash = errors.New("invalid password hash")
	// ErrUnsupportedHashVersion означает, что версия Argon2id пока не поддерживается.
	ErrUnsupportedHashVersion = errors.New("unsupported password hash version")
	// ErrUnsafeHashParameters означает, что параметры encoded hash выходят за безопасные границы.
	ErrUnsafeHashParameters = errors.New("unsafe password hash parameters")
	// ErrRandomSourceUnavailable означает, что hasher создан без источника случайности.
	ErrRandomSourceUnavailable = errors.New("password random source is unavailable")
)

type parameters struct {
	memory      uint32
	iterations  uint32
	parallelism uint8
}

// Hasher создаёт и проверяет версионированные Argon2id hashes.
// Для Hash экземпляр необходимо создавать через NewHasher; Verify не требует
// источника случайности и может вызываться у zero value.
type Hasher struct {
	random io.Reader
}

// NewHasher создаёт hasher с криптографически стойким источником случайности.
func NewHasher() *Hasher {
	return &Hasher{random: rand.Reader}
}

// Hash проверяет password, создаёт уникальную соль и возвращает encoded
// Argon2id hash. Открытый password в результат не включается.
func (h *Hasher) Hash(password string) (string, error) {
	if err := Validate(password); err != nil {
		return "", err
	}
	if h == nil || h.random == nil {
		return "", ErrRandomSourceUnavailable
	}

	salt := make([]byte, saltLength)
	if _, err := io.ReadFull(h.random, salt); err != nil {
		return "", fmt.Errorf("read password salt: %w", err)
	}

	derivedKey := argon2.IDKey(
		[]byte(password),
		salt,
		argon2Iterations,
		argon2Memory,
		argon2Parallelism,
		keyLength,
	)

	return encodeHash(
		parameters{
			memory:      argon2Memory,
			iterations:  argon2Iterations,
			parallelism: argon2Parallelism,
		},
		salt,
		derivedKey,
	), nil
}

// Verify проверяет password по encoded Argon2id hash. Повреждённый hash и
// небезопасные параметры возвращаются как ошибка, а несовпадение — как false.
func (h *Hasher) Verify(password, encodedHash string) (bool, error) {
	if err := Validate(password); err != nil {
		return false, err
	}

	params, salt, expectedKey, err := decodeHash(encodedHash)
	if err != nil {
		return false, err
	}

	actualKey := argon2.IDKey(
		[]byte(password),
		salt,
		params.iterations,
		params.memory,
		params.parallelism,
		uint32(len(expectedKey)),
	)

	return subtle.ConstantTimeCompare(actualKey, expectedKey) == 1, nil
}

// Validate проверяет согласованную длину password в Unicode-символах.
// Значение не обрезается и не нормализуется; пробелы и Unicode разрешены.
func Validate(password string) error {
	if !utf8.ValidString(password) {
		return ErrInvalidUTF8
	}

	length := utf8.RuneCountInString(password)
	if length < MinLength {
		return ErrTooShort
	}
	if length > MaxLength {
		return ErrTooLong
	}

	return nil
}

func encodeHash(params parameters, salt, derivedKey []byte) string {
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		params.memory,
		params.iterations,
		params.parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(derivedKey),
	)
}

func decodeHash(encodedHash string) (parameters, []byte, []byte, error) {
	if len(encodedHash) == 0 || len(encodedHash) > maxEncodedHashLength {
		return parameters{}, nil, nil, ErrInvalidHash
	}

	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return parameters{}, nil, nil, ErrInvalidHash
	}

	version, err := parseUint(strings.TrimPrefix(parts[2], "v="), 32)
	if err != nil || parts[2] != "v="+strconv.FormatUint(version, 10) {
		return parameters{}, nil, nil, ErrInvalidHash
	}
	if int(version) != argon2.Version {
		return parameters{}, nil, nil, ErrUnsupportedHashVersion
	}

	params, err := decodeParameters(parts[3])
	if err != nil {
		return parameters{}, nil, nil, err
	}

	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil {
		return parameters{}, nil, nil, ErrInvalidHash
	}
	derivedKey, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil {
		return parameters{}, nil, nil, ErrInvalidHash
	}
	if len(salt) < saltLength || len(salt) > maxSaltLength ||
		len(derivedKey) < int(keyLength) || len(derivedKey) > maxKeyLength {
		return parameters{}, nil, nil, ErrUnsafeHashParameters
	}

	return params, salt, derivedKey, nil
}

func decodeParameters(encoded string) (parameters, error) {
	parts := strings.Split(encoded, ",")
	if len(parts) != 3 {
		return parameters{}, ErrInvalidHash
	}

	memory, err := parseNamedUint(parts[0], "m=", 32)
	if err != nil {
		return parameters{}, ErrInvalidHash
	}
	iterations, err := parseNamedUint(parts[1], "t=", 32)
	if err != nil {
		return parameters{}, ErrInvalidHash
	}
	parallelism, err := parseNamedUint(parts[2], "p=", 8)
	if err != nil {
		return parameters{}, ErrInvalidHash
	}

	if memory < 7*1024 || memory > maxMemory ||
		iterations == 0 || iterations > maxIterations ||
		parallelism == 0 || parallelism > maxParallelism {
		return parameters{}, ErrUnsafeHashParameters
	}

	return parameters{
		memory:      uint32(memory),
		iterations:  uint32(iterations),
		parallelism: uint8(parallelism),
	}, nil
}

func parseNamedUint(value, prefix string, bitSize int) (uint64, error) {
	if !strings.HasPrefix(value, prefix) {
		return 0, ErrInvalidHash
	}

	parsed, err := parseUint(strings.TrimPrefix(value, prefix), bitSize)
	if err != nil || value != prefix+strconv.FormatUint(parsed, 10) {
		return 0, ErrInvalidHash
	}

	return parsed, nil
}

func parseUint(value string, bitSize int) (uint64, error) {
	if value == "" {
		return 0, ErrInvalidHash
	}

	parsed, err := strconv.ParseUint(value, 10, bitSize)
	if err != nil {
		return 0, ErrInvalidHash
	}

	return parsed, nil
}
