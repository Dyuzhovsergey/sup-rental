package equipment

import (
	"context"
	"errors"
	"fmt"

	"github.com/Dyuzhovsergey/sup-rental/internal/user"
)

var (
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
	// ErrInvalidListPage означает некорректный номер или размер страницы.
	ErrInvalidListPage = errors.New("invalid equipment list page")
	// ErrInvalidListScope означает неизвестную группу оборудования.
	ErrInvalidListScope = errors.New("invalid equipment list scope")
)

// Repository определяет операции хранения, необходимые сервису оборудования.
type Repository interface {
	CreateBatch(ctx context.Context, actor user.User, input BatchCreateInput) (Batch, error)
	List(ctx context.Context) ([]Item, error)
	ListPage(ctx context.Context, input ListPageInput) (ListPage, error)
	Get(ctx context.Context, id int64) (Item, error)
	UpdateStatus(ctx context.Context, actor user.User, id int64, status Status) (Item, error)
	ChangeModel(ctx context.Context, actor user.User, id int64, input ModelChangeInput) (Item, error)
	ChangeModelRate(ctx context.Context, actor user.User, id int64, hourlyRateKopecks int64) (ModelRateChange, error)
	Delete(ctx context.Context, actor user.User, id int64) (Item, error)
}

// AllowedPageSizes возвращает разрешённые количества строк на странице.
// Новый slice не позволяет вызывающему коду изменить правила сервиса.
func AllowedPageSizes() []int { return []int{5, 10, 15} }

// Service реализует сценарии учёта оборудования.
type Service struct {
	repository Repository
}

// UpdateInput содержит изменяемые данные зарегистрированного оборудования.
type UpdateInput struct {
	// Status — выбранное физическое состояние оборудования.
	Status Status
}

// NewService создаёт сервис оборудования с обязательным repository.
func NewService(repository Repository) *Service {
	return &Service{repository: repository}
}

