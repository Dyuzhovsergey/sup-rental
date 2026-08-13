package client

import (
	"context"
	"errors"
	"fmt"

	"github.com/Dyuzhovsergey/sup-rental/internal/user"
)

// DefaultPageSize задаёт количество клиентов на странице списка по умолчанию.
const DefaultPageSize = 5

var allowedPageSizes = [...]int{5, 10, 15}

var (
	// ErrInvalidPage означает некорректный номер страницы или количество строк.
	ErrInvalidPage = errors.New("invalid client page")
)

// Page содержит одну страницу клиентов и общее количество записей.
type Page struct {
	// Clients содержит клиентов текущей страницы.
	Clients []Client
	// Total — общее количество клиентов.
	Total int
	// Page — номер текущей страницы начиная с единицы.
	Page int
}

// Repository определяет операции хранения, необходимые сервису клиентов.
type Repository interface {
	Create(ctx context.Context, actor user.User, customer Client) (Client, error)
	Update(ctx context.Context, actor user.User, customer Client) (Client, error)
	Get(ctx context.Context, id int64) (Client, error)
	FindByPhone(ctx context.Context, phone Phone) (Client, error)
	ListPage(ctx context.Context, page, pageSize int) (Page, error)
}

// Service реализует сценарии поиска, просмотра и создания клиентов.
type Service struct{ repository Repository }

// NewService создаёт сервис клиентов с обязательным repository.
func NewService(repository Repository) *Service { return &Service{repository: repository} }

// Create нормализует данные и создаёт клиента от имени активного оператора.
func (s *Service) Create(ctx context.Context, actor user.User, fullName, rawPhone string) (Client, error) {
	if actor.ID <= 0 || actor.Role != user.RoleOperator || !actor.Active {
		return Client{}, user.ErrAccessDenied
	}
	customer, err := New(fullName, rawPhone)
	if err != nil {
		return Client{}, err
	}
	created, err := s.repository.Create(ctx, actor, customer)
	if err != nil {
		return Client{}, fmt.Errorf("create client: %w", err)
	}
	return created, nil
}

// Update нормализует и изменяет контактные данные клиента от имени активного
// оператора. Для неположительного ID возвращается ErrClientNotFound.
func (s *Service) Update(
	ctx context.Context,
	actor user.User,
	id int64,
	fullName, rawPhone string,
) (Client, error) {
	if actor.ID <= 0 || actor.Role != user.RoleOperator || !actor.Active {
		return Client{}, user.ErrAccessDenied
	}
	if id <= 0 {
		return Client{}, ErrClientNotFound
	}

	customer, err := New(fullName, rawPhone)
	if err != nil {
		return Client{}, err
	}
	customer.ID = id

	updated, err := s.repository.Update(ctx, actor, customer)
	if err != nil {
		return Client{}, fmt.Errorf("update client: %w", err)
	}
	return updated, nil
}

// Get возвращает клиента по положительному внутреннему идентификатору.
func (s *Service) Get(ctx context.Context, id int64) (Client, error) {
	if id <= 0 {
		return Client{}, ErrClientNotFound
	}
	customer, err := s.repository.Get(ctx, id)
	if err != nil {
		return Client{}, fmt.Errorf("get client: %w", err)
	}
	return customer, nil
}

// FindByPhone нормализует введённый телефон и выполняет точный поиск.
func (s *Service) FindByPhone(ctx context.Context, rawPhone string) (Client, error) {
	phone, err := NormalizePhone(rawPhone)
	if err != nil {
		return Client{}, err
	}
	customer, err := s.repository.FindByPhone(ctx, phone)
	if err != nil {
		return Client{}, fmt.Errorf("find client by phone: %w", err)
	}
	return customer, nil
}

// AllowedPageSizes возвращает допустимые количества клиентов на странице.
// Возвращаемый slice является копией и может безопасно изменяться вызывающим кодом.
func AllowedPageSizes() []int {
	return append([]int(nil), allowedPageSizes[:]...)
}

// ListPage возвращает страницу клиентов от новых записей к старым.
func (s *Service) ListPage(ctx context.Context, page, pageSize int) (Page, error) {
	if page <= 0 || !allowedPageSize(pageSize) {
		return Page{}, ErrInvalidPage
	}
	result, err := s.repository.ListPage(ctx, page, pageSize)
	if err != nil {
		return Page{}, fmt.Errorf("list clients: %w", err)
	}
	return result, nil
}

func allowedPageSize(value int) bool {
	for _, allowed := range allowedPageSizes {
		if value == allowed {
			return true
		}
	}
	return false
}
