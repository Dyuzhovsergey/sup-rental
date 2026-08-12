package user

import (
	"context"
	"errors"
	"fmt"
)

var (
	// ErrOperatorNotFound означает, что учётная запись оператора не найдена.
	ErrOperatorNotFound = errors.New("operator not found")
	// ErrOperatorAlreadyActive означает, что оператор уже активен.
	ErrOperatorAlreadyActive = errors.New("operator is already active")
	// ErrOperatorAlreadyDisabled означает, что оператор уже отключён.
	ErrOperatorAlreadyDisabled = errors.New("operator is already disabled")
)

// OperatorRepository определяет атомарные операции хранения для управления
// операторами и их обязательными audit events.
type OperatorRepository interface {
	ListOperators(ctx context.Context) ([]User, error)
	GetOperator(ctx context.Context, id int64) (User, error)
	CreateOperator(ctx context.Context, actor User, account User, passwordHash string) (User, error)
	SetOperatorActive(ctx context.Context, actor User, id int64, active bool) (User, error)
	ChangeOperatorPassword(ctx context.Context, actor User, id int64, passwordHash string) (User, error)
}

// OperatorService реализует административные сценарии управления операторами.
type OperatorService struct {
	repository OperatorRepository
	hasher     PasswordHasher
}

// NewOperatorService создаёт сервис с обязательными repository и password hasher.
func NewOperatorService(repository OperatorRepository, hasher PasswordHasher) *OperatorService {
	return &OperatorService{repository: repository, hasher: hasher}
}

// List возвращает всех операторов после проверки действующего администратора.
func (s *OperatorService) List(ctx context.Context, actor User) ([]User, error) {
	if err := requireActiveAdmin(actor); err != nil {
		return nil, err
	}

	accounts, err := s.repository.ListOperators(ctx)
	if err != nil {
		return nil, fmt.Errorf("list operators: %w", err)
	}
	return accounts, nil
}

// Get возвращает оператора по ID после проверки действующего администратора.
func (s *OperatorService) Get(ctx context.Context, actor User, id int64) (User, error) {
	if err := requireActiveAdmin(actor); err != nil {
		return User{}, err
	}
	if id <= 0 {
		return User{}, ErrOperatorNotFound
	}

	account, err := s.repository.GetOperator(ctx, id)
	if err != nil {
		return User{}, fmt.Errorf("get operator: %w", err)
	}
	return account, nil
}

// Create проверяет login и постоянный password, создаёт Argon2id hash и
// атомарно сохраняет оператора вместе с audit event.
func (s *OperatorService) Create(
	ctx context.Context,
	actor User,
	login string,
	plainPassword string,
) (User, error) {
	if err := requireActiveAdmin(actor); err != nil {
		return User{}, err
	}

	account, err := New(login, RoleOperator)
	if err != nil {
		return User{}, fmt.Errorf("validate operator: %w", err)
	}
	passwordHash, err := s.hasher.Hash(plainPassword)
	if err != nil {
		return User{}, fmt.Errorf("hash operator password: %w", err)
	}

	created, err := s.repository.CreateOperator(ctx, actor, account, passwordHash)
	if err != nil {
		return User{}, fmt.Errorf("create operator: %w", err)
	}
	return created, nil
}

// Disable отключает оператора и отзывает все его активные сессии одной
// атомарной операцией с audit event.
func (s *OperatorService) Disable(ctx context.Context, actor User, id int64) (User, error) {
	return s.setActive(ctx, actor, id, false)
}

// Activate повторно разрешает оператору вход, сохраняя действующий password.
func (s *OperatorService) Activate(ctx context.Context, actor User, id int64) (User, error) {
	return s.setActive(ctx, actor, id, true)
}

// ChangePassword заменяет постоянный password оператора и отзывает все его
// активные сессии одной атомарной операцией с audit event.
func (s *OperatorService) ChangePassword(
	ctx context.Context,
	actor User,
	id int64,
	plainPassword string,
) (User, error) {
	if err := requireActiveAdmin(actor); err != nil {
		return User{}, err
	}
	if id <= 0 {
		return User{}, ErrOperatorNotFound
	}

	passwordHash, err := s.hasher.Hash(plainPassword)
	if err != nil {
		return User{}, fmt.Errorf("hash operator password: %w", err)
	}
	account, err := s.repository.ChangeOperatorPassword(ctx, actor, id, passwordHash)
	if err != nil {
		return User{}, fmt.Errorf("change operator password: %w", err)
	}
	return account, nil
}

func (s *OperatorService) setActive(
	ctx context.Context,
	actor User,
	id int64,
	active bool,
) (User, error) {
	if err := requireActiveAdmin(actor); err != nil {
		return User{}, err
	}
	if id <= 0 {
		return User{}, ErrOperatorNotFound
	}

	account, err := s.repository.SetOperatorActive(ctx, actor, id, active)
	if err != nil {
		return User{}, fmt.Errorf("set operator active: %w", err)
	}
	return account, nil
}

func requireActiveAdmin(actor User) error {
	if actor.ID <= 0 || actor.Role != RoleAdmin || !actor.Active {
		return ErrAccessDenied
	}
	return nil
}
