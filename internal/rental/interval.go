package rental

import (
	"errors"
	"time"
)

const (
	// SlotDuration задаёт минимальный шаг продолжительности аренды.
	SlotDuration = 30 * time.Minute
	// MaxDurationDays задаёт максимальное число полных суток в форме аренды.
	MaxDurationDays = 31
	// MaxDuration ограничивает одну планируемую аренду 31 сутками 23 часами
	// 30 минутами.
	MaxDuration = MaxDurationDays*24*time.Hour + 23*time.Hour + 30*time.Minute
)

var (
	// ErrStartTimeRequired означает, что время начала аренды не указано.
	ErrStartTimeRequired = errors.New("rental start time is required")
	// ErrEndTimeRequired означает, что время окончания аренды не указано.
	ErrEndTimeRequired = errors.New("rental end time is required")
	// ErrEndNotAfterStart означает, что окончание не позднее начала аренды.
	ErrEndNotAfterStart = errors.New("rental end time must be after start time")
	// ErrIntervalTooShort означает, что аренда длится менее одного временного шага.
	ErrIntervalTooShort = errors.New("rental interval must be at least 30 minutes")
	// ErrIntervalTooLong означает, что аренда длится более 31 суток 23 часов
	// 30 минут.
	ErrIntervalTooLong = errors.New("rental interval exceeds maximum duration")
	// ErrStartNotMinuteAligned означает, что начало содержит секунды или доли секунды.
	ErrStartNotMinuteAligned = errors.New("rental start time must align to a whole minute")
	// ErrDurationNotAligned означает, что продолжительность не кратна 30 минутам.
	ErrDurationNotAligned = errors.New("rental duration must align to 30-minute slots")
)

// Interval представляет планируемый полуоткрытый интервал аренды [start, end).
// Нулевое значение не является валидным интервалом; используйте NewInterval.
type Interval struct {
	start time.Time
	end   time.Time
}

// NewInterval создаёт интервал с началом, заданным с точностью до минуты.
// Продолжительность должна составлять от 30 минут до 31 суток 23 часов
// 30 минут и быть кратной 30 минутам.
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
	if end.Sub(start) > MaxDuration {
		return ErrIntervalTooLong
	}
	if !start.Equal(start.Truncate(time.Minute)) {
		return ErrStartNotMinuteAligned
	}
	if end.Sub(start)%SlotDuration != 0 {
		return ErrDurationNotAligned
	}

	return nil
}
