package rental

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/Dyuzhovsergey/sup-rental/internal/equipment"
	"github.com/Dyuzhovsergey/sup-rental/internal/user"
)

const (
	// DefaultPageSize задаёт количество аренд на странице списка по умолчанию.
	DefaultPageSize = 5
	// MaxBulkSelection задаёт максимальное число аренд в одной массовой операции.
	MaxBulkSelection = 15
)

var (
	// ErrInvalidPage означает некорректный номер страницы или размер страницы.
	ErrInvalidPage = errors.New("invalid rental page")
	// ErrInvalidModelSelection означает некорректный идентификатор модели,
	// повтор модели или отрицательное количество в запросе на создание аренды.
	ErrInvalidModelSelection = errors.New("invalid rental model selection")
	// ErrInsufficientEquipment означает, что доступных единиц выбранной модели
	// меньше запрошенного количества.
	ErrInsufficientEquipment = errors.New("insufficient available equipment")
	// ErrEquipmentUnavailable означает, что физическую единицу состава нельзя
	// выдать или принять обратно в её текущем состоянии.
	ErrEquipmentUnavailable = errors.New("rental equipment is unavailable for lifecycle operation")
	// ErrInvalidBulkSelection означает пустой, слишком большой или содержащий
	// некорректные либо повторяющиеся ID набор аренд.
	ErrInvalidBulkSelection = errors.New("invalid bulk rental selection")
	// ErrIssueConflict означает, что фактический период выдачи пересекается с
	// резервом другой подтверждённой или активной аренды.
	ErrIssueConflict = errors.New("rental issue conflicts with another reservation")
	// ErrInvalidReplacement означает некорректную или устаревшую замену
	// конфликтующей физической единицы.
	ErrInvalidReplacement = errors.New("invalid rental equipment replacement")
)

var allowedPageSizes = [...]int{5, 10, 15}

// Repository определяет операции хранения, необходимые пользовательским
// сценариям аренды.
type Repository interface {
	CreateConfirmed(
		ctx context.Context,
		actor user.User,
		clientID int64,
		interval Interval,
		selections []ModelSelection,
	) (Rental, error)
	PreviewIssue(ctx context.Context, id int64, issuedAt time.Time) (IssuePreview, error)
	Issue(ctx context.Context, actor user.User, id int64, issuedAt time.Time) (Rental, error)
	IssueWithReplacements(ctx context.Context, actor user.User, id int64, issuedAt time.Time, replacements []EquipmentReplacement) (Rental, error)
	IssueMany(ctx context.Context, actor user.User, ids []int64, issuedAt time.Time) ([]Rental, error)
	Cancel(ctx context.Context, actor user.User, id int64) (Rental, error)
	CancelMany(ctx context.Context, actor user.User, ids []int64) ([]Rental, error)
	Complete(ctx context.Context, actor user.User, id int64, returnedAt time.Time, overdueTotalKopecks int64) (Rental, error)
	CompleteMany(ctx context.Context, actor user.User, ids []int64, returnedAt time.Time) ([]Rental, error)
	Get(ctx context.Context, id int64) (Rental, error)
	PaymentSummary(ctx context.Context, id int64) (PaymentSummary, error)
	ListPage(ctx context.Context, statuses []Status, page, pageSize int) (Page, error)
	Monitoring(ctx context.Context, query MonitoringQuery) (MonitoringData, error)
	AvailableEquipment(ctx context.Context, interval Interval) ([]equipment.Item, error)
}

// PaymentSummary возвращает зафиксированные оплаты и возврат одной аренды.
func (s *Service) PaymentSummary(ctx context.Context, id int64) (PaymentSummary, error) {
	if id <= 0 {
		return PaymentSummary{}, ErrInvalidRentalID
	}
	summary, err := s.repository.PaymentSummary(ctx, id)
	if err != nil {
		return PaymentSummary{}, fmt.Errorf("load rental payment summary: %w", err)
	}
	return summary, nil
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
	// IssuedAt — фактическое время выдачи для активной или завершённой аренды.
	IssuedAt *time.Time
	// ExpectedReturnAt — ожидаемое время возврата, зафиксированное при выдаче.
	ExpectedReturnAt *time.Time
	// ReturnedAt — фактическое время возврата завершённой аренды.
	ReturnedAt *time.Time
	// ItemCount — число физических единиц в составе.
	ItemCount int
	// PlannedTotalKopecks — предварительная стоимость в копейках.
	PlannedTotalKopecks int64
	// FinalTotalKopecks — сохранённая итоговая стоимость завершённой аренды.
	// Для остальных состояний значение отсутствует.
	FinalTotalKopecks *int64
	// WaitingForIssue означает, что плановое начало подтверждённой аренды уже
	// наступило, но оборудование ещё не выдано.
	WaitingForIssue bool
}

