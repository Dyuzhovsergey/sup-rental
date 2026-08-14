// Package rental содержит доменную модель аренды и правила её жизненного цикла.
package rental

import (
	"errors"
	"fmt"
)

// Status определяет состояние аренды в её жизненном цикле.
type Status string

const (
	// StatusDraft обозначает редактируемый черновик аренды.
	StatusDraft Status = "draft"
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
	// ErrRentalItemsRequired означает попытку подтвердить аренду без оборудования.
	ErrRentalItemsRequired = errors.New("rental must contain at least one item before confirmation")
	// ErrRentalNotFound означает, что аренда не найдена в постоянном хранилище.
	ErrRentalNotFound = errors.New("rental not found")
	// ErrRentalNotEditable означает попытку сохранить изменения не-черновика.
	ErrRentalNotEditable = errors.New("only draft rental can be edited")
	// ErrRentalAlreadyPersisted означает попытку повторно создать аренду с ID.
	ErrRentalAlreadyPersisted = errors.New("rental is already persisted")
)

// Valid сообщает, является ли состояние аренды поддерживаемым.
func (s Status) Valid() bool {
	switch s {
	case StatusDraft, StatusConfirmed, StatusActive, StatusCompleted, StatusCancelled:
		return true
	default:
		return false
	}
}

// CanTransitionTo сообщает, разрешён ли переход из текущего состояния в target.
func (s Status) CanTransitionTo(target Status) bool {
	switch s {
	case StatusDraft:
		return target == StatusConfirmed || target == StatusCancelled
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
	Status Status
	items  []Item
}

// New создаёт ещё не сохранённую аренду в состоянии draft.
func New(clientID int64, interval Interval) (Rental, error) {
	if clientID <= 0 {
		return Rental{}, ErrInvalidClientID
	}
	if err := interval.validate(); err != nil {
		return Rental{}, err
	}

	return Rental{
		ClientID: clientID,
		Interval: interval,
		Status:   StatusDraft,
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
	if status != StatusDraft && status != StatusCancelled && len(items) == 0 {
		return Rental{}, ErrRentalItemsRequired
	}

	restoredItems := make([]Item, 0, len(items))
	seenEquipment := make(map[int64]struct{}, len(items))
	for _, item := range items {
		if err := item.validate(); err != nil {
			return Rental{}, err
		}
		if _, exists := seenEquipment[item.EquipmentID]; exists {
			return Rental{}, ErrEquipmentAlreadyAdded
		}
		seenEquipment[item.EquipmentID] = struct{}{}
		restoredItems = append(restoredItems, item)
	}

	return Rental{
		ID:       id,
		ClientID: clientID,
		Interval: interval,
		Status:   status,
		items:    restoredItems,
	}, nil
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
	if target == StatusConfirmed && len(r.items) == 0 {
		return ErrRentalItemsRequired
	}

	r.Status = target
	return nil
}

// AddItem добавляет снимок физической единицы в состав черновика.
func (r *Rental) AddItem(item Item) error {
	if r.Status != StatusDraft {
		return ErrRentalCompositionLocked
	}
	if err := item.validate(); err != nil {
		return err
	}
	for _, existing := range r.items {
		if existing.EquipmentID == item.EquipmentID {
			return ErrEquipmentAlreadyAdded
		}
	}

	updated := make([]Item, len(r.items)+1)
	copy(updated, r.items)
	updated[len(r.items)] = item
	r.items = updated
	return nil
}

// RemoveItem удаляет физическую единицу из состава черновика по идентификатору.
func (r *Rental) RemoveItem(equipmentID int64) error {
	if r.Status != StatusDraft {
		return ErrRentalCompositionLocked
	}
	if equipmentID <= 0 {
		return ErrInvalidEquipmentID
	}
	for index, item := range r.items {
		if item.EquipmentID == equipmentID {
			updated := make([]Item, 0, len(r.items)-1)
			updated = append(updated, r.items[:index]...)
			updated = append(updated, r.items[index+1:]...)
			r.items = updated
			return nil
		}
	}

	return ErrRentalItemNotFound
}

// Items возвращает независимую копию состава аренды в порядке добавления.
func (r Rental) Items() []Item {
	return append([]Item(nil), r.items...)
}

// ItemCount возвращает количество физических единиц в составе аренды.
func (r Rental) ItemCount() int {
	return len(r.items)
}
