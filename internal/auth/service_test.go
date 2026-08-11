package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Dyuzhovsergey/sup-rental/internal/session"
	"github.com/Dyuzhovsergey/sup-rental/internal/user"
)

func TestServiceLoginSuccess(t *testing.T) {
	now := time.Date(2026, time.August, 11, 12, 0, 0, 0, time.UTC)
	account := user.User{ID: 7, Login: "admin", Role: user.RoleAdmin, Active: true}
	repository := &authRepositoryStub{
		findAccount: account,
		findHash:    "stored-hash",
		created: session.Session{
			ID:         11,
			UserID:     account.ID,
			LastSeenAt: now,
		},
	}
	service := newTestService(t, repository, true)
	service.now = func() time.Time { return now }

	result, err := service.Login(context.Background(), LoginInput{
		Login:    " Admin ",
		Password: "secret1",
		RemoteIP: "127.0.0.1",
	})
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if result.User.ID != account.ID || result.Session.ID != 11 || result.Token != "raw-token" {
		t.Errorf("Login() = %+v", result)
	}
	if repository.completed.Account.Login != "admin" ||
		repository.completed.ExpectedPasswordHash != "stored-hash" {
		t.Errorf("CompleteLogin() params = %+v", repository.completed)
	}
	if repository.completed.Attempt.LoginLabel != "admin" ||
		repository.completed.Attempt.RemoteIP != "127.0.0.1" {
		t.Errorf("CompleteLogin() attempt = %+v", repository.completed.Attempt)
	}
}

func TestServiceLoginUsesGenericFailure(t *testing.T) {
	tests := []struct {
		name       string
		input      LoginInput
		account    user.User
		findErr    error
		passwordOK bool
		wantFind   bool
	}{
		{
			name:     "unknown user",
			input:    LoginInput{Login: "missing", Password: "secret1", RemoteIP: "127.0.0.1"},
			findErr:  user.ErrUserNotFound,
			wantFind: true,
		},
		{
			name:       "disabled user",
			input:      LoginInput{Login: "operator", Password: "secret1", RemoteIP: "127.0.0.1"},
			account:    user.User{ID: 2, Login: "operator", Role: user.RoleOperator},
			passwordOK: true,
			wantFind:   true,
		},
		{
			name:     "wrong password",
			input:    LoginInput{Login: "operator", Password: "secret1", RemoteIP: "127.0.0.1"},
			account:  user.User{ID: 2, Login: "operator", Role: user.RoleOperator, Active: true},
			wantFind: true,
		},
		{
			name:  "invalid login",
			input: LoginInput{Login: "?", Password: "secret1", RemoteIP: "127.0.0.1"},
		},
		{
			name:       "invalid password length",
			input:      LoginInput{Login: "operator", Password: "x", RemoteIP: "127.0.0.1"},
			account:    user.User{ID: 2, Login: "operator", Role: user.RoleOperator, Active: true},
			passwordOK: true,
			wantFind:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repository := &authRepositoryStub{
				findAccount: tt.account,
				findHash:    "stored-hash",
				findErr:     tt.findErr,
			}
			service := newTestService(t, repository, tt.passwordOK)

			_, err := service.Login(context.Background(), tt.input)
			if !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("Login() error = %v, want ErrInvalidCredentials", err)
			}
			if repository.findCalled != tt.wantFind {
				t.Errorf("FindByLogin() called = %t, want %t", repository.findCalled, tt.wantFind)
			}
			if repository.failureCount != 1 {
				t.Errorf("RecordFailure() calls = %d, want 1", repository.failureCount)
			}
		})
	}
}

func TestServiceLoginThrottled(t *testing.T) {
	now := time.Date(2026, time.August, 11, 12, 0, 0, 0, time.UTC)
	repository := &authRepositoryStub{blockedUntil: now.Add(10 * time.Minute)}
	service := newTestService(t, repository, true)
	service.now = func() time.Time { return now }

	_, err := service.Login(context.Background(), LoginInput{
		Login: "admin", Password: "secret1", RemoteIP: "127.0.0.1",
	})
	if !errors.Is(err, ErrLoginThrottled) {
		t.Fatalf("Login() error = %v, want ErrLoginThrottled", err)
	}
	var throttled *ThrottledError
	if !errors.As(err, &throttled) || !throttled.Until.Equal(repository.blockedUntil) {
		t.Errorf("Login() throttled error = %v", err)
	}
	if repository.throttledCount != 1 || repository.findCalled {
		t.Errorf("repository calls: throttled=%d find=%t", repository.throttledCount, repository.findCalled)
	}
}

