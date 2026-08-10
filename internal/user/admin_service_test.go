package user

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Dyuzhovsergey/sup-rental/internal/password"
)

func TestAdminServiceCreateAdmin(t *testing.T) {
	repository := &adminRepositoryStub{}
	hasher := &passwordHasherStub{hash: "encoded-password-hash"}
	service := NewAdminService(repository, hasher)

	created, err := service.CreateAdmin(context.Background(), "  ADMIN  ", "secret1")
	if err != nil {
		t.Fatalf("CreateAdmin() error = %v", err)
	}
	if created.Login != "admin" || created.Role != RoleAdmin || !created.Active {
		t.Errorf("CreateAdmin() = %+v, want active admin", created)
	}
	if hasher.gotPassword != "secret1" {
		t.Errorf("Hash() password = %q, want supplied password", hasher.gotPassword)
	}
	if repository.gotPasswordHash != hasher.hash {
		t.Errorf("CreateAdmin() hash = %q, want %q", repository.gotPasswordHash, hasher.hash)
	}
}

func TestAdminServiceCreateAdminErrors(t *testing.T) {
	tests := []struct {
		name       string
		login      string
		hashErr    error
		repoErr    error
		wantErr    error
		wantCalled bool
	}{
		{name: "invalid login", login: "_admin", wantErr: ErrInvalidLogin},
		{name: "hash error", login: "admin", hashErr: errors.New("hash failed"), wantErr: nil},
		{name: "admin exists", login: "admin", repoErr: ErrAdminExists, wantErr: ErrAdminExists, wantCalled: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repository := &adminRepositoryStub{createErr: tt.repoErr}
			hasher := &passwordHasherStub{hash: "hash", err: tt.hashErr}
			service := NewAdminService(repository, hasher)

			_, err := service.CreateAdmin(context.Background(), tt.login, "secret1")
			if tt.hashErr != nil {
				if err == nil || !errors.Is(err, tt.hashErr) {
					t.Fatalf("CreateAdmin() error = %v, want hash error", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("CreateAdmin() error = %v, want %v", err, tt.wantErr)
			}
			if repository.createCalled != tt.wantCalled {
				t.Errorf("CreateAdmin() repository called = %t, want %t", repository.createCalled, tt.wantCalled)
			}
		})
	}
}

func TestAdminServiceRejectsInvalidPassword(t *testing.T) {
	tests := []struct {
		name          string
		plainPassword string
		wantErr       error
	}{
		{name: "short", plainPassword: "12345", wantErr: password.ErrTooShort},
		{
			name:          "long",
			plainPassword: strings.Repeat("a", password.MaxLength+1),
			wantErr:       password.ErrTooLong,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repository := &adminRepositoryStub{}
			service := NewAdminService(repository, password.NewHasher())

			_, err := service.CreateAdmin(
				context.Background(),
				"admin",
				tt.plainPassword,
			)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("CreateAdmin() error = %v, want %v", err, tt.wantErr)
			}
			if repository.createCalled {
				t.Error("CreateAdmin() repository called for invalid password")
			}
		})
	}
}

func TestAdminServiceResetAdminPassword(t *testing.T) {
	repository := &adminRepositoryStub{
		resetAccount: User{ID: 1, Login: "admin", Role: RoleAdmin, Active: true},
	}
	hasher := &passwordHasherStub{hash: "new-password-hash"}
	service := NewAdminService(repository, hasher)

	account, err := service.ResetAdminPassword(context.Background(), "new-secret")
	if err != nil {
		t.Fatalf("ResetAdminPassword() error = %v", err)
	}
	if account.Login != "admin" {
		t.Errorf("ResetAdminPassword() Login = %q, want admin", account.Login)
	}
	if repository.gotPasswordHash != hasher.hash {
		t.Errorf("ResetAdminPassword() hash = %q, want %q", repository.gotPasswordHash, hasher.hash)
	}
}

func TestAdminServiceResetAdminPasswordErrors(t *testing.T) {
	hashErr := errors.New("hash failed")
	repositoryErr := errors.New("repository failed")

	tests := []struct {
		name    string
		hashErr error
		repoErr error
		wantErr error
	}{
		{name: "hash error", hashErr: hashErr, wantErr: hashErr},
		{name: "admin not found", repoErr: ErrUserNotFound, wantErr: ErrUserNotFound},
		{name: "repository error", repoErr: repositoryErr, wantErr: repositoryErr},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repository := &adminRepositoryStub{resetErr: tt.repoErr}
			hasher := &passwordHasherStub{hash: "hash", err: tt.hashErr}
			service := NewAdminService(repository, hasher)

			_, err := service.ResetAdminPassword(context.Background(), "secret1")
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ResetAdminPassword() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

type adminRepositoryStub struct {
	createCalled    bool
	createErr       error
	resetAccount    User
	resetErr        error
	gotPasswordHash string
}

func (r *adminRepositoryStub) CreateAdmin(
	_ context.Context,
	account User,
	passwordHash string,
) (User, error) {
	r.createCalled = true
	r.gotPasswordHash = passwordHash
	if r.createErr != nil {
		return User{}, r.createErr
	}
	account.ID = 1
	return account, nil
}

func (r *adminRepositoryStub) ResetAdminPassword(
	_ context.Context,
	passwordHash string,
) (User, error) {
	r.gotPasswordHash = passwordHash
	if r.resetErr != nil {
		return User{}, r.resetErr
	}
	return r.resetAccount, nil
}

type passwordHasherStub struct {
	hash        string
	err         error
	gotPassword string
}

func (h *passwordHasherStub) Hash(password string) (string, error) {
	h.gotPassword = password
	return h.hash, h.err
}
