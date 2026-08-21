package rental

import (
	"context"
	"fmt"
	"time"
)

const (
	// MonitoringListLimit ограничивает число оперативных записей одного типа
	// на главной странице оператора.
	MonitoringListLimit = 10
)

var moscowLocation = time.FixedZone("Europe/Moscow", 3*60*60)

// MonitoringQuery задаёт единый момент расчёта, границы рабочего дня и лимит
// строк для read-only снимка оперативного мониторинга.
type MonitoringQuery struct {
	// Now — момент, относительно которого определяются задержки и просрочки.
	Now time.Time
	// DayStart — включённая граница московского календарного дня.
	DayStart time.Time
	// DayEnd — исключённая граница московского календарного дня.
	DayEnd time.Time
	// Limit — максимальное число подтверждённых и активных записей в снимке.
	Limit int
}

// MonitoringData содержит сохранённые данные, необходимые application service
// для формирования оперативного снимка.
type MonitoringData struct {
	// TodayTotal — число подтверждённых и активных аренд, пересекающих выбранный день.
	TodayTotal int
	// ConfirmedTotal — число подтверждённых аренд, пересекающих выбранный день.
	ConfirmedTotal int
	// ActiveTotal — полное число активных аренд.
	ActiveTotal int
	// OverdueTotal — число активных аренд с прошедшим плановым окончанием.
	OverdueTotal int
	// Confirmed содержит ближайшие подтверждённые аренды выбранного дня.
	Confirmed []Summary
	// Active содержит активные аренды, начиная с просроченных.
	Active []Summary
}

// MonitoringTimingState описывает временное положение аренды относительно её
// планового интервала.
type MonitoringTimingState string

const (
	// MonitoringUpcoming означает, что плановый интервал ещё не начался.
	MonitoringUpcoming MonitoringTimingState = "upcoming"
	// MonitoringIssueDelayed означает, что подтверждённая аренда не выдана после
	// наступления планового начала.
	MonitoringIssueDelayed MonitoringTimingState = "issue_delayed"
	// MonitoringActive означает, что активная аренда находится внутри интервала.
	MonitoringActive MonitoringTimingState = "active"
	// MonitoringDue означает точное наступление планового окончания.
	MonitoringDue MonitoringTimingState = "due"
	// MonitoringOverdue означает, что плановое окончание активной аренды прошло.
	MonitoringOverdue MonitoringTimingState = "overdue"
)

// MonitoringTiming содержит рассчитанный прогресс и продолжительность до или
// после ближайшей значимой плановой границы.
type MonitoringTiming struct {
	// State задаёт смысл значения Delta.
	State MonitoringTimingState
	// Percent — целая доля прошедшего планового интервала от 0 до 100.
	Percent int
	// Delta — время до начала или окончания либо длительность задержки.
	Delta time.Duration
}

// MonitoringEntry объединяет данные аренды и её временное состояние.
type MonitoringEntry struct {
	// Summary содержит сохранённые данные аренды для строки мониторинга.
	Summary Summary
	// Timing содержит рассчитанное положение относительно планового интервала.
	Timing MonitoringTiming
}

// MonitoringSnapshot представляет согласованный read-only снимок главной
// страницы оператора на один момент времени.
type MonitoringSnapshot struct {
	// GeneratedAt — единый момент формирования снимка.
	GeneratedAt time.Time
	// TodayTotal — число подтверждённых и активных аренд выбранного дня.
	TodayTotal int
	// ConfirmedTotal — число подтверждённых аренд выбранного дня.
	ConfirmedTotal int
	// ActiveTotal — полное число активных аренд.
	ActiveTotal int
	// OverdueTotal — число просроченных активных аренд.
	OverdueTotal int
	// Confirmed содержит ограниченный список подтверждённых аренд дня.
	Confirmed []MonitoringEntry
	// Active содержит ограниченный список активных аренд с просроченными в начале.
	Active []MonitoringEntry
}

// Monitoring возвращает оперативный снимок аренды для московского
// календарного дня. Расчёт использует один момент времени для всех показателей.
func (s *Service) Monitoring(ctx context.Context) (MonitoringSnapshot, error) {
	now := s.now()
	localNow := now.In(moscowLocation)
	dayStart := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, moscowLocation)
	query := MonitoringQuery{
		Now:      now,
		DayStart: dayStart,
		DayEnd:   dayStart.AddDate(0, 0, 1),
		Limit:    MonitoringListLimit,
	}

	data, err := s.repository.Monitoring(ctx, query)
	if err != nil {
		return MonitoringSnapshot{}, fmt.Errorf("load rental monitoring: %w", err)
	}
	if data.TodayTotal < 0 || data.ConfirmedTotal < 0 || data.ActiveTotal < 0 || data.OverdueTotal < 0 {
		return MonitoringSnapshot{}, fmt.Errorf("validate rental monitoring: negative count")
	}

	confirmed, err := monitoringEntries(data.Confirmed, StatusConfirmed, now)
	if err != nil {
		return MonitoringSnapshot{}, err
	}
	active, err := monitoringEntries(data.Active, StatusActive, now)
	if err != nil {
		return MonitoringSnapshot{}, err
	}

	return MonitoringSnapshot{
		GeneratedAt: now, TodayTotal: data.TodayTotal,
		ConfirmedTotal: data.ConfirmedTotal, ActiveTotal: data.ActiveTotal,
		OverdueTotal: data.OverdueTotal, Confirmed: confirmed, Active: active,
	}, nil
}

func monitoringEntries(summaries []Summary, want Status, now time.Time) ([]MonitoringEntry, error) {
	entries := make([]MonitoringEntry, 0, len(summaries))
	for _, summary := range summaries {
		if summary.ID <= 0 || summary.Status != want {
			return nil, fmt.Errorf("validate rental monitoring entry: %w", ErrInvalidStatus)
		}
		entries = append(entries, MonitoringEntry{
			Summary: summary,
			Timing:  calculateMonitoringTiming(summary, now),
		})
	}
	return entries, nil
}

func calculateMonitoringTiming(summary Summary, now time.Time) MonitoringTiming {
	start := summary.Interval.Start()
	end := summary.Interval.End()
	if now.Before(start) {
		return MonitoringTiming{State: MonitoringUpcoming, Delta: start.Sub(now)}
	}

	percent := monitoringPercent(start, end, now)
	if summary.Status == StatusConfirmed {
		return MonitoringTiming{State: MonitoringIssueDelayed, Percent: percent, Delta: now.Sub(start)}
	}
	if now.Equal(end) {
		return MonitoringTiming{State: MonitoringDue, Percent: 100}
	}
	if now.After(end) {
		return MonitoringTiming{State: MonitoringOverdue, Percent: 100, Delta: now.Sub(end)}
	}
	return MonitoringTiming{State: MonitoringActive, Percent: percent, Delta: end.Sub(now)}
}

func monitoringPercent(start, end, now time.Time) int {
	if !now.After(start) {
		return 0
	}
	if !now.Before(end) {
		return 100
	}
	return int(now.Sub(start) * 100 / end.Sub(start))
}
