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

// ErrInvalidFinancialPeriod сообщает о пустом или перевёрнутом финансовом периоде.
var ErrInvalidFinancialPeriod = errors.New("invalid financial period")

// FinancialPeriod задаёт полуоткрытый временной диапазон финансового отчёта.
type FinancialPeriod struct {
	// Start — включённая граница периода.
	Start time.Time
	// End — исключённая граница периода.
	End time.Time
}

// NewFinancialPeriod создаёт проверенный финансовый период с исключённой правой границей.
func NewFinancialPeriod(start, end time.Time) (FinancialPeriod, error) {
	period := FinancialPeriod{Start: start, End: end}
	if !period.Valid() {
		return FinancialPeriod{}, ErrInvalidFinancialPeriod
	}
	return period, nil
}

// Valid сообщает, что обе границы заданы и конец находится после начала.
func (p FinancialPeriod) Valid() bool {
	return !p.Start.IsZero() && p.End.After(p.Start)
}

// Query задаёт единый момент расчёта, текущий день и финансовый период.
type Query struct {
	// Now — момент, относительно которого определяется просрочка.
	Now time.Time
	// TodayStart — включённая граница текущего московского календарного дня.
	TodayStart time.Time
	// TodayEnd — исключённая граница текущего московского календарного дня.
	TodayEnd time.Time
	// PaymentStart — включённая граница финансового периода.
	PaymentStart time.Time
	// PaymentEnd — исключённая граница финансового периода.
	PaymentEnd time.Time
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
	// PaymentsBaseKopecks — основные оплаты, полученные за выбранный период.
	PaymentsBaseKopecks int64
	// PaymentsOverdueKopecks — доплаты за просрочку, полученные за выбранный период.
	PaymentsOverdueKopecks int64
	// PaymentsRefundKopecks — возвраты оплаты, выполненные за выбранный период.
	PaymentsRefundKopecks int64
	// PaymentsNetKopecks — чистая выручка за период с учётом возвратов.
	PaymentsNetKopecks int64
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

// PaymentOperationsQuery задаёт финансовый период и страницу платёжных операций.
type PaymentOperationsQuery struct {
	// PeriodStart — включённая граница финансового периода.
	PeriodStart time.Time
	// PeriodEnd — исключённая граница финансового периода.
	PeriodEnd time.Time
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

// Snapshot возвращает оперативные показатели текущего дня и финансы за выбранный период.
func (s *Service) Snapshot(ctx context.Context, period FinancialPeriod) (Snapshot, error) {
	if !period.Valid() {
		return Snapshot{}, ErrInvalidFinancialPeriod
	}
	now, dayStart, dayEnd := s.currentMoscowDay()

	snapshot, err := s.repository.Snapshot(ctx, Query{
		Now: now, TodayStart: dayStart, TodayEnd: dayEnd,
		PaymentStart: period.Start, PaymentEnd: period.End,
	})
	if err != nil {
		return Snapshot{}, fmt.Errorf("load admin dashboard: %w", err)
	}
	if err := validateSnapshot(snapshot); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

// PaymentOperations возвращает страницу платёжных операций выбранного периода.
func (s *Service) PaymentOperations(
	ctx context.Context,
	period FinancialPeriod,
	page, pageSize int,
) (PaymentOperationsPage, error) {
	if !period.Valid() {
		return PaymentOperationsPage{}, ErrInvalidFinancialPeriod
	}
	if page <= 0 || !allowedPaymentPageSize(pageSize) {
		return PaymentOperationsPage{}, ErrInvalidPaymentPagination
	}
	result, err := s.repository.PaymentOperations(ctx, PaymentOperationsQuery{
		PeriodStart: period.Start, PeriodEnd: period.End, Page: page, PageSize: pageSize,
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
		snapshot.RentalsEndingToday, snapshot.PaymentsBaseKopecks,
		snapshot.PaymentsOverdueKopecks, snapshot.PaymentsRefundKopecks,
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
	wantNet := snapshot.PaymentsBaseKopecks + snapshot.PaymentsOverdueKopecks -
		snapshot.PaymentsRefundKopecks
	if snapshot.PaymentsNetKopecks != wantNet {
		return fmt.Errorf("validate admin dashboard: payment net does not match payment totals")
	}
	return nil
}
