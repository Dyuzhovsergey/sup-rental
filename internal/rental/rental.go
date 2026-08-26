// Package rental содержит доменную модель аренды и правила её жизненного цикла.
package rental

import (
	"errors"
	"fmt"
	"time"
)

// Status определяет состояние аренды в её жизненном цикле.
type Status string

const (
	// StatusConfirmed обозначает подтверждённую аренду с зарезервированным оборудованием.
	StatusConfirmed Status = "confirmed"
	// StatusActive обозначает аренду, по которой оборудование выдано клиенту.
	StatusActive Status = "active"
	// StatusCompleted обозначает завершённую аренду после возврата оборудования.
	StatusCompleted Status = "completed"
	// StatusCancelled обозначает отменённую до выдачи аренду.
	StatusCancelled Status = "cancelled"
)

var (
	// ErrInvalidRentalID означает, что сохранённая аренда не имеет положительного ID.
	ErrInvalidRentalID = errors.New("rental ID must be positive")
	// ErrInvalidClientID означает, что аренда не связана с существующим клиентом.
	ErrInvalidClientID = errors.New("rental client ID must be positive")
	// ErrInvalidStatus означает, что передано неизвестное состояние аренды.
	ErrInvalidStatus = errors.New("invalid rental status")
	// ErrStatusTransitionNotAllowed означает, что переход между состояниями запрещён.
	ErrStatusTransitionNotAllowed = errors.New("rental status transition is not allowed")
	// ErrRentalItemsRequired означает попытку создать аренду без оборудования.
	ErrRentalItemsRequired = errors.New("confirmed rental must contain at least one item")
	// ErrRentalNotFound означает, что аренда не найдена в постоянном хранилище.
	ErrRentalNotFound = errors.New("rental not found")
	// ErrIssuedAtRequired означает, что активная или завершённая аренда не имеет
	// фактического времени выдачи.
	ErrIssuedAtRequired = errors.New("rental issued time is required")
	// ErrUnexpectedIssuedAt означает, что время выдачи задано до фактической выдачи.
	ErrUnexpectedIssuedAt = errors.New("rental issued time is not allowed")
	// ErrExpectedReturnAtRequired означает отсутствие ожидаемого времени возврата
	// у активной или завершённой аренды.
	ErrExpectedReturnAtRequired = errors.New("rental expected return time is required")
	// ErrUnexpectedExpectedReturnAt означает ожидаемый возврат до выдачи аренды.
	ErrUnexpectedExpectedReturnAt = errors.New("rental expected return time is not allowed")
	// ErrReturnedAtRequired означает, что завершённая аренда не имеет
	// фактического времени возврата.
	ErrReturnedAtRequired = errors.New("rental returned time is required")
	// ErrUnexpectedReturnedAt означает, что время возврата задано до завершения аренды.
	ErrUnexpectedReturnedAt = errors.New("rental returned time is not allowed")
	// ErrReturnedBeforeIssued означает, что возврат указан раньше фактической выдачи.
	ErrReturnedBeforeIssued = errors.New("rental returned time is before issued time")
)

// Valid сообщает, является ли состояние аренды поддерживаемым.
func (s Status) Valid() bool {
	switch s {
	case StatusConfirmed, StatusActive, StatusCompleted, StatusCancelled:
		return true
	default:
		return false
	}
}

// CanTransitionTo сообщает, разрешён ли переход из текущего состояния в target.
func (s Status) CanTransitionTo(target Status) bool {
	switch s {
	case StatusConfirmed:
		return target == StatusActive || target == StatusCancelled
	case StatusActive:
		return target == StatusCompleted
	default:
		return false
	}
}

// Rental представляет одну аренду клиента на планируемый временной интервал.
// Нулевой ID означает, что аренда ещё не сохранена в постоянном хранилище.
type Rental struct {
	// ID — внутренний идентификатор аренды.
	ID int64
	// ClientID — идентификатор клиента, для которого оформляется аренда.
	ClientID int64
	// Interval — планируемый полуоткрытый интервал аренды.
	Interval Interval
	// Status — текущее состояние аренды.
	Status           Status
	items            []Item
	issuedAt         *time.Time
	expectedReturnAt *time.Time
	returnedAt       *time.Time
	settlement       *Settlement
}

// New создаёт ещё не сохранённую подтверждённую аренду с неизменяемым составом.
// Переданный состав копируется и должен содержать хотя бы одну физическую единицу.
func New(clientID int64, interval Interval, items []Item) (Rental, error) {
	if clientID <= 0 {
		return Rental{}, ErrInvalidClientID
	}
	if err := interval.validate(); err != nil {
		return Rental{}, err
	}
	validatedItems, err := validateItems(items)
	if err != nil {
		return Rental{}, err
	}

	return Rental{
		ClientID: clientID,
		Interval: interval,
		Status:   StatusConfirmed,
		items:    validatedItems,
	}, nil
}

