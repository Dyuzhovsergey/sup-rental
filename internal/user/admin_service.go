package user

import (
	"context"
	"fmt"
)

// AdminRepository определяет атомарные операции хранения, необходимые CLI
// управления единственным администратором.
type AdminRepository interface {
	CreateAdmin(ctx context.Context, account User, passwordHash string) (User, error)
	ResetAdminPassword(ctx context.Context, passwordHash string) (User, error)
}

// PasswordHasher создаёт необратимый hash проверенного password.
type PasswordHasher interface {
	Hash(password string) (string, error)
}

// AdminService реализует создание единственного admin и восстановление доступа.
type AdminService struct {
	repository AdminRepository
	hasher     PasswordHasher
}

// NewAdminService создаёт сервис с обязательными repository и password hasher.
func NewAdminService(repository AdminRepository, hasher PasswordHasher) *AdminService {
	return &AdminService{repository: repository, hasher: hasher}
}

// CreateAdmin проверяет login и password, создаёт Argon2id hash и сохраняет
// единственного активного администратора вместе с обязательным audit event.
func (s *AdminService) CreateAdmin(
	ctx context.Context,
	login string,
	plainPassword string,
) (User, error) {
	account, err := New(login, RoleAdmin)
	if err != nil {
		return User{}, fmt.Errorf("validate admin: %w", err)
	}

	passwordHash, err := s.hasher.Hash(plainPassword)
	if err != nil {
		return User{}, fmt.Errorf("hash admin password: %w", err)
	}

	created, err := s.repository.CreateAdmin(ctx, account, passwordHash)
	if err != nil {
		return User{}, fmt.Errorf("create admin: %w", err)
	}

	return created, nil
}

// ResetAdminPassword создаёт новый hash и атомарно заменяет password
// единственного администратора вместе с обязательным audit event.
func (s *AdminService) ResetAdminPassword(
	ctx context.Context,
	plainPassword string,
) (User, error) {
	passwordHash, err := s.hasher.Hash(plainPassword)
	if err != nil {
		return User{}, fmt.Errorf("hash admin password: %w", err)
	}

	account, err := s.repository.ResetAdminPassword(ctx, passwordHash)
	if err != nil {
		return User{}, fmt.Errorf("reset admin password: %w", err)
	}

	return account, nil
}