// SettlementPreview содержит расчёт возврата на единый серверный момент времени.
type SettlementPreview struct {
	// Rental — активная аренда, для которой выполнен расчёт.
	Rental Rental
	// ReturnedAt — момент, использованный как предполагаемое время возврата.
	ReturnedAt time.Time
	// Settlement — предварительный окончательный расчёт.
	Settlement Settlement
}

// EquipmentReplacement задаёт выбранную оператором замену конфликтующей
// физической единицы на другую единицу той же модели.
type EquipmentReplacement struct {
	// OriginalEquipmentID — ID единицы из исходного состава аренды.
	OriginalEquipmentID int64
	// ReplacementEquipmentID — ID выбранной свободной единицы той же модели.
	ReplacementEquipmentID int64
}

// IssueConflict описывает конфликт одной единицы состава и допустимые замены.
type IssueConflict struct {
	// Item — сохранённая позиция аренды, которую нельзя выдать на фактический период.
	Item Item
	// Replacements — доступные физические единицы той же модели.
	Replacements []equipment.Item
}

// IssuePreview содержит фактический период предполагаемой выдачи и конфликты.
type IssuePreview struct {
	// Rental — подтверждённая аренда.
	Rental Rental
	// IssuedAt — единый момент предполагаемой фактической выдачи.
	IssuedAt time.Time
	// ExpectedReturnAt — окончание выбранной длительности от IssuedAt.
	ExpectedReturnAt time.Time
	// Conflicts — позиции, требующие ручной замены перед выдачей.
	Conflicts []IssueConflict
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
	now        func() time.Time
}

// NewService создаёт сервис аренды с обязательным repository.
func NewService(repository Repository) *Service {
	return &Service{repository: repository, now: time.Now}
}

// AvailableModels группирует физические единицы, доступные на весь интервал,
// по модели.
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

// CreateConfirmed создаёт подтверждённую аренду от имени активного оператора.
// Repository повторно проверяет доступность, выбирает физические единицы и
// сохраняет аренду вместе с обязательным audit event одной транзакцией.
func (s *Service) CreateConfirmed(
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

	created, err := s.repository.CreateConfirmed(ctx, actor, clientID, interval, selections)
	if err != nil {
		return Rental{}, fmt.Errorf("create confirmed rental: %w", err)
	}
	return created, nil
}

// Issue выдаёт весь состав подтверждённой аренды от имени активного оператора.
// Repository атомарно меняет аренду, оборудование и обязательный audit event.
func (s *Service) PreviewIssue(ctx context.Context, id int64) (IssuePreview, error) {
	if id <= 0 {
		return IssuePreview{}, ErrRentalNotFound
	}
	preview, err := s.repository.PreviewIssue(ctx, id, s.now().UTC().Truncate(time.Microsecond))
	if err != nil {
		return IssuePreview{}, fmt.Errorf("preview rental issue: %w", err)
	}
	return preview, nil
}

func (s *Service) Issue(ctx context.Context, actor user.User, id int64) (Rental, error) {
	return s.IssueWithReplacements(ctx, actor, id, nil)
}

// IssueWithReplacements выдаёт весь состав подтверждённой аренды от имени
// активного оператора и применяет явные замены конфликтующих единиц.
func (s *Service) IssueWithReplacements(
	ctx context.Context,
	actor user.User,
	id int64,
	replacements []EquipmentReplacement,
) (Rental, error) {
	if actor.ID <= 0 || actor.Role != user.RoleOperator || !actor.Active {
		return Rental{}, user.ErrAccessDenied
	}
	if id <= 0 {
		return Rental{}, ErrRentalNotFound
	}

	if err := validateReplacements(replacements); err != nil {
		return Rental{}, err
	}
	issued, err := s.repository.IssueWithReplacements(
		ctx, actor, id, s.now().UTC().Truncate(time.Microsecond), replacements,
	)
	if err != nil {
		return Rental{}, fmt.Errorf("issue rental: %w", err)
	}
	return issued, nil
}