func TestServiceLoginRecordsConcurrentStateChangeAsFailure(t *testing.T) {
	repository := &authRepositoryStub{
		findAccount: user.User{ID: 1, Login: "admin", Role: user.RoleAdmin, Active: true},
		findHash:    "stored-hash",
		completeErr: ErrLoginStateChanged,
	}
	service := newTestService(t, repository, true)

	_, err := service.Login(context.Background(), LoginInput{
		Login: "admin", Password: "secret1", RemoteIP: "127.0.0.1",
	})
	if !errors.Is(err, ErrInvalidCredentials) || repository.failureCount != 1 {
		t.Errorf("Login() error = %v, failure calls = %d", err, repository.failureCount)
	}
}

func TestServiceLogout(t *testing.T) {
	repository := &authRepositoryStub{}
	service := newTestService(t, repository, true)
	authenticated := session.AuthenticatedSession{
		Session: session.Session{ID: 3, UserID: 2},
		User:    user.User{ID: 2, Login: "operator", Role: user.RoleOperator, Active: true},
	}

	if err := service.Logout(context.Background(), authenticated); err != nil {
		t.Fatalf("Logout() error = %v", err)
	}
	if repository.loggedOut.Session.ID != authenticated.Session.ID {
		t.Errorf("Logout() session = %+v", repository.loggedOut.Session)
	}
}

func newTestService(
	t *testing.T,
	repository *authRepositoryStub,
	passwordMatches bool,
) *Service {
	t.Helper()

	service, err := NewService(
		repository,
		&authHasherStub{matches: passwordMatches},
		&sessionPreparerStub{},
	)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return service
}

type authRepositoryStub struct {
	findAccount    user.User
	findHash       string
	findErr        error
	findCalled     bool
	blockedUntil   time.Time
	blockedErr     error
	failureCount   int
	failureErr     error
	throttledCount int
	throttledErr   error
	completed      CompleteLoginParams
	created        session.Session
	completeErr    error
	loggedOut      session.AuthenticatedSession
	logoutErr      error
}

func (r *authRepositoryStub) FindByLogin(_ context.Context, _ string) (user.User, string, error) {
	r.findCalled = true
	return r.findAccount, r.findHash, r.findErr
}

func (r *authRepositoryStub) BlockedUntil(_ context.Context, _ Attempt) (time.Time, error) {
	return r.blockedUntil, r.blockedErr
}

func (r *authRepositoryStub) RecordFailure(_ context.Context, _ Attempt) error {
	r.failureCount++
	return r.failureErr
}

func (r *authRepositoryStub) RecordThrottled(_ context.Context, _ Attempt) error {
	r.throttledCount++
	return r.throttledErr
}

func (r *authRepositoryStub) CompleteLogin(
	_ context.Context,
	params CompleteLoginParams,
) (session.Session, error) {
	r.completed = params
	return r.created, r.completeErr
}

func (r *authRepositoryStub) Logout(
	_ context.Context,
	authenticated session.AuthenticatedSession,
	_ time.Time,
) error {
	r.loggedOut = authenticated
	return r.logoutErr
}

type authHasherStub struct {
	matches bool
}

func (h *authHasherStub) Hash(string) (string, error) {
	return "dummy-hash", nil
}

func (h *authHasherStub) Verify(_, _ string) (bool, error) {
	return h.matches, nil
}

type sessionPreparerStub struct{}

func (s *sessionPreparerStub) Prepare(userID int64) (session.Prepared, error) {
	return session.Prepared{
		Token: "raw-token",
		Params: session.CreateParams{
			UserID:            userID,
			TokenDigest:       make([]byte, 32),
			CSRFToken:         "csrf-token",
			CreatedAt:         time.Now(),
			AbsoluteExpiresAt: time.Now().Add(session.AbsoluteLifetime),
		},
	}, nil
}
