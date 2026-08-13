package rental

import (
	"errors"
	"time"
)

// SlotDuration задаёт минимальный временной шаг планируемой аренды.
const SlotDuration = 30 * time.Minute

var (
	// ErrStartTimeRequired означает, что время начала аренды не указано.
	ErrStartTimeRequired = errors.New("rental start time is required")
	// ErrEndTimeRequired означает, что время окончания аренды не указано.
	ErrEndTimeRequired = errors.New("rental end time is required")
	// ErrEndNotAfterStart означает, что окончание не позднее начала аренды.
	ErrEndNotAfterStart = errors.New("rental end time must be after start time")
	// ErrIntervalTooShort означает, что аренда длится менее одного временного шага.
	ErrIntervalTooShort = errors.New("rental interval must be at least 30 minutes")
	// ErrIntervalNotAligned означает, что граница интервала не совпадает с
	// началом или серединой часа.
	ErrIntervalNotAligned = errors.New("rental interval must align to 30-minute boundaries")
)

// Interval представляет планируемый полуоткрытый интервал аренды [start, end).
// Нулевое значение не является валидным интервалом; используйте NewInterval.
type Interval struct {
	start time.Time
	end   time.Time
}

// NewInterval создаёт интервал с границами, кратными 30 минутам.
// Минимальная длительность интервала составляет один 30-минутный слот.
func NewInterval(start, end time.Time) (Interval, error) {
	if err := validateInterval(start, end); err != nil {
		return Interval{}, err
	}

	return Interval{start: start, end: end}, nil
}

// Start возвращает включённую границу начала интервала.
func (i Interval) Start() time.Time {
	return i.start
}

// End возвращает исключённую границу окончания интервала.
func (i Interval) End() time.Time {
	return i.end
}

// SlotCount возвращает количество полных 30-минутных слотов в интервале.
// Для невалидного нулевого значения Interval метод возвращает 0.
func (i Interval) SlotCount() int {
	if i.start.IsZero() || !i.end.After(i.start) {
		return 0
	}

	return int(i.end.Sub(i.start) / SlotDuration)
}

// Overlaps сообщает, пересекается ли интервал с другим полуоткрытым интервалом.
// Соседние интервалы, у которых окончание одного равно началу другого, не
// пересекаются. Метод предназначен для значений, созданных через NewInterval.
func (i Interval) Overlaps(other Interval) bool {
	return i.start.Before(other.end) && other.start.Before(i.end)
}

func (i Interval) validate() error {
	return validateInterval(i.start, i.end)
}

func validateInterval(start, end time.Time) error {
	if start.IsZero() {
		return ErrStartTimeRequired
	}
	if end.IsZero() {
		return ErrEndTimeRequired
	}
	if !end.After(start) {
		return ErrEndNotAfterStart
	}
	if end.Sub(start) < SlotDuration {
		return ErrIntervalTooShort
	}
	if !isSlotBoundary(start) || !isSlotBoundary(end) {
		return ErrIntervalNotAligned
	}

	return nil
}

func isSlotBoundary(value time.Time) bool {
	return value.Equal(value.Truncate(SlotDuration))
}
