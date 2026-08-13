package rental

import (
	"errors"
	"math"
	"strconv"
	"testing"
	"time"
)

func TestRentalPlannedTotalKopecks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		duration  time.Duration
		items     []Item
		wantTotal int64
	}{
		{
			name:      "empty draft",
			duration:  30 * time.Minute,
			wantTotal: 0,
		},
		{
			name:      "one item for half hour",
			duration:  30 * time.Minute,
			items:     []Item{validRentalItem(1)},
			wantTotal: 25_000,
		},
		{
			name:      "one item for one hour",
			duration:  time.Hour,
			items:     []Item{validRentalItem(1)},
			wantTotal: 50_000,
		},
		{
			name:      "one item for one and a half hours",
			duration:  90 * time.Minute,
			items:     []Item{validRentalItem(1)},
			wantTotal: 75_000,
		},
		{
			name:     "several items for one and a half hours",
			duration: 90 * time.Minute,
			items: []Item{
				validRentalItemWithRate(1, 50_000),
				validRentalItemWithRate(2, 30_000),
				validRentalItemWithRate(3, 20_000),
			},
			wantTotal: 150_000,
		},
		{
			name:      "one item for several hours",
			duration:  3 * time.Hour,
			items:     []Item{validRentalItem(1)},
			wantTotal: 150_000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rental := rentalWithDuration(t, tt.duration)
			for _, item := range tt.items {
				if err := rental.AddItem(item); err != nil {
					t.Fatalf("AddItem() error = %v", err)
				}
			}

			got, err := rental.PlannedTotalKopecks()
			if err != nil {
				t.Fatalf("PlannedTotalKopecks() error = %v", err)
			}
			if got != tt.wantTotal {
				t.Fatalf("PlannedTotalKopecks() = %d, want %d", got, tt.wantTotal)
			}
		})
	}
}

func TestRentalPlannedTotalKopecksRejectsOverflow(t *testing.T) {
	t.Parallel()

	rental := rentalWithDuration(t, 90*time.Minute)
	item := validRentalItemWithRate(1, math.MaxInt64-1)
	if err := rental.AddItem(item); err != nil {
		t.Fatalf("AddItem() error = %v", err)
	}

	_, err := rental.PlannedTotalKopecks()
	if !errors.Is(err, ErrPriceOverflow) {
		t.Fatalf("PlannedTotalKopecks() error = %v, want %v", err, ErrPriceOverflow)
	}
}

func TestRentalPlannedTotalKopecksRejectsHourlySumOverflow(t *testing.T) {
	t.Parallel()

	rental := rentalWithDuration(t, time.Hour)
	for _, item := range []Item{
		validRentalItemWithRate(1, math.MaxInt64-1),
		validRentalItemWithRate(2, 2),
	} {
		if err := rental.AddItem(item); err != nil {
			t.Fatalf("AddItem() error = %v", err)
		}
	}

	_, err := rental.PlannedTotalKopecks()
	if !errors.Is(err, ErrPriceOverflow) {
		t.Fatalf("PlannedTotalKopecks() error = %v, want %v", err, ErrPriceOverflow)
	}
}

func rentalWithDuration(t *testing.T, duration time.Duration) Rental {
	t.Helper()

	start := time.Date(2026, time.August, 14, 10, 0, 0, 0, time.UTC)
	interval := mustInterval(t, start, start.Add(duration))
	rental, err := New(42, interval)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return rental
}

func validRentalItemWithRate(equipmentID, hourlyRate int64) Item {
	item := validRentalItem(equipmentID)
	item.InventoryNumber = "SUP-CARBON-" + strconv.FormatInt(equipmentID, 10)
	item.HourlyRateKopecks = hourlyRate
	return item
}
