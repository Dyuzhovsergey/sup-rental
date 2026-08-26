package rental

import (
	"errors"
	"math"
	"time"
)

const (
	// LateReturnGracePeriod задаёт бесплатный период после планового окончания.
	LateReturnGracePeriod = 10 * time.Minute
)

var (
	// ErrPriceOverflow означает, что итоговая стоимость не помещается в int64.
	ErrPriceOverflow = errors.New("rental price exceeds supported range")
	// ErrSettlementNotAvailable означает, что окончательный расчёт ещё не зафиксирован.
	ErrSettlementNotAvailable = errors.New("rental settlement is not available")
	// ErrInvalidSettlement означает несогласованный с арендой сохранённый расчёт.
	ErrInvalidSettlement = errors.New("invalid rental settlement")
	// ErrInvalidOverdueTotal означает отрицательную доплату или сумму, при
	// добавлении которой итоговая стоимость переполняет int64.
	ErrInvalidOverdueTotal = errors.New("invalid rental overdue total")
)

// Settlement содержит зафиксированный окончательный расчёт завершённой аренды.
// Все денежные значения хранятся в копейках без использования float.
type Settlement struct {
	// PlannedTotalKopecks — стоимость планового периода по снимкам тарифов.
	PlannedTotalKopecks int64
	// OverdueDuration — фактическое время после планового окончания.
	OverdueDuration time.Duration
	// OverdueSlots — число оплачиваемых получасовых слотов просрочки.
	OverdueSlots int
	// CalculatedOverdueTotalKopecks — доплата, автоматически рассчитанная по
	// фактической просрочке и сохранённым снимкам тарифов.
	CalculatedOverdueTotalKopecks int64
	// OverdueTotalKopecks — фактически применённая оператором доплата.
	OverdueTotalKopecks int64
	// FinalTotalKopecks — итоговая стоимость аренды.
	FinalTotalKopecks int64
}

// PlannedTotalKopecks рассчитывает предварительную стоимость аренды в копейках.
// Стоимость использует плановый интервал и тарифы снимков состава.
func (r Rental) PlannedTotalKopecks() (int64, error) {
	if err := r.Interval.validate(); err != nil {
		return 0, err
	}

	hourlyTotal, err := r.hourlyTotalKopecks()
	if err != nil {
		return 0, err
	}

	if hourlyTotal == 0 {
		return 0, nil
	}

	halfHourlyTotal := hourlyTotal / 2
	slots := int64(r.Interval.SlotCount())
	if slots > math.MaxInt64/halfHourlyTotal {
		return 0, ErrPriceOverflow
	}

	return halfHourlyTotal * slots, nil
}

// SettlementAt рассчитывает предварительный итог на указанный момент возврата.
// Метод доступен только для активной аренды и не изменяет её состояние.
func (r Rental) SettlementAt(returnedAt time.Time) (Settlement, error) {
	if r.Status != StatusActive {
		return Settlement{}, ErrSettlementNotAvailable
	}
	if returnedAt.IsZero() {
		return Settlement{}, ErrReturnedAtRequired
	}
	if r.issuedAt == nil || r.issuedAt.IsZero() {
		return Settlement{}, ErrIssuedAtRequired
	}
	if returnedAt.Before(*r.issuedAt) {
		return Settlement{}, ErrReturnedBeforeIssued
	}
	return r.calculateSettlement(returnedAt)
}

// Settlement возвращает копию окончательного расчёта и признак его наличия.
func (r Rental) Settlement() (Settlement, bool) {
	if r.settlement == nil {
		return Settlement{}, false
	}
	return *r.settlement, true
}

// WithOverdueTotal возвращает копию расчёта с вручную заданной доплатой.
// Значение может быть меньше или больше автоматического расчёта, но не может
// быть отрицательным или приводить к переполнению итоговой стоимости.
func (s Settlement) WithOverdueTotal(overdueTotalKopecks int64) (Settlement, error) {
	if s.PlannedTotalKopecks < 0 || overdueTotalKopecks < 0 ||
		overdueTotalKopecks > math.MaxInt64-s.PlannedTotalKopecks {
		return Settlement{}, ErrInvalidOverdueTotal
	}
	s.OverdueTotalKopecks = overdueTotalKopecks
	s.FinalTotalKopecks = s.PlannedTotalKopecks + overdueTotalKopecks
	return s, nil
}

