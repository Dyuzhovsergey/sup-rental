package rental

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/Dyuzhovsergey/sup-rental/internal/equipment"
	"github.com/Dyuzhovsergey/sup-rental/internal/user"
)

const (
	// DefaultPageSize задаёт количество аренд на странице списка по умолчанию.
	DefaultPageSize = 5
)

var (
	// ErrInvalidPage означает некорректный номер страницы или размер страницы.
	ErrInvalidPage = errors.New("invalid rental page")
	// ErrInvalidModelSelection означает некорректный идентификатор модели,
	// повтор модели или отрицательное количество в запросе на создание черновика.
	ErrInvalidModelSelection = errors.New("invalid rental model selection")
	// ErrInsufficientEquipment означает, что доступных единиц выбранной модели
	// меньше запрошенного количества.
	ErrInsufficientEquipment = errors.New("insufficient available equipment")
)

var allowedPageSizes = [...]int{5, 10, 15}

// Repository определяет операции хранения, необходимые пользовательским
// сценариям аренды.
type Repository interface {
	Create(ctx context.Context, value Rental) (Rental, error)
	Get(ctx context.Context, id int64) (Rental, error)
	ListPage(ctx context.Context, page, pageSize int) (Page, error)
	AvailableEquipment(ctx context.Context, interval Interval) ([]equipment.Item, error)
}

// ModelSelection задаёт требуемое количество единиц одной модели.
type ModelSelection struct {
	// ModelID — внутренний идентификатор модели оборудования.
	ModelID int64
	// Quantity — требуемое количество физических единиц. Ноль означает, что
	// модель не добавляется в состав.
	Quantity int
}

// AvailableModel описывает одну модель и число единиц, доступных на период.
type AvailableModel struct {
	// ModelID — внутренний идентификатор модели.
	ModelID int64
	// Kind — тип оборудования.
	Kind equipment.Kind
	// ModelCode — нормализованный код модели.
	ModelCode string
	// HourlyRateKopecks — текущий часовой тариф модели в копейках.
	HourlyRateKopecks int64
	// AvailableCount — количество доступных физических единиц.
	AvailableCount int
}

// Summary содержит данные одной аренды для списка.
type Summary struct {
	// ID — внутренний идентификатор аренды.
	ID int64
	// ClientID — идентификатор клиента.
	ClientID int64
	// ClientName — снимок текущего ФИО клиента для списка.
	ClientName string
	// Interval — плановый период аренды.
	Interval Interval
	// Status — текущее состояние аренды.
	Status Status
	// ItemCount — число физических единиц в составе.
	ItemCount int
	// PlannedTotalKopecks — предварительная стоимость в копейках.
	PlannedTotalKopecks int64
}

// Page содержит одну страницу аренд и общее количество записей.
type Page struct {
	// Rentals содержит аренды текущей страницы.
	Rentals []Summary
	// Total — общее количество аренд.
	Total int
	// Page — номер текущей страницы начиная с единицы.
	Page int
	// PageSize — количество строк на странице.
	PageSize int
}

// Service реализует пользовательские сценарии создания и просмотра аренд.
type Service struct {
	repository Repository
}

// NewService создаёт сервис аренды с обязательным repository.
func NewService(repository Repository) *Service {
	return &Service{repository: repository}
}

// AvailableModels группирует физические единицы, доступные на весь интервал,
// по модели. Черновики не резервируют оборудование и поэтому не уменьшают
// доступное количество.
func (s *Service) AvailableModels(ctx context.Context, interval Interval) ([]AvailableModel, error) {
	if err := interval.validate(); err != nil {
		return nil, err
	}

	items, err := s.repository.AvailableEquipment(ctx, interval)
	if err != nil {
		return nil, fmt.Errorf("load available rental equipment: %w", err)
	}
	return groupAvailableModels(items), nil
}

