package equipment

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrInventoryNumberRequired означает, что инвентарный номер не указан.
	ErrInventoryNumberRequired = errors.New("inventory number is required")
	// ErrInvalidKind означает, что передан неподдерживаемый тип оборудования.
	ErrInvalidKind = errors.New("invalid equipment kind")
	// ErrInventoryNumberExists означает, что инвентарный номер уже используется.
	ErrInventoryNumberExists = errors.New("inventory number already exists")
	// ErrEquipmentNotFound означает, что оборудование с указанным ID не найдено.
	ErrEquipmentNotFound = errors.New("equipment not found")
	// ErrInvalidStatus означает, что передано неподдерживаемое состояние.
	ErrInvalidStatus = errors.New("invalid equipment status")
	// ErrStatusTransitionNotAllowed означает, что ручной переход запрещён.
	ErrStatusTransitionNotAllowed = errors.New("equipment status transition is not allowed")
)

// Repository определяет операции хранения, необходимые сервису оборудования.
type Repository interface {
	Create(ctx context.Context, item Item) (Item, error)
	List(ctx context.Context) ([]Item, error)
	Get(ctx context.Context, id int64) (Item, error)
	UpdateStatus(ctx context.Context, id int64, status Status) (Item, error)
}

// Service реализует сценарии создания и просмотра оборудования.
type Service struct {
	repository Repository
}

// CreateInput содержит данные для регистрации нового оборудования.
type CreateInput struct {
	// InventoryNumber — введённый администратором инвентарный номер.
	InventoryNumber string
	// Kind — выбранный тип оборудования.
	Kind Kind
}

// NewService создаёт сервис оборудования с обязательным repository.
func NewService(repository Repository) *Service {
	return &Service{repository: repository}
}

// Create проверяет данные и регистрирует новое доступное оборудование.
//
// Внешние пробелы инвентарного номера удаляются. Повторный номер возвращает
// ErrInventoryNumberExists независимо от регистра, если repository
// обеспечивает согласованный контракт уникальности.
func (s *Service) Create(ctx context.Context, input CreateInput) (Item, error) {
	inventoryNumber := strings.TrimSpace(input.InventoryNumber)
	if inventoryNumber == "" {
		return Item{}, ErrInventoryNumberRequired
	}

	if !input.Kind.Valid() {
		return Item{}, ErrInvalidKind
	}

	item := Item{
		InventoryNumber: inventoryNumber,
		Kind:            input.Kind,
		Status:          StatusAvailable,
	}

	created, err := s.repository.Create(ctx, item)
	if err != nil {
		return Item{}, fmt.Errorf("create equipment: %w", err)
	}

	return created, nil
}

// List возвращает оборудование в порядке, определённом repository.
func (s *Service) List(ctx context.Context) ([]Item, error) {
	items, err := s.repository.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list equipment: %w", err)
	}

	return items, nil
}

// ChangeStatus изменяет физическое состояние оборудования, если ручной
// переход разрешён для его текущего состояния.
func (s *Service) ChangeStatus(ctx context.Context, id int64, target Status) (Item, error) {
	if !target.Valid() {
		return Item{}, ErrInvalidStatus
	}

	item, err := s.repository.Get(ctx, id)
	if err != nil {
		return Item{}, fmt.Errorf("get equipment: %w", err)
	}

	if !item.Status.CanTransitionTo(target) {
		return Item{}, ErrStatusTransitionNotAllowed
	}

	updated, err := s.repository.UpdateStatus(ctx, id, target)
	if err != nil {
		return Item{}, fmt.Errorf("update equipment status: %w", err)
	}

	return updated, nil
}
