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
	// ErrInvalidClientID означает, что аренда не связана с существующим клиентом.
	ErrInvalidClientID = errors.New("rental client ID must be positive")
	// ErrInvalidStatus означает, что передано неизвестное состояние аренды.
	ErrInvalidStatus = errors.New("invalid rental status")
	// ErrStatusTransitionNotAllowed означает, что переход между состояниями запрещён.
	ErrStatusTransitionNotAllowed = errors.New("rental status transition is not allowed")
	// ErrRentalItemsRequired означает попытку подтвердить аренду без оборудования.
	ErrRentalItemsRequired = errors.New("rental must contain at least one item before confirmation")
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
