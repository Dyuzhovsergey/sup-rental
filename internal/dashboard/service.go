// Package dashboard формирует read-only показатели и финансовые отчёты администратора.
package dashboard

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Dyuzhovsergey/sup-rental/internal/rental"
)

var moscowLocation = time.FixedZone("Europe/Moscow", 3*60*60)

// ErrInvalidPaymentPagination сообщает о недопустимом номере или размере страницы платежей.
var ErrInvalidPaymentPagination = errors.New("invalid payment pagination")

// Query задаёт единый момент расчёта и границы московского календарного дня.
type Query struct {
	// Now — момент, относительно которого определяется просрочка.
	Now time.Time
	// DayStart — включённая граница московского календарного дня.
	DayStart time.Time
	// DayEnd — исключённая граница московского календарного дня.
	DayEnd time.Time
}

// Snapshot содержит агрегированные показатели оборудования, аренды и платежей.
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
	// PaymentsBaseTodayKopecks — основные оплаты, полученные сегодня.
	PaymentsBaseTodayKopecks int64
	// PaymentsOverdueTodayKopecks — доплаты за просрочку, полученные сегодня.
	PaymentsOverdueTodayKopecks int64
	// PaymentsRefundTodayKopecks — возвраты оплаты, выполненные сегодня.
	PaymentsRefundTodayKopecks int64
	// PaymentsNetTodayKopecks — чистая выручка за сегодня с учётом возвратов.
	PaymentsNetTodayKopecks int64
}

// PaymentOperation описывает одну неизменяемую платёжную операцию для административного отчёта.
type PaymentOperation struct {
	// ID — внутренний идентификатор платёжной операции.
	ID int64
	// RentalID — идентификатор аренды, к которой относится операция.
	RentalID int64
	// ClientName — текущее ФИО клиента аренды.
	ClientName string
	// Kind — вид полученной оплаты или возврата.
	Kind rental.PaymentKind
	// AmountKopecks — положительная сумма операции в копейках; знак отображения определяется Kind.
	AmountKopecks int64
	// OccurredAt — абсолютный момент фиксации операции.
	OccurredAt time.Time
	// ActorLogin — неизменяемый login пользователя, выполнившего операцию.
	ActorLogin string
}

// PaymentOperationsQuery задаёт московский день и страницу платёжных операций.
type PaymentOperationsQuery struct {
	// DayStart — включённая граница московского календарного дня.
	DayStart time.Time
	// DayEnd — исключённая граница московского календарного дня.
	DayEnd time.Time
	// Page — номер страницы, начиная с единицы.
	Page int
	// PageSize — количество операций на странице.
	PageSize int
}

// PaymentOperationsPage содержит страницу операций и общее число операций за день.
type PaymentOperationsPage struct {
	// Operations — операции текущей страницы в порядке от новых к старым.
	Operations []PaymentOperation
	// Total — полное число операций за выбранный день.
	Total int64
	// Page — номер текущей страницы.
	Page int
	// PageSize — запрошенный размер страницы.
	PageSize int
}

// Repository загружает административные показатели и платёжные операции из постоянного хранилища.
type Repository interface {
	Snapshot(ctx context.Context, query Query) (Snapshot, error)
	PaymentOperations(ctx context.Context, query PaymentOperationsQuery) (PaymentOperationsPage, error)
}

// Service предоставляет read-only сценарии административной панели.
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
	now, dayStart, dayEnd := s.currentMoscowDay()

	snapshot, err := s.repository.Snapshot(ctx, Query{
		Now: now, DayStart: dayStart, DayEnd: dayEnd,
	})
	if err != nil {
		return Snapshot{}, fmt.Errorf("load admin dashboard: %w", err)
	}
	if err := validateSnapshot(snapshot); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

// PaymentOperations возвращает страницу платёжных операций текущего московского дня.
func (s *Service) PaymentOperations(ctx context.Context, page, pageSize int) (PaymentOperationsPage, error) {
	if page <= 0 || !allowedPaymentPageSize(pageSize) {
		return PaymentOperationsPage{}, ErrInvalidPaymentPagination
	}
	_, dayStart, dayEnd := s.currentMoscowDay()
	result, err := s.repository.PaymentOperations(ctx, PaymentOperationsQuery{
		DayStart: dayStart, DayEnd: dayEnd, Page: page, PageSize: pageSize,
	})
	if err != nil {
		return PaymentOperationsPage{}, fmt.Errorf("load payment operations: %w", err)
	}
	if result.Total < 0 {
		return PaymentOperationsPage{}, fmt.Errorf("validate payment operations: negative total")
	}
	result.Page = page
	result.PageSize = pageSize
	return result, nil
}

func (s *Service) currentMoscowDay() (time.Time, time.Time, time.Time) {
	now := s.now()
	localNow := now.In(moscowLocation)
	dayStart := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, moscowLocation)
	return now, dayStart, dayStart.AddDate(0, 0, 1)
}

func allowedPaymentPageSize(pageSize int) bool {
	return pageSize == 5 || pageSize == 10 || pageSize == 15
}

func validateSnapshot(snapshot Snapshot) error {
	values := []int64{
		snapshot.EquipmentTotal, snapshot.EquipmentAvailable,
		snapshot.EquipmentMaintenance, snapshot.EquipmentRetired,
		snapshot.EquipmentIssued, snapshot.RentalsActive,
		snapshot.RentalsOverdue, snapshot.RentalsStartingToday,
		snapshot.RentalsEndingToday, snapshot.PaymentsBaseTodayKopecks,
		snapshot.PaymentsOverdueTodayKopecks, snapshot.PaymentsRefundTodayKopecks,
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
	wantNet := snapshot.PaymentsBaseTodayKopecks + snapshot.PaymentsOverdueTodayKopecks -
		snapshot.PaymentsRefundTodayKopecks
	if snapshot.PaymentsNetTodayKopecks != wantNet {
		return fmt.Errorf("validate admin dashboard: payment net does not match payment totals")
	}
	return nil
}
