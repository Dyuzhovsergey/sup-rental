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
	// ErrEquipmentUpdateNotAllowed означает, что данные предмета нельзя
	// редактировать в его текущем состоянии.
	ErrEquipmentUpdateNotAllowed = errors.New("equipment update is not allowed")
	// ErrEquipmentDeleteNotAllowed означает, что предмет нельзя удалить в его
	// текущем состоянии.
	ErrEquipmentDeleteNotAllowed = errors.New("equipment deletion is not allowed")
	// ErrEquipmentHasHistory означает, что предмет связан с историческими
	// данными и поэтому не может быть удалён.
	ErrEquipmentHasHistory = errors.New("equipment has history")
)

// Repository определяет операции хранения, необходимые сервису оборудования.
type Repository interface {
	Create(ctx context.Context, item Item) (Item, error)
	List(ctx context.Context) ([]Item, error)
	Get(ctx context.Context, id int64) (Item, error)
	Update(ctx context.Context, id int64, inventoryNumber string, kind Kind, status Status) (Item, error)
	UpdateStatus(ctx context.Context, id int64, status Status) (Item, error)
	Delete(ctx context.Context, id int64) error
}

// Service реализует сценарии учёта оборудования.
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

// UpdateInput содержит изменяемые данные зарегистрированного оборудования.
type UpdateInput struct {
	// InventoryNumber — исправленный инвентарный номер.
	InventoryNumber string
	// Kind — исправленный тип оборудования.
	Kind Kind
	// Status — выбранное физическое состояние оборудования.
	Status Status
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

// Get возвращает оборудование по внутреннему идентификатору.
func (s *Service) Get(ctx context.Context, id int64) (Item, error) {
	item, err := s.repository.Get(ctx, id)
	if err != nil {
		return Item{}, fmt.Errorf("get equipment: %w", err)
	}

	return item, nil
}

// Update изменяет инвентарный номер, тип и обратимое физическое состояние
// оборудования.
//
// Редактирование разрешено только для доступного оборудования и оборудования
// на обслуживании. Через форму редактирования можно переключаться только между
// этими двумя состояниями; списание требует отдельного подтверждения. Внешние
// пробелы инвентарного номера удаляются.
func (s *Service) Update(ctx context.Context, id int64, input UpdateInput) (Item, error) {
	item, err := s.repository.Get(ctx, id)
	if err != nil {
		return Item{}, fmt.Errorf("get equipment for update: %w", err)
	}

	if !item.Status.CanEditDetails() {
		return Item{}, ErrEquipmentUpdateNotAllowed
	}

	inventoryNumber := strings.TrimSpace(input.InventoryNumber)
	if inventoryNumber == "" {
		return Item{}, ErrInventoryNumberRequired
	}

	if !input.Kind.Valid() {
		return Item{}, ErrInvalidKind
	}

	if !input.Status.Valid() {
		return Item{}, ErrInvalidStatus
	}

	if !input.Status.CanEditDetails() ||
		(input.Status != item.Status && !item.Status.CanTransitionTo(input.Status)) {
		return Item{}, ErrStatusTransitionNotAllowed
	}

	updated, err := s.repository.Update(
		ctx,
		id,
		inventoryNumber,
		input.Kind,
		input.Status,
	)
	if err != nil {
		return Item{}, fmt.Errorf("update equipment: %w", err)
	}

	return updated, nil
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

// Delete удаляет списанное оборудование и возвращает данные удалённого
// предмета.
//
// Оборудование в любом другом состоянии сохраняется. Repository может вернуть
// ErrEquipmentHasHistory, если предмет уже связан с историческими данными.
// Возвращённый предмет позволяет вызывающему коду сформировать подтверждение
// удаления без повторного запроса уже удалённой записи.
func (s *Service) Delete(ctx context.Context, id int64) (Item, error) {
	item, err := s.repository.Get(ctx, id)
	if err != nil {
		return Item{}, fmt.Errorf("get equipment for deletion: %w", err)
	}

	if item.Status != StatusRetired {
		return Item{}, ErrEquipmentDeleteNotAllowed
	}

	if err := s.repository.Delete(ctx, id); err != nil {
		return Item{}, fmt.Errorf("delete equipment: %w", err)
	}

	return item, nil
}
