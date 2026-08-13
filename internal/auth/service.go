// Package auth реализует вход, ограничение неуспешных попыток и logout.
package auth

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Dyuzhovsergey/sup-rental/internal/password"
	"github.com/Dyuzhovsergey/sup-rental/internal/session"
	"github.com/Dyuzhovsergey/sup-rental/internal/user"
)

const (
	// AttemptWindow задаёт окно подсчёта неуспешных попыток login.
	AttemptWindow = 15 * time.Minute
	// BlockDuration задаёт длительность блокировки после достижения лимита.
	BlockDuration = 15 * time.Minute
	// LoginFailureLimit задаёт допустимое количество ошибок для одного login.
	LoginFailureLimit = 5
	// IPFailureLimit задаёт допустимое количество ошибок для одного IP.
	IPFailureLimit = 20

	dummyPassword = "dummy-password-not-used"
)

var (
	// ErrInvalidCredentials скрывает различие между неизвестным login,
	// неправильным password и отключённой учётной записью.
	ErrInvalidCredentials = errors.New("invalid login or password")
	// ErrLoginThrottled означает временную блокировку попыток входа.
	ErrLoginThrottled = errors.New("login attempts are throttled")
	// ErrLoginStateChanged означает, что пользователь изменился между проверкой
	// password и транзакционным завершением login.
	ErrLoginStateChanged = errors.New("login state changed")
)

// ThrottledError сообщает время, после которого login можно повторить.
type ThrottledError struct {
	// Until содержит окончание текущей блокировки.
	Until time.Time
}

// Error возвращает безопасное техническое описание блокировки.
func (e *ThrottledError) Error() string {
	return fmt.Sprintf("login attempts are throttled until %s", e.Until.Format(time.RFC3339))
}

// Unwrap позволяет проверять ошибку через errors.Is с ErrLoginThrottled.
func (e *ThrottledError) Unwrap() error {
	return ErrLoginThrottled
}

// LoginInput содержит данные одной попытки аутентификации.
type LoginInput struct {
	// Login содержит введённое пользователем имя учётной записи.
	Login string
	// Password содержит write-only значение для проверки Argon2id hash.
	Password string
	// RemoteIP содержит IP из RemoteAddr без доверия proxy-заголовкам.
	RemoteIP string
}

// LoginResult содержит созданную сессию и raw token для session cookie.
type LoginResult struct {
	// User содержит актуального аутентифицированного пользователя.
	User user.User
	// Session содержит сохранённое server-side состояние сессии.
	Session session.Session
	// Token содержит raw token и предназначен только для session cookie.
	Token string
}

// Attempt содержит безопасные ключи throttling и audit-метаданные попытки.
type Attempt struct {
	// LoginKey содержит SHA-256 digest канонического введённого login.
	LoginKey []byte
	// IPKey содержит SHA-256 digest IP-адреса.
	IPKey []byte
	// LoginLabel содержит нормализованный login либо безопасную метку.
	LoginLabel string
	// RemoteIP содержит IP для security audit.
	RemoteIP string
	// OccurredAt содержит единое время операции.
	OccurredAt time.Time
}

// CompleteLoginParams содержит данные для атомарного завершения login.
type CompleteLoginParams struct {
	// Account содержит проверенного пользователя.
	Account user.User
	// ExpectedPasswordHash защищает от конкурентной замены password.
	ExpectedPasswordHash string
	// Session содержит подготовленные безопасные параметры новой сессии.
	Session session.CreateParams
	// Attempt содержит throttling-ключи и audit-метаданные.
	Attempt Attempt
}

// Repository объединяет операции, которым необходимы PostgreSQL-транзакции.
type Repository interface {
	// FindByLogin возвращает пользователя и password hash для проверки.
	FindByLogin(ctx context.Context, normalizedLogin string) (user.User, string, error)
	// BlockedUntil возвращает окончание действующей блокировки либо zero time.
	BlockedUntil(ctx context.Context, attempt Attempt) (time.Time, error)
	// RecordFailure атомарно обновляет оба лимита и записывает audit event.
	RecordFailure(ctx context.Context, attempt Attempt) error
	// RecordThrottled записывает отказ уже заблокированной попытки.
	RecordThrottled(ctx context.Context, attempt Attempt) error
	// CompleteLogin атомарно создаёт сессию, обновляет пользователя и audit.
	CompleteLogin(ctx context.Context, params CompleteLoginParams) (session.Session, error)
	// Logout атомарно отзывает сессию и записывает audit event.
	Logout(ctx context.Context, authenticated session.AuthenticatedSession, occurredAt time.Time) error
}

// PasswordHasher создаёт dummy hash и проверяет Argon2id password hashes.
type PasswordHasher interface {
	Hash(plainPassword string) (string, error)
	Verify(plainPassword, encodedHash string) (bool, error)
}

type sessionPreparer interface {
	Prepare(userID int64) (session.Prepared, error)
}

// Service реализует аутентификацию без зависимости от HTTP и PostgreSQL.
type Service struct {
	repository Repository
	hasher     PasswordHasher
	sessions   sessionPreparer
	dummyHash  string
	now        func() time.Time
}