// CreateDraft создаёт черновик от имени активного оператора. Снимки состава
// формируются только из актуальных серверных данных; значения номера, типа,
// модели и тарифа из browser не принимаются.
func (s *Service) CreateDraft(
	ctx context.Context,
	actor user.User,
	clientID int64,
	interval Interval,
	selections []ModelSelection,
) (Rental, error) {
	if actor.ID <= 0 || actor.Role != user.RoleOperator || !actor.Active {
		return Rental{}, user.ErrAccessDenied
	}
	if clientID <= 0 {
		return Rental{}, ErrInvalidClientID
	}
	if err := interval.validate(); err != nil {
		return Rental{}, err
	}
	if err := validateSelections(selections); err != nil {
		return Rental{}, err
	}

	available, err := s.repository.AvailableEquipment(ctx, interval)
	if err != nil {
		return Rental{}, fmt.Errorf("load equipment for rental draft: %w", err)
	}
	selected, err := selectEquipment(available, selections)
	if err != nil {
		return Rental{}, err
	}

	draft, err := New(clientID, interval)
	if err != nil {
		return Rental{}, err
	}
	for _, item := range selected {
		if err := draft.AddItem(Item{
			EquipmentID:       item.ID,
			InventoryNumber:   item.InventoryNumber,
			Kind:              item.Kind,
			ModelCode:         item.ModelCode,
			HourlyRateKopecks: item.HourlyRateKopecks,
		}); err != nil {
			return Rental{}, fmt.Errorf("add equipment to rental draft: %w", err)
		}
	}

	created, err := s.repository.Create(ctx, draft)
	if err != nil {
		return Rental{}, fmt.Errorf("create rental draft: %w", err)
	}
	return created, nil
}

// Get возвращает аренду по положительному идентификатору.
func (s *Service) Get(ctx context.Context, id int64) (Rental, error) {
	if id <= 0 {
		return Rental{}, ErrRentalNotFound
	}
	value, err := s.repository.Get(ctx, id)
	if err != nil {
		return Rental{}, fmt.Errorf("get rental: %w", err)
	}
	return value, nil
}

// ListPage возвращает страницу аренд от новых записей к старым.
func (s *Service) ListPage(ctx context.Context, page, pageSize int) (Page, error) {
	if page <= 0 || !allowedPageSize(pageSize) {
		return Page{}, ErrInvalidPage
	}
	result, err := s.repository.ListPage(ctx, page, pageSize)
	if err != nil {
		return Page{}, fmt.Errorf("list rentals: %w", err)
	}
	return result, nil
}

// AllowedPageSizes возвращает копию списка допустимых размеров страницы.
func AllowedPageSizes() []int {
	return append([]int(nil), allowedPageSizes[:]...)
}

func allowedPageSize(value int) bool {
	for _, allowed := range allowedPageSizes {
		if value == allowed {
			return true
		}
	}
	return false
}

func validateSelections(selections []ModelSelection) error {
	seen := make(map[int64]struct{}, len(selections))
	for _, selection := range selections {
		if selection.ModelID <= 0 || selection.Quantity < 0 {
			return ErrInvalidModelSelection
		}
		if _, exists := seen[selection.ModelID]; exists {
			return ErrInvalidModelSelection
		}
		seen[selection.ModelID] = struct{}{}
	}
	return nil
}

func selectEquipment(items []equipment.Item, selections []ModelSelection) ([]equipment.Item, error) {
	byModel := make(map[int64][]equipment.Item)
	for _, item := range items {
		byModel[item.ModelID] = append(byModel[item.ModelID], item)
	}

	selected := make([]equipment.Item, 0)
	for _, selection := range selections {
		if selection.Quantity == 0 {
			continue
		}
		candidates, exists := byModel[selection.ModelID]
		if !exists {
			// Между показом формы и отправкой запроса последняя доступная единица
			// модели могла изменить состояние или попасть в другую аренду.
			return nil, ErrInsufficientEquipment
		}
		if selection.Quantity > len(candidates) {
			return nil, ErrInsufficientEquipment
		}
		selected = append(selected, candidates[:selection.Quantity]...)
	}
	return selected, nil
}

func groupAvailableModels(items []equipment.Item) []AvailableModel {
	byModel := make(map[int64]*AvailableModel)
	for _, item := range items {
		model, exists := byModel[item.ModelID]
		if !exists {
			model = &AvailableModel{
				ModelID: item.ModelID, Kind: item.Kind, ModelCode: item.ModelCode,
				HourlyRateKopecks: item.HourlyRateKopecks,
			}
			byModel[item.ModelID] = model
		}
		model.AvailableCount++
	}

	models := make([]AvailableModel, 0, len(byModel))
	for _, model := range byModel {
		models = append(models, *model)
	}
	sort.Slice(models, func(i, j int) bool {
		if models[i].Kind != models[j].Kind {
			return equipmentKindOrder(models[i].Kind) < equipmentKindOrder(models[j].Kind)
		}
		return models[i].ModelCode < models[j].ModelCode
	})
	return models
}

func equipmentKindOrder(kind equipment.Kind) int {
	switch kind {
	case equipment.KindSUPBoard:
		return 0
	case equipment.KindPaddle:
		return 1
	case equipment.KindLifeJacket:
		return 2
	default:
		return 3
	}
}