func validateReplacements(values []EquipmentReplacement) error {
	originals := make(map[int64]struct{}, len(values))
	targets := make(map[int64]struct{}, len(values))
	for _, value := range values {
		if value.OriginalEquipmentID <= 0 || value.ReplacementEquipmentID <= 0 ||
			value.OriginalEquipmentID == value.ReplacementEquipmentID {
			return ErrInvalidReplacement
		}
		if _, exists := originals[value.OriginalEquipmentID]; exists {
			return ErrInvalidReplacement
		}
		if _, exists := targets[value.ReplacementEquipmentID]; exists {
			return ErrInvalidReplacement
		}
		originals[value.OriginalEquipmentID] = struct{}{}
		targets[value.ReplacementEquipmentID] = struct{}{}
	}
	return nil
}

// IssueMany атомарно выдаёт выбранные подтверждённые аренды от имени активного
// оператора. Все аренды используют один момент фактической выдачи.
func (s *Service) IssueMany(ctx context.Context, actor user.User, ids []int64) ([]Rental, error) {
	if actor.ID <= 0 || actor.Role != user.RoleOperator || !actor.Active {
		return nil, user.ErrAccessDenied
	}
	validated, err := validateBulkSelection(ids)
	if err != nil {
		return nil, err
	}

	issued, err := s.repository.IssueMany(ctx, actor, validated, s.now().UTC())
	if err != nil {
		return nil, fmt.Errorf("issue rental selection: %w", err)
	}
	return issued, nil
}

// Cancel отменяет подтверждённую аренду от имени активного оператора.
// Repository сохраняет состояние и обязательный audit event одной транзакцией.
func (s *Service) Cancel(ctx context.Context, actor user.User, id int64) (Rental, error) {
	if actor.ID <= 0 || actor.Role != user.RoleOperator || !actor.Active {
		return Rental{}, user.ErrAccessDenied
	}
	if id <= 0 {
		return Rental{}, ErrRentalNotFound
	}

	cancelled, err := s.repository.Cancel(ctx, actor, id)
	if err != nil {
		return Rental{}, fmt.Errorf("cancel rental: %w", err)
	}
	return cancelled, nil
}

// CancelMany атомарно отменяет выбранные подтверждённые аренды от имени
// активного оператора.
func (s *Service) CancelMany(ctx context.Context, actor user.User, ids []int64) ([]Rental, error) {
	if actor.ID <= 0 || actor.Role != user.RoleOperator || !actor.Active {
		return nil, user.ErrAccessDenied
	}
	validated, err := validateBulkSelection(ids)
	if err != nil {
		return nil, err
	}

	cancelled, err := s.repository.CancelMany(ctx, actor, validated)
	if err != nil {
		return nil, fmt.Errorf("cancel rental selection: %w", err)
	}
	return cancelled, nil
}

// Complete фиксирует полный возврат активной аренды от имени активного
// оператора с выбранной доплатой за просрочку. Repository атомарно завершает
// аренду, освобождает оборудование и сохраняет обязательный audit event.
func (s *Service) Complete(
	ctx context.Context,
	actor user.User,
	id int64,
	overdueTotalKopecks int64,
) (Rental, error) {
	if actor.ID <= 0 || actor.Role != user.RoleOperator || !actor.Active {
		return Rental{}, user.ErrAccessDenied
	}
	if id <= 0 {
		return Rental{}, ErrRentalNotFound
	}

	completed, err := s.repository.Complete(
		ctx, actor, id, s.now().UTC(), overdueTotalKopecks,
	)
	if err != nil {
		return Rental{}, fmt.Errorf("complete rental: %w", err)
	}
	return completed, nil
}

