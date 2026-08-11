// Package session создаёт и проверяет server-side сессии пользователей.
package session

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/Dyuzhovsergey/sup-rental/internal/user"
)

const (
	// IdleTimeout задаёт максимальный период бездействия действующей сессии.
	IdleTimeout = 8 * time.Hour
	// AbsoluteLifetime задаёт максимальный срок сессии независимо от активности.
	AbsoluteLifetime = 24 * time.Hour
	secretSize       = 32
)

var (
	// ErrInvalidToken означает, что session token отсутствует или повреждён.
	ErrInvalidToken = errors.New("invalid session token")
	// ErrSessionNotFound означает, что сессия отсутствует, истекла, отозвана
	// или принадлежит отключённому пользователю.
	ErrSessionNotFound = errors.New("session not found")
	// ErrUserUnavailable означает, что для пользователя нельзя создать сессию.
	ErrUserUnavailable = errors.New("session user is unavailable")
)

// Session содержит серверные сведения о пользовательской сессии.
type Session struct {
	// ID однозначно идентифицирует сессию в PostgreSQL.
	ID int64
	// UserID идентифицирует владельца сессии.
	UserID int64
	// CSRFToken содержит отдельный session-bound token для будущей защиты форм.
	CSRFToken string
	// CreatedAt содержит время создания сессии.
	CreatedAt time.Time
	// LastSeenAt содержит время последней успешной проверки сессии.
	LastSeenAt time.Time
	// AbsoluteExpiresAt содержит предельное время действия сессии.
	AbsoluteExpiresAt time.Time
}

// AuthenticatedSession объединяет действующую сессию и актуальные сведения о
// её активном пользователе.
type AuthenticatedSession struct {
	// Session содержит проверенное серверное состояние сессии.
	Session Session
	// User содержит актуальные сведения об активном владельце сессии.
	User user.User
}

// CreateParams содержит подготовленные значения для сохранения новой сессии.
// TokenDigest является SHA-256 digest и не позволяет получить raw token.
type CreateParams struct {
	// UserID идентифицирует активного владельца новой сессии.
	UserID int64
	// TokenDigest содержит SHA-256 digest raw session token.
	TokenDigest []byte
	// CSRFToken содержит отдельный случайный token для изменяющих HTML-форм.
	CSRFToken string
	// CreatedAt содержит единое время создания и начальной активности.
	CreatedAt time.Time
	// AbsoluteExpiresAt ограничивает полный срок жизни сессии.
	AbsoluteExpiresAt time.Time
}

// Repository хранит и разрешает пользовательские сессии.
type Repository interface {
	// Create сохраняет подготовленную сессию активного пользователя.
	Create(ctx context.Context, params CreateParams) (Session, error)
	// Resolve проверяет digest и сроки, обновляя время успешной активности.
	Resolve(
		ctx context.Context,
		tokenDigest []byte,
		now time.Time,
		idleTimeout time.Duration,
	) (AuthenticatedSession, error)
	// Revoke отзывает одну сессию по digest её token.
	Revoke(ctx context.Context, tokenDigest []byte, revokedAt time.Time) error
	// RevokeAll отзывает все действующие сессии пользователя.
	RevokeAll(ctx context.Context, userID int64, revokedAt time.Time) error
}

// Service применяет политику времени и скрывает raw session token от хранилища.
type Service struct {
	repository Repository
	random     io.Reader
	now        func() time.Time
}

// NewService создаёт сервис сессий с криптографическим источником случайности.
func NewService(repository Repository) *Service {
	return &Service{
		repository: repository,
		random:     rand.Reader,
		now:        time.Now,
	}
}

// Create создаёт новую сессию и единственный раз возвращает raw session token.
// В repository передаётся только SHA-256 digest token.
func (s *Service) Create(ctx context.Context, userID int64) (Session, string, error) {
	token, err := s.randomToken()
	if err != nil {
		return Session{}, "", fmt.Errorf("generate session token: %w", err)
	}
	csrfToken, err := s.randomToken()
	if err != nil {
		return Session{}, "", fmt.Errorf("generate CSRF token: %w", err)
	}

	now := s.now().UTC()
	created, err := s.repository.Create(ctx, CreateParams{
		UserID:            userID,
		TokenDigest:       digest(token),
		CSRFToken:         csrfToken,
		CreatedAt:         now,
		AbsoluteExpiresAt: now.Add(AbsoluteLifetime),
	})
	if err != nil {
		return Session{}, "", fmt.Errorf("create session: %w", err)
	}

	return created, token, nil
}

// Resolve проверяет token и возвращает сессию вместе с актуальным активным
// пользователем. Успешная проверка обновляет время последней активности.
func (s *Service) Resolve(ctx context.Context, token string) (AuthenticatedSession, error) {
	if err := validateToken(token); err != nil {
		return AuthenticatedSession{}, err
	}

	resolved, err := s.repository.Resolve(ctx, digest(token), s.now().UTC(), IdleTimeout)
	if err != nil {
		return AuthenticatedSession{}, fmt.Errorf("resolve session: %w", err)
	}

	return resolved, nil
}

// Revoke отзывает сессию, соответствующую raw token.
func (s *Service) Revoke(ctx context.Context, token string) error {
	if err := validateToken(token); err != nil {
		return err
	}
	if err := s.repository.Revoke(ctx, digest(token), s.now().UTC()); err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}

	return nil
}

// RevokeAll отзывает все действующие сессии указанного пользователя.
func (s *Service) RevokeAll(ctx context.Context, userID int64) error {
	if err := s.repository.RevokeAll(ctx, userID, s.now().UTC()); err != nil {
		return fmt.Errorf("revoke all user sessions: %w", err)
	}

	return nil
}

func (s *Service) randomToken() (string, error) {
	secret := make([]byte, secretSize)
	if _, err := io.ReadFull(s.random, secret); err != nil {
		return "", err
	}

	return base64.RawURLEncoding.EncodeToString(secret), nil
}

func validateToken(token string) error {
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != secretSize {
		return ErrInvalidToken
	}

	return nil
}

func digest(token string) []byte {
	value := sha256.Sum256([]byte(token))
	return value[:]
}