// NewService создаёт auth service и заранее подготавливает dummy Argon2id hash,
// используемый для выравнивания обработки неизвестного login.
func NewService(
	repository Repository,
	hasher PasswordHasher,
	sessions sessionPreparer,
) (*Service, error) {
	dummyHash, err := hasher.Hash(dummyPassword)
	if err != nil {
		return nil, fmt.Errorf("create dummy password hash: %w", err)
	}

	return &Service{
		repository: repository,
		hasher:     hasher,
		sessions:   sessions,
		dummyHash:  dummyHash,
		now:        time.Now,
	}, nil
}

// Login проверяет throttling и credentials, затем атомарно создаёт сессию.
func (s *Service) Login(ctx context.Context, input LoginInput) (LoginResult, error) {
	now := s.now().UTC()
	normalizedLogin, normalizeErr := user.NormalizeLogin(input.Login)
	attempt := newAttempt(input.Login, normalizedLogin, normalizeErr, input.RemoteIP, now)

	blockedUntil, err := s.repository.BlockedUntil(ctx, attempt)
	if err != nil {
		return LoginResult{}, fmt.Errorf("check login throttling: %w", err)
	}
	if blockedUntil.After(now) {
		if err := s.repository.RecordThrottled(ctx, attempt); err != nil {
			return LoginResult{}, fmt.Errorf("audit throttled login: %w", err)
		}
		return LoginResult{}, &ThrottledError{Until: blockedUntil}
	}

	account, encodedHash, found, err := s.findAccount(ctx, normalizedLogin, normalizeErr)
	if err != nil {
		return LoginResult{}, err
	}

	passwordToVerify := input.Password
	validPasswordInput := password.Validate(input.Password) == nil
	if !validPasswordInput {
		passwordToVerify = dummyPassword
	}

	hashToVerify := encodedHash
	if !found {
		hashToVerify = s.dummyHash
	}
	matched, err := s.hasher.Verify(passwordToVerify, hashToVerify)
	if err != nil {
		return LoginResult{}, fmt.Errorf("verify password: %w", err)
	}

	if !found || !account.Active || !validPasswordInput || !matched {
		return LoginResult{}, s.failLogin(ctx, attempt)
	}

	prepared, err := s.sessions.Prepare(account.ID)
	if err != nil {
		return LoginResult{}, fmt.Errorf("prepare session: %w", err)
	}

	created, err := s.repository.CompleteLogin(ctx, CompleteLoginParams{
		Account:              account,
		ExpectedPasswordHash: encodedHash,
		Session:              prepared.Params,
		Attempt:              attempt,
	})
	if errors.Is(err, ErrLoginStateChanged) {
		return LoginResult{}, s.failLogin(ctx, attempt)
	}
	if errors.Is(err, ErrLoginThrottled) {
		return LoginResult{}, err
	}
	if err != nil {
		return LoginResult{}, fmt.Errorf("complete login: %w", err)
	}

	account.LastLoginAt = &created.LastSeenAt
	return LoginResult{User: account, Session: created, Token: prepared.Token}, nil
}

// Logout атомарно отзывает текущую сессию и записывает audit event.
func (s *Service) Logout(
	ctx context.Context,
	authenticated session.AuthenticatedSession,
) error {
	if err := s.repository.Logout(ctx, authenticated, s.now().UTC()); err != nil {
		return fmt.Errorf("logout: %w", err)
	}

	return nil
}

func (s *Service) findAccount(
	ctx context.Context,
	normalizedLogin string,
	normalizeErr error,
) (user.User, string, bool, error) {
	if normalizeErr != nil {
		return user.User{}, "", false, nil
	}

	account, encodedHash, err := s.repository.FindByLogin(ctx, normalizedLogin)
	if errors.Is(err, user.ErrUserNotFound) {
		return user.User{}, "", false, nil
	}
	if err != nil {
		return user.User{}, "", false, fmt.Errorf("find user for login: %w", err)
	}

	return account, encodedHash, true, nil
}

func (s *Service) failLogin(ctx context.Context, attempt Attempt) error {
	if err := s.repository.RecordFailure(ctx, attempt); err != nil {
		return fmt.Errorf("record failed login: %w", err)
	}
	return ErrInvalidCredentials
}

func newAttempt(
	rawLogin string,
	normalizedLogin string,
	normalizeErr error,
	remoteIP string,
	now time.Time,
) Attempt {
	canonicalLogin := strings.ToLower(strings.TrimSpace(rawLogin))
	label := normalizedLogin
	if normalizeErr != nil {
		label = "invalid-login"
	}

	return Attempt{
		LoginKey:   sha256Bytes("login:" + canonicalLogin),
		IPKey:      sha256Bytes("ip:" + remoteIP),
		LoginLabel: label,
		RemoteIP:   remoteIP,
		OccurredAt: now,
	}
}

func sha256Bytes(value string) []byte {
	digest := sha256.Sum256([]byte(value))
	return digest[:]
}
