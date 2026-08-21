// Package dashboard формирует read-only показатели главной панели администратора.
package dashboard

import (
	"context"
	"fmt"
	"time"
)

var moscowLocation = time.FixedZone("Europe/Moscow", 3*60*60)

// Query задаёт единый момент расчёта и границы московского календарного дня.
type Query struct {
	// Now — момент, относительно которого определяется просрочка.
	Now time.Time
	// DayStart — включённая граница московского календарного дня.
	DayStart time.Time
	// DayEnd — исключённая граница московского календарного дня.
	DayEnd time.Time
}

// Snapshot содержит агрегированные показатели оборудования и аренды.
type Snapshot struct {
	// EquipmentTotal — общее число физических единиц оборудования.
	EquipmentTotal int64
	// EquipmentAvailable — число единиц в состоянии available.
	EquipmentAvailable int64
	// EquipmentMaintenance — число единиц на обслуживании.
	EquipmentMaintenance int64
	// EquipmentRetired — число списанных единиц.
	EquipmentRetired int64
	// EquipmentIssued — число выданных клиентам единиц.
	EquipmentIssued int64
	// RentalsActive — полное число активных аренд.
	RentalsActive int64
	// RentalsOverdue — число активных аренд с прошедшим плановым окончанием.
	RentalsOverdue int64
	// RentalsStartingToday — число текущих аренд с плановым началом сегодня.
	RentalsStartingToday int64
	// RentalsEndingToday — число текущих аренд с плановым окончанием сегодня.
	RentalsEndingToday int64
}

// Repository загружает согласованный снимок показателей из постоянного хранилища.
type Repository interface {
	Snapshot(ctx context.Context, query Query) (Snapshot, error)
}

// Service предоставляет сценарий чтения главной панели администратора.
type Service struct {
	repository Repository
	now        func() time.Time
}

// NewService создаёт сервис с обязательным repository и системными часами.
func NewService(repository Repository) *Service {
	return &Service{repository: repository, now: time.Now}
}

// Snapshot возвращает показатели для текущего московского календарного дня.
func (s *Service) Snapshot(ctx context.Context) (Snapshot, error) {
	now := s.now()
	localNow := now.In(moscowLocation)
	dayStart := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, moscowLocation)

	snapshot, err := s.repository.Snapshot(ctx, Query{
		Now: now, DayStart: dayStart, DayEnd: dayStart.AddDate(0, 0, 1),
	})
	if err != nil {
		return Snapshot{}, fmt.Errorf("load admin dashboard: %w", err)
	}
	if err := validateSnapshot(snapshot); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func validateSnapshot(snapshot Snapshot) error {
	values := []int64{
		snapshot.EquipmentTotal, snapshot.EquipmentAvailable,
		snapshot.EquipmentMaintenance, snapshot.EquipmentRetired,
		snapshot.EquipmentIssued, snapshot.RentalsActive,
		snapshot.RentalsOverdue, snapshot.RentalsStartingToday,
		snapshot.RentalsEndingToday,
	}
	for _, value := range values {
		if value < 0 {
			return fmt.Errorf("validate admin dashboard: negative count")
		}
	}
	statusTotal := snapshot.EquipmentAvailable + snapshot.EquipmentMaintenance +
		snapshot.EquipmentRetired + snapshot.EquipmentIssued
	if statusTotal != snapshot.EquipmentTotal {
		return fmt.Errorf("validate admin dashboard: equipment status counts do not match total")
	}
	if snapshot.RentalsOverdue > snapshot.RentalsActive {
		return fmt.Errorf("validate admin dashboard: overdue rentals exceed active rentals")
	}
	return nil
}