// CompleteMany атомарно фиксирует полный возврат выбранных активных аренд от
// имени активного оператора. Все аренды используют один момент возврата.
func (s *Service) CompleteMany(ctx context.Context, actor user.User, ids []int64) ([]Rental, error) {
	if actor.ID <= 0 || actor.Role != user.RoleOperator || !actor.Active {
		return nil, user.ErrAccessDenied
	}
	validated, err := validateBulkSelection(ids)
	if err != nil {
		return nil, err
	}

	completed, err := s.repository.CompleteMany(ctx, actor, validated, s.now().UTC())
	if err != nil {
		return nil, fmt.Errorf("complete rental selection: %w", err)
	}
	return completed, nil
}

// PreviewSettlement рассчитывает итог активной аренды на текущий серверный момент,
// не изменяя состояние и данные в постоянном хранилище.
func (s *Service) PreviewSettlement(ctx context.Context, id int64) (SettlementPreview, error) {
	if id <= 0 {
		return SettlementPreview{}, ErrRentalNotFound
	}
	value, err := s.repository.Get(ctx, id)
	if err != nil {
		return SettlementPreview{}, fmt.Errorf("get rental for settlement preview: %w", err)
	}
	returnedAt := s.now().UTC()
	settlement, err := value.SettlementAt(returnedAt)
	if err != nil {
		return SettlementPreview{}, err
	}
	return SettlementPreview{Rental: value, ReturnedAt: returnedAt, Settlement: settlement}, nil
}

// PreviewSettlements рассчитывает итоги выбранных активных аренд на один
// серверный момент времени без изменения хранилища.
func (s *Service) PreviewSettlements(ctx context.Context, ids []int64) ([]SettlementPreview, error) {
	validated, err := validateBulkSelection(ids)
	if err != nil {
		return nil, err
	}
	returnedAt := s.now().UTC()
	previews := make([]SettlementPreview, 0, len(validated))
	for _, id := range validated {
		value, err := s.repository.Get(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("get rental %d for settlement preview: %w", id, err)
		}
		settlement, err := value.SettlementAt(returnedAt)
		if err != nil {
			return nil, err
		}
		previews = append(previews, SettlementPreview{
			Rental: value, ReturnedAt: returnedAt, Settlement: settlement,
		})
	}
	return previews, nil
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

// ListPage возвращает страницу аренд выбранных состояний от новых записей к старым.
func (s *Service) ListPage(ctx context.Context, statuses []Status, page, pageSize int) (Page, error) {
	if page <= 0 || !allowedPageSize(pageSize) || !validStatusFilter(statuses) {
		return Page{}, ErrInvalidPage
	}
	result, err := s.repository.ListPage(ctx, statuses, page, pageSize)
	if err != nil {
		return Page{}, fmt.Errorf("list rentals: %w", err)
	}
	now := s.now()
	for index := range result.Rentals {
		value := &result.Rentals[index]
		value.WaitingForIssue = value.Status == StatusConfirmed && !now.Before(value.Interval.Start())
	}
	return result, nil
}

func validStatusFilter(statuses []Status) bool {
	if len(statuses) == 0 {
		return false
	}
	seen := make(map[Status]struct{}, len(statuses))
	for _, status := range statuses {
		if !status.Valid() {
			return false
		}
		if _, exists := seen[status]; exists {
			return false
		}
		seen[status] = struct{}{}
	}
	return true
}

func validateBulkSelection(ids []int64) ([]int64, error) {
	if len(ids) == 0 || len(ids) > MaxBulkSelection {
		return nil, ErrInvalidBulkSelection
	}
	validated := append([]int64(nil), ids...)
	seen := make(map[int64]struct{}, len(validated))
	for _, id := range validated {
		if id <= 0 {
			return nil, ErrInvalidBulkSelection
		}
		if _, exists := seen[id]; exists {
			return nil, ErrInvalidBulkSelection
		}
		seen[id] = struct{}{}
	}
	return validated, nil
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
	selectedCount := 0
	for _, selection := range selections {
		if selection.ModelID <= 0 || selection.Quantity < 0 {
			return ErrInvalidModelSelection
		}
		if _, exists := seen[selection.ModelID]; exists {
			return ErrInvalidModelSelection
		}
		seen[selection.ModelID] = struct{}{}
		selectedCount += selection.Quantity
	}
	if selectedCount == 0 {
		return ErrRentalItemsRequired
	}
	return nil
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
