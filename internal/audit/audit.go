// Package audit предоставляет read-only сценарии просмотра постоянного журнала действий.
package audit

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Dyuzhovsergey/sup-rental/internal/user"
)

// PageSize задаёт фиксированное количество событий на странице журнала.
const PageSize = 50

// Category объединяет события одного функционального раздела.
type Category string

const (
	CategoryAll       Category = ""
	CategoryAuth      Category = "auth"
	CategoryUsers     Category = "users"
	CategoryEquipment Category = "equipment"
	// CategoryClients ограничивает журнал событиями работы с клиентами.
	CategoryClients Category = "clients"
	// CategoryRentals ограничивает журнал событиями работы с арендами.
	CategoryRentals Category = "rentals"
	ResultAll                = ""
	ResultSuccess            = "success"
	ResultFailure            = "failure"
)

var (
	// ErrInvalidFilter означает, что передан неподдерживаемый фильтр журнала.
	ErrInvalidFilter = errors.New("invalid audit filter")
	// ErrInvalidPage означает, что номер страницы некорректен.
	ErrInvalidPage = errors.New("invalid audit page")
)

// Event содержит безопасно извлечённую запись постоянного журнала.
type Event struct {
	// ID — постоянный идентификатор события.
	ID int64
	// OccurredAt — время записи события.
	OccurredAt time.Time
	// ActorUserID — ID исполнителя либо nil для события без известного пользователя.
	ActorUserID *int64
	// ActorLogin — сохранённый на момент события login исполнителя.
	ActorLogin *string
	// ActorRole — сохранённая на момент события роль исполнителя.
	ActorRole *string
	// Action — стабильный машинный код действия.
	Action string
	// TargetType — тип объекта действия.
	TargetType string
	// TargetID — ID объекта либо nil, если объект не существует в таблице.
	TargetID *int64
	// TargetLabel — безопасная понятная метка объекта.
	TargetLabel string
	// Result — результат выполнения действия.
	Result string
	// Details — ограниченные структурированные данные события в JSON.
	Details []byte
}

// Filter задаёт проверенные условия и страницу выборки журнала.
type Filter struct {
	// Category ограничивает события функциональным разделом.
	Category Category
	// Result ограничивает события результатом выполнения.
	Result string
	// Actor выполняет частичный поиск по login исполнителя.
	Actor string
	// Target выполняет частичный поиск по метке объекта.
	Target string
	// From включает события, произошедшие не раньше указанного времени.
	From *time.Time
	// To исключает события, произошедшие в указанное время или позже.
	To *time.Time
	// Page задаёт номер страницы начиная с единицы.
	Page int
}

// Page содержит одну страницу событий и общее количество найденных записей.
type Page struct {
	// Events содержит события текущей страницы.
	Events []Event
	// Total — общее число событий, соответствующих фильтру.
	Total int
	// Page — номер текущей страницы.
	Page int
}

// Repository определяет read-only выборку событий аудита.
type Repository interface {
	List(ctx context.Context, filter Filter) (Page, error)
}

// Service проверяет права и предоставляет журнал HTTP-слою.
type Service struct{ repository Repository }

// NewService создаёт сервис с обязательным repository.
func NewService(repository Repository) *Service { return &Service{repository: repository} }

// NewFilter нормализует текстовые фильтры и проверяет allowlist значений.
func NewFilter(category, result, actor, target string, from, to *time.Time, page int) (Filter, error) {
	filter := Filter{
		Category: Category(strings.TrimSpace(category)),
		Result:   strings.TrimSpace(result),
		Actor:    strings.TrimSpace(actor),
		Target:   strings.TrimSpace(target),
		From:     from,
		To:       to,
		Page:     page,
	}
	if filter.Category != CategoryAll && filter.Category != CategoryAuth &&
		filter.Category != CategoryUsers && filter.Category != CategoryEquipment &&
		filter.Category != CategoryClients && filter.Category != CategoryRentals {
		return Filter{}, ErrInvalidFilter
	}
	if filter.Result != ResultAll && filter.Result != ResultSuccess && filter.Result != ResultFailure {
		return Filter{}, ErrInvalidFilter
	}
	if page <= 0 {
		return Filter{}, ErrInvalidPage
	}
	if from != nil && to != nil && from.After(*to) {
		return Filter{}, ErrInvalidFilter
	}
	return filter, nil
}

// List возвращает страницу журнала только активному администратору.
func (s *Service) List(ctx context.Context, actor user.User, filter Filter) (Page, error) {
	if actor.ID <= 0 || actor.Role != user.RoleAdmin || !actor.Active {
		return Page{}, user.ErrAccessDenied
	}
	page, err := s.repository.List(ctx, filter)
	if err != nil {
		return Page{}, fmt.Errorf("list audit events: %w", err)
	}
	return page, nil
}
