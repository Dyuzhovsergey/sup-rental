package session

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Dyuzhovsergey/sup-rental/internal/user"
)

func TestServiceCreateGeneratesSeparateSecrets(t *testing.T) {
	repository := &repositoryStub{}
	now := time.Date(2026, time.August, 11, 10, 0, 0, 0, time.FixedZone("test", 3*60*60))
	service := NewService(repository)
	service.now = func() time.Time { return now }
	service.random = bytes.NewReader(append(bytes.Repeat([]byte{1}, secretSize), bytes.Repeat([]byte{2}, secretSize)...))

	created, token, err := service.Create(context.Background(), 42)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if token == "" || token == repository.params.CSRFToken {
		t.Error("Create() tokens are empty or equal")
	}
	if bytes.Equal([]byte(token), repository.params.TokenDigest) {
		t.Error("Create() stored raw token instead of digest")
	}
	if len(repository.params.TokenDigest) != sha256DigestSize {
		t.Errorf("Create() digest length = %d, want %d", len(repository.params.TokenDigest), sha256DigestSize)
	}
	if repository.params.UserID != 42 || !repository.params.CreatedAt.Equal(now.UTC()) {
		t.Errorf("Create() params = %+v", repository.params)
	}
	if !repository.params.AbsoluteExpiresAt.Equal(now.UTC().Add(AbsoluteLifetime)) {
		t.Errorf("Create() absolute expiry = %v", repository.params.AbsoluteExpiresAt)
	}
	if created.UserID != 42 {
		t.Errorf("Create() session = %+v", created)
	}
}

func TestServiceResolveUsesDigestAndPolicy(t *testing.T) {
	repository := &repositoryStub{}
	now := time.Date(2026, time.August, 11, 10, 0, 0, 0, time.UTC)
	service := NewService(repository)
	service.now = func() time.Time { return now }
	token := encodedToken(3)

	_, err := service.Resolve(context.Background(), token)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if string(repository.digest) == token {
		t.Error("Resolve() passed raw token to repository")
	}
	if !repository.now.Equal(now) || repository.idleTimeout != IdleTimeout {
		t.Errorf("Resolve() now = %v, idle = %v", repository.now, repository.idleTimeout)
	}
}

func TestServiceRejectsInvalidTokensWithoutRepository(t *testing.T) {
	service := NewService(&repositoryStub{})

	for _, token := range []string{"", "not-base64", encodedToken(1) + "x"} {
		_, err := service.Resolve(context.Background(), token)
		if !errors.Is(err, ErrInvalidToken) {
			t.Errorf("Resolve(%q) error = %v, want ErrInvalidToken", token, err)
		}
		if err := service.Revoke(context.Background(), token); !errors.Is(err, ErrInvalidToken) {
			t.Errorf("Revoke(%q) error = %v, want ErrInvalidToken", token, err)
		}
	}
}

func TestServicePropagatesRepositoryErrors(t *testing.T) {
	repositoryError := errors.New("repository unavailable")
	repository := &repositoryStub{err: repositoryError}
	service := NewService(repository)

	if _, _, err := service.Create(context.Background(), 1); !errors.Is(err, repositoryError) {
		t.Errorf("Create() error = %v, want repository error", err)
	}
	if _, err := service.Resolve(context.Background(), encodedToken(1)); !errors.Is(err, repositoryError) {
		t.Errorf("Resolve() error = %v, want repository error", err)
	}
	if err := service.Revoke(context.Background(), encodedToken(1)); !errors.Is(err, repositoryError) {
		t.Errorf("Revoke() error = %v, want repository error", err)
	}
	if err := service.RevokeAll(context.Background(), 1); !errors.Is(err, repositoryError) {
		t.Errorf("RevokeAll() error = %v, want repository error", err)
	}
}

const sha256DigestSize = 32

type repositoryStub struct {
	params      CreateParams
	digest      []byte
	now         time.Time
	idleTimeout time.Duration
	err         error
}

func (r *repositoryStub) Create(_ context.Context, params CreateParams) (Session, error) {
	r.params = params
	return Session{UserID: params.UserID}, r.err
}

func (r *repositoryStub) Resolve(
	_ context.Context,
	digest []byte,
	now time.Time,
	idleTimeout time.Duration,
) (AuthenticatedSession, error) {
	r.digest = digest
	r.now = now
	r.idleTimeout = idleTimeout
	return AuthenticatedSession{User: user.User{ID: 1, Active: true}}, r.err
}

func (r *repositoryStub) Revoke(_ context.Context, digest []byte, now time.Time) error {
	r.digest = digest
	r.now = now
	return r.err
}

func (r *repositoryStub) RevokeAll(_ context.Context, _ int64, now time.Time) error {
	r.now = now
	return r.err
}

func encodedToken(value byte) string {
	service := NewService(&repositoryStub{})
	service.random = bytes.NewReader(bytes.Repeat([]byte{value}, secretSize))
	token, err := service.randomToken()
	if err != nil {
		panic(err)
	}
	return token
}