// CreateBatch проверяет модель, тариф и количество, затем атомарно регистрирует
// партию отдельных физических единиц оборудования.
func (s *Service) CreateBatch(
	ctx context.Context,
	actor user.User,
	input BatchCreateInput,
) (Batch, error) {
	if err := requireActiveAdmin(actor); err != nil {
		return Batch{}, err
	}
	if !input.Kind.Valid() {
		return Batch{}, ErrInvalidKind
	}
	modelCode, err := NormalizeModelCode(input.ModelCode)
	if err != nil {
		return Batch{}, err
	}
	if _, err := HourlyRateKopecks(input.HourlyRateRubles); err != nil {
		return Batch{}, err
	}
	if err := ValidateBatchQuantity(input.Quantity); err != nil {
		return Batch{}, err
	}

	input.ModelCode = modelCode
	created, err := s.repository.CreateBatch(ctx, actor, input)
	if err != nil {
		return Batch{}, fmt.Errorf("create equipment batch: %w", err)
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

// ListPage возвращает одну страницу выбранной группы оборудования.
func (s *Service) ListPage(ctx context.Context, input ListPageInput) (ListPage, error) {
	if input.Scope != ListScopeActive && input.Scope != ListScopeRetired {
		return ListPage{}, ErrInvalidListScope
	}
	if input.Page <= 0 || !validPageSize(input.PageSize) {
		return ListPage{}, ErrInvalidListPage
	}

	page, err := s.repository.ListPage(ctx, input)
	if err != nil {
		return ListPage{}, fmt.Errorf("list equipment page: %w", err)
	}
	return page, nil
}

func validPageSize(size int) bool {
	for _, allowed := range AllowedPageSizes() {
		if size == allowed {
			return true
		}
	}
	return false
}

// Get возвращает оборудование по внутреннему идентификатору.
func (s *Service) Get(ctx context.Context, id int64) (Item, error) {
	item, err := s.repository.Get(ctx, id)
	if err != nil {
		return Item{}, fmt.Errorf("get equipment: %w", err)
	}

	return item, nil
}

// Update изменяет обратимое физическое состояние оборудования.
//
// Редактирование разрешено только для доступного оборудования и оборудования
// на обслуживании. Через форму редактирования можно переключаться только между
// этими двумя состояниями; списание требует отдельного подтверждения. Номер,
// тип, модель и тариф в этом инкременте не редактируются.
func (s *Service) Update(ctx context.Context, actor user.User, id int64, input UpdateInput) (Item, error) {
	if err := requireActiveAdmin(actor); err != nil {
		return Item{}, err
	}
	item, err := s.repository.Get(ctx, id)
	if err != nil {
		return Item{}, fmt.Errorf("get equipment for update: %w", err)
	}

	if !item.Status.CanEditDetails() {
		return Item{}, ErrEquipmentUpdateNotAllowed
	}

	if !input.Status.Valid() {
		return Item{}, ErrInvalidStatus
	}

	if !input.Status.CanEditDetails() ||
		(input.Status != item.Status && !item.Status.CanTransitionTo(input.Status)) {
		return Item{}, ErrStatusTransitionNotAllowed
	}

	if input.Status == item.Status {
		return item, nil
	}

	updated, err := s.repository.UpdateStatus(ctx, actor, id, input.Status)
	if err != nil {
		return Item{}, fmt.Errorf("update equipment: %w", err)
	}

	return updated, nil
}

// ChangeModel переносит физическую единицу в существующую или новую модель.
//
// Внутренний ID и состояние сохраняются, а инвентарный номер формируется заново
// из типа, кода и следующего номера целевой модели. Для существующей модели
// переданный тариф должен совпадать с её общим тарифом.
func (s *Service) ChangeModel(
	ctx context.Context,
	actor user.User,
	id int64,
	input ModelChangeInput,
) (Item, error) {
	if err := requireActiveAdmin(actor); err != nil {
		return Item{}, err
	}
	if !input.Kind.Valid() {
		return Item{}, ErrInvalidKind
	}
	modelCode, err := NormalizeModelCode(input.ModelCode)
	if err != nil {
		return Item{}, err
	}
	if _, err := HourlyRateKopecks(input.HourlyRateRubles); err != nil {
		return Item{}, err
	}

	item, err := s.repository.Get(ctx, id)
	if err != nil {
		return Item{}, fmt.Errorf("get equipment for model change: %w", err)
	}
	if !item.Status.CanEditDetails() {
		return Item{}, ErrEquipmentUpdateNotAllowed
	}
	if item.Kind == input.Kind && item.ModelCode == modelCode {
		return Item{}, ErrEquipmentModelUnchanged
	}

	input.ModelCode = modelCode
	updated, err := s.repository.ChangeModel(ctx, actor, id, input)
	if err != nil {
		return Item{}, fmt.Errorf("change equipment model: %w", err)
	}
	return updated, nil
}

// ChangeModelRate изменяет общий тариф модели выбранной физической единицы.
// Изменение отражается на всех единицах этой модели и возвращает их количество.
func (s *Service) ChangeModelRate(
	ctx context.Context,
	actor user.User,
	id int64,
	hourlyRateRubles int64,
) (ModelRateChange, error) {
	if err := requireActiveAdmin(actor); err != nil {
		return ModelRateChange{}, err
	}
	hourlyRateKopecks, err := HourlyRateKopecks(hourlyRateRubles)
	if err != nil {
		return ModelRateChange{}, err
	}

	item, err := s.repository.Get(ctx, id)
	if err != nil {
		return ModelRateChange{}, fmt.Errorf("get equipment for model rate change: %w", err)
	}
	if !item.Status.CanEditDetails() {
		return ModelRateChange{}, ErrEquipmentUpdateNotAllowed
	}
	if item.HourlyRateKopecks == hourlyRateKopecks {
		return ModelRateChange{}, ErrModelRateUnchanged
	}

	changed, err := s.repository.ChangeModelRate(ctx, actor, id, hourlyRateKopecks)
	if err != nil {
		return ModelRateChange{}, fmt.Errorf("change equipment model rate: %w", err)
	}
	return changed, nil
}

// ChangeStatus изменяет физическое состояние оборудования, если ручной
// переход разрешён для его текущего состояния.
func (s *Service) ChangeStatus(ctx context.Context, actor user.User, id int64, target Status) (Item, error) {
	if err := requireActiveAdmin(actor); err != nil {
		return Item{}, err
	}
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

	updated, err := s.repository.UpdateStatus(ctx, actor, id, target)
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
func (s *Service) Delete(ctx context.Context, actor user.User, id int64) (Item, error) {
	if err := requireActiveAdmin(actor); err != nil {
		return Item{}, err
	}
	item, err := s.repository.Get(ctx, id)
	if err != nil {
		return Item{}, fmt.Errorf("get equipment for deletion: %w", err)
	}

	if item.Status != StatusRetired {
		return Item{}, ErrEquipmentDeleteNotAllowed
	}

	deleted, err := s.repository.Delete(ctx, actor, id)
	if err != nil {
		return Item{}, fmt.Errorf("delete equipment: %w", err)
	}

	return deleted, nil
}

func requireActiveAdmin(actor user.User) error {
	if actor.ID <= 0 || actor.Role != user.RoleAdmin || !actor.Active {
		return user.ErrAccessDenied
	}
	return nil
}
