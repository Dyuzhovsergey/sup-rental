package rental

import (
	"errors"
	"math"
)

var (
	// ErrPriceOverflow означает, что итоговая стоимость не помещается в int64.
	ErrPriceOverflow = errors.New("rental price exceeds supported range")
)

// PlannedTotalKopecks рассчитывает предварительную стоимость аренды в копейках.
// Стоимость использует плановый интервал и тарифы снимков состава.
func (r Rental) PlannedTotalKopecks() (int64, error) {
	if err := r.Interval.validate(); err != nil {
		return 0, err
	}

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