// Restore проверяет данные из постоянного хранилища и восстанавливает аренду.
// Переданный состав копируется, поэтому последующие изменения исходного slice
// не влияют на возвращённый объект.
func Restore(
	id int64,
	clientID int64,
	interval Interval,
	status Status,
	issuedAt *time.Time,
	returnedAt *time.Time,
	items []Item,
) (Rental, error) {
	var expectedReturnAt *time.Time
	if status == StatusActive || status == StatusCompleted {
		value := interval.End()
		expectedReturnAt = &value
	}
	return RestoreWithExpectedReturn(
		id, clientID, interval, status, issuedAt, expectedReturnAt, returnedAt, items,
	)
}

// RestoreWithExpectedReturn проверяет и восстанавливает аренду с отдельно
// сохранённым ожидаемым временем возврата. Функция используется хранилищем для
// различения планового бронирования и фактического периода после выдачи.
func RestoreWithExpectedReturn(
	id int64,
	clientID int64,
	interval Interval,
	status Status,
	issuedAt *time.Time,
	expectedReturnAt *time.Time,
	returnedAt *time.Time,
	items []Item,
) (Rental, error) {
	if id <= 0 {
		return Rental{}, ErrInvalidRentalID
	}
	if clientID <= 0 {
		return Rental{}, ErrInvalidClientID
	}
	if err := interval.validate(); err != nil {
		return Rental{}, err
	}
	if !status.Valid() {
		return Rental{}, ErrInvalidStatus
	}
	validatedIssuedAt, validatedExpectedReturnAt, validatedReturnedAt, err := validateLifecycleTimes(
		status, issuedAt, expectedReturnAt, returnedAt,
	)
	if err != nil {
		return Rental{}, err
	}
	restoredItems, err := validateItems(items)
	if err != nil {
		return Rental{}, err
	}

	return Rental{
		ID:               id,
		ClientID:         clientID,
		Interval:         interval,
		Status:           status,
		items:            restoredItems,
		issuedAt:         validatedIssuedAt,
		expectedReturnAt: validatedExpectedReturnAt,
		returnedAt:       validatedReturnedAt,
	}, nil
}

// Issue фиксирует фактическую выдачу и переводит подтверждённую аренду в active.
// Плановое время не ограничивает момент выдачи: раннее или позднее действие
// сохраняется как отдельный фактический timestamp.
func (r *Rental) Issue(issuedAt time.Time) error {
	if issuedAt.IsZero() {
		return ErrIssuedAtRequired
	}
	if !r.Status.Valid() {
		return ErrInvalidStatus
	}
	if !r.Status.CanTransitionTo(StatusActive) {
		return fmt.Errorf("%w: %s -> %s", ErrStatusTransitionNotAllowed, r.Status, StatusActive)
	}
	r.Status = StatusActive
	issuedValue := issuedAt
	expectedReturnValue := issuedAt.Add(r.Interval.End().Sub(r.Interval.Start()))
	r.issuedAt = &issuedValue
	r.expectedReturnAt = &expectedReturnValue
	return nil
}

// Cancel отменяет подтверждённую аренду до фактической выдачи оборудования.
// Отменённая аренда остаётся в истории и больше не резервирует оборудование.
func (r *Rental) Cancel() error {
	return r.ChangeStatus(StatusCancelled)
}

// Complete фиксирует фактический возврат всего состава и переводит активную
// аренду в completed. Плановый интервал при этом не изменяется.
func (r *Rental) Complete(returnedAt time.Time) error {
	return r.complete(returnedAt, nil)
}

// CompleteWithOverdueTotal фиксирует возврат с вручную заданной доплатой за
// просрочку. Фактическая просрочка и число расчётных слотов сохраняются без
// изменения, а итог использует переданную оператором сумму.
func (r *Rental) CompleteWithOverdueTotal(returnedAt time.Time, overdueTotalKopecks int64) error {
	return r.complete(returnedAt, &overdueTotalKopecks)
}