// RestoreSettlement проверяет и присоединяет расчёт, загруженный из хранилища.
// Сохранённое число платных слотов считается историческим результатом политики
// расчёта. Рассчитанная доплата восстанавливается по тарифным снимкам, а
// применённая — как разница между сохранённым итогом и базовой стоимостью.
func (r *Rental) RestoreSettlement(overdueSlots int, finalTotalKopecks int64) error {
	if r.Status != StatusCompleted || r.returnedAt == nil {
		return ErrInvalidSettlement
	}
	if overdueSlots < 0 {
		return ErrInvalidSettlement
	}

	plannedTotal, err := r.PlannedTotalKopecks()
	if err != nil {
		return err
	}
	hourlyTotal, err := r.hourlyTotalKopecks()
	if err != nil {
		return err
	}
	halfHourlyTotal := hourlyTotal / 2
	if overdueSlots > 0 && int64(overdueSlots) > math.MaxInt64/halfHourlyTotal {
		return ErrInvalidSettlement
	}
	calculatedOverdueTotal := halfHourlyTotal * int64(overdueSlots)
	if calculatedOverdueTotal > math.MaxInt64-plannedTotal || finalTotalKopecks < plannedTotal {
		return ErrInvalidSettlement
	}
	appliedOverdueTotal := finalTotalKopecks - plannedTotal

	expectedReturnAt, ok := r.ExpectedReturnAt()
	if !ok {
		return ErrInvalidSettlement
	}
	overdueDuration := r.returnedAt.Sub(expectedReturnAt)
	if overdueDuration < 0 {
		overdueDuration = 0
	}
	r.settlement = &Settlement{
		PlannedTotalKopecks:           plannedTotal,
		OverdueDuration:               overdueDuration,
		OverdueSlots:                  overdueSlots,
		CalculatedOverdueTotalKopecks: calculatedOverdueTotal,
		OverdueTotalKopecks:           appliedOverdueTotal,
		FinalTotalKopecks:             finalTotalKopecks,
	}
	return nil
}

func (r Rental) calculateSettlement(returnedAt time.Time) (Settlement, error) {
	plannedTotal, err := r.PlannedTotalKopecks()
	if err != nil {
		return Settlement{}, err
	}
	hourlyTotal, err := r.hourlyTotalKopecks()
	if err != nil {
		return Settlement{}, err
	}

	expectedReturnAt, ok := r.ExpectedReturnAt()
	if !ok {
		return Settlement{}, ErrExpectedReturnAtRequired
	}
	overdueDuration := returnedAt.Sub(expectedReturnAt)
	if overdueDuration < 0 {
		overdueDuration = 0
	}
	overdueSlots := 0
	if overdueDuration > LateReturnGracePeriod {
		overdueSlots = int(overdueDuration / SlotDuration)
		if overdueDuration%SlotDuration != 0 {
			overdueSlots++
		}
	}

	halfHourlyTotal := hourlyTotal / 2
	if overdueSlots > 0 && int64(overdueSlots) > math.MaxInt64/halfHourlyTotal {
		return Settlement{}, ErrPriceOverflow
	}
	overdueTotal := halfHourlyTotal * int64(overdueSlots)
	if overdueTotal > math.MaxInt64-plannedTotal {
		return Settlement{}, ErrPriceOverflow
	}
	return Settlement{
		PlannedTotalKopecks:           plannedTotal,
		OverdueDuration:               overdueDuration,
		OverdueSlots:                  overdueSlots,
		CalculatedOverdueTotalKopecks: overdueTotal,
		OverdueTotalKopecks:           overdueTotal,
		FinalTotalKopecks:             plannedTotal + overdueTotal,
	}, nil
}

func (r Rental) hourlyTotalKopecks() (int64, error) {
	var hourlyTotal int64
	for _, item := range r.items {
		if err := item.validate(); err != nil {
			return 0, err
		}
		if item.HourlyRateKopecks > math.MaxInt64-hourlyTotal {
			return 0, ErrPriceOverflow
		}
		hourlyTotal += item.HourlyRateKopecks
	}
	return hourlyTotal, nil
}