func (r *Rental) complete(returnedAt time.Time, overdueTotalKopecks *int64) error {
	if returnedAt.IsZero() {
		return ErrReturnedAtRequired
	}
	if !r.Status.Valid() {
		return ErrInvalidStatus
	}
	if !r.Status.CanTransitionTo(StatusCompleted) {
		return fmt.Errorf("%w: %s -> %s", ErrStatusTransitionNotAllowed, r.Status, StatusCompleted)
	}
	if r.issuedAt == nil || r.issuedAt.IsZero() {
		return ErrIssuedAtRequired
	}
	if returnedAt.Before(*r.issuedAt) {
		return ErrReturnedBeforeIssued
	}
	settlement, err := r.calculateSettlement(returnedAt)
	if err != nil {
		return err
	}
	if overdueTotalKopecks != nil {
		settlement, err = settlement.WithOverdueTotal(*overdueTotalKopecks)
		if err != nil {
			return err
		}
	}
	r.Status = StatusCompleted
	value := returnedAt
	r.returnedAt = &value
	r.settlement = &settlement
	return nil
}

// ChangeStatus переводит аренду в target, если такой переход разрешён.
// При ошибке исходное состояние аренды не изменяется.
func (r *Rental) ChangeStatus(target Status) error {
	if !r.Status.Valid() || !target.Valid() {
		return ErrInvalidStatus
	}
	if !r.Status.CanTransitionTo(target) {
		return fmt.Errorf("%w: %s -> %s", ErrStatusTransitionNotAllowed, r.Status, target)
	}
	if target == StatusActive {
		return ErrIssuedAtRequired
	}
	if target == StatusCompleted {
		return ErrReturnedAtRequired
	}
	r.Status = target
	return nil
}

// IssuedAt возвращает фактическое время выдачи и признак его наличия.
func (r Rental) IssuedAt() (time.Time, bool) {
	if r.issuedAt == nil {
		return time.Time{}, false
	}
	return *r.issuedAt, true
}

// ExpectedReturnAt возвращает ожидаемое время возврата, рассчитанное при
// фактической выдаче, и признак его наличия.
func (r Rental) ExpectedReturnAt() (time.Time, bool) {
	if r.expectedReturnAt == nil {
		return time.Time{}, false
	}
	return *r.expectedReturnAt, true
}

// ReturnedAt возвращает фактическое время полного возврата и признак его наличия.
func (r Rental) ReturnedAt() (time.Time, bool) {
	if r.returnedAt == nil {
		return time.Time{}, false
	}
	return *r.returnedAt, true
}

// Items возвращает независимую копию состава аренды в порядке добавления.
func (r Rental) Items() []Item {
	return append([]Item(nil), r.items...)
}

// ItemCount возвращает количество физических единиц в составе аренды.
func (r Rental) ItemCount() int {
	return len(r.items)
}

func validateItems(items []Item) ([]Item, error) {
	if len(items) == 0 {
		return nil, ErrRentalItemsRequired
	}

	validated := make([]Item, 0, len(items))
	seenEquipment := make(map[int64]struct{}, len(items))
	for _, item := range items {
		if err := item.validate(); err != nil {
			return nil, err
		}
		if _, exists := seenEquipment[item.EquipmentID]; exists {
			return nil, ErrEquipmentAlreadyAdded
		}
		seenEquipment[item.EquipmentID] = struct{}{}
		validated = append(validated, item)
	}
	return validated, nil
}

func validateLifecycleTimes(
	status Status,
	issuedAt, expectedReturnAt, returnedAt *time.Time,
) (*time.Time, *time.Time, *time.Time, error) {
	requiresIssuedAt := status == StatusActive || status == StatusCompleted
	if requiresIssuedAt {
		if issuedAt == nil || issuedAt.IsZero() {
			return nil, nil, nil, ErrIssuedAtRequired
		}
	} else if issuedAt != nil {
		return nil, nil, nil, ErrUnexpectedIssuedAt
	}

	if requiresIssuedAt {
		if expectedReturnAt == nil || expectedReturnAt.IsZero() {
			return nil, nil, nil, ErrExpectedReturnAtRequired
		}
	} else if expectedReturnAt != nil {
		return nil, nil, nil, ErrUnexpectedExpectedReturnAt
	}

	if status == StatusCompleted {
		if returnedAt == nil || returnedAt.IsZero() {
			return nil, nil, nil, ErrReturnedAtRequired
		}
		if returnedAt.Before(*issuedAt) {
			return nil, nil, nil, ErrReturnedBeforeIssued
		}
	} else if returnedAt != nil {
		return nil, nil, nil, ErrUnexpectedReturnedAt
	}

	var issuedCopy, expectedReturnCopy, returnedCopy *time.Time
	if issuedAt != nil {
		value := *issuedAt
		issuedCopy = &value
	}
	if expectedReturnAt != nil {
		value := *expectedReturnAt
		expectedReturnCopy = &value
	}
	if returnedAt != nil {
		value := *returnedAt
		returnedCopy = &value
	}
	return issuedCopy, expectedReturnCopy, returnedCopy, nil
}
