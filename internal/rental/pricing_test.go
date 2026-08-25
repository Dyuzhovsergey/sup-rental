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

			rental := rentalWithDuration(t, tt.duration, tt.items)

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

	rental := rentalWithDuration(t, 90*time.Minute, []Item{validRentalItemWithRate(1, math.MaxInt64-1)})

	_, err := rental.PlannedTotalKopecks()
	if !errors.Is(err, ErrPriceOverflow) {
		t.Fatalf("PlannedTotalKopecks() error = %v, want %v", err, ErrPriceOverflow)
	}
}

func TestRentalPlannedTotalKopecksRejectsHourlySumOverflow(t *testing.T) {
	t.Parallel()

	rental := rentalWithDuration(t, time.Hour, []Item{
		validRentalItemWithRate(1, math.MaxInt64-1),
		validRentalItemWithRate(2, 2),
	})

	_, err := rental.PlannedTotalKopecks()
	if !errors.Is(err, ErrPriceOverflow) {
		t.Fatalf("PlannedTotalKopecks() error = %v, want %v", err, ErrPriceOverflow)
	}
}

func TestRentalSettlementAtAppliesGraceAndRoundsWholeLatenessUp(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.August, 14, 10, 0, 0, 0, time.UTC)
	interval := mustInterval(t, start, start.Add(time.Hour))
	issuedAt := start
	tests := []struct {
		name           string
		returnedAt     time.Time
		wantDuration   time.Duration
		wantSlots      int
		wantOverdue    int64
		wantFinalTotal int64
	}{
		{name: "early return", returnedAt: start.Add(45 * time.Minute), wantFinalTotal: 50_000},
		{name: "on planned end", returnedAt: interval.End(), wantFinalTotal: 50_000},
		{name: "inside grace", returnedAt: interval.End().Add(8 * time.Minute), wantDuration: 8 * time.Minute, wantFinalTotal: 50_000},
		{name: "grace boundary", returnedAt: interval.End().Add(10 * time.Minute), wantDuration: 10 * time.Minute, wantFinalTotal: 50_000},
		{name: "first paid slot", returnedAt: interval.End().Add(11 * time.Minute), wantDuration: 11 * time.Minute, wantSlots: 1, wantOverdue: 25_000, wantFinalTotal: 75_000},
		{name: "second paid slot", returnedAt: interval.End().Add(31 * time.Minute), wantDuration: 31 * time.Minute, wantSlots: 2, wantOverdue: 50_000, wantFinalTotal: 100_000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value, err := Restore(7, 42, interval, StatusActive, &issuedAt, nil, []Item{validRentalItem(1)})
			if err != nil {
				t.Fatalf("Restore() error = %v", err)
			}
			got, err := value.SettlementAt(tt.returnedAt)
			if err != nil {
				t.Fatalf("SettlementAt() error = %v", err)
			}
			if got.PlannedTotalKopecks != 50_000 || got.OverdueDuration != tt.wantDuration ||
				got.OverdueSlots != tt.wantSlots || got.OverdueTotalKopecks != tt.wantOverdue ||
				got.FinalTotalKopecks != tt.wantFinalTotal {
				t.Fatalf("SettlementAt() = %+v", got)
			}
		})
	}
}

func TestRentalSettlementUsesExpectedReturnFromActualIssue(t *testing.T) {
	t.Parallel()

	plannedStart := time.Date(2026, time.August, 14, 10, 0, 0, 0, time.UTC)
	interval := mustInterval(t, plannedStart, plannedStart.Add(time.Hour))
	value, err := New(42, interval, []Item{validRentalItem(1)})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	issuedAt := plannedStart.Add(20 * time.Minute)
	if err := value.Issue(issuedAt); err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	settlement, err := value.SettlementAt(issuedAt.Add(time.Hour + 11*time.Minute))
	if err != nil {
		t.Fatalf("SettlementAt() error = %v", err)
	}
	if settlement.OverdueDuration != 11*time.Minute || settlement.OverdueSlots != 1 ||
		settlement.FinalTotalKopecks != 75_000 {
		t.Fatalf("SettlementAt() = %+v", settlement)
	}
}

func TestRentalCompleteFixesAndRestoresSettlement(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.August, 14, 10, 0, 0, 0, time.UTC)
	interval := mustInterval(t, start, start.Add(time.Hour))
	issuedAt := start
	returnedAt := interval.End().Add(11 * time.Minute)
	value, err := Restore(7, 42, interval, StatusActive, &issuedAt, nil, []Item{validRentalItem(1)})
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if err := value.Complete(returnedAt); err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	settlement, ok := value.Settlement()
	if !ok || settlement.OverdueSlots != 1 || settlement.FinalTotalKopecks != 75_000 {
		t.Fatalf("Settlement() = %+v, %t", settlement, ok)
	}

	restored, err := Restore(7, 42, interval, StatusCompleted, &issuedAt, &returnedAt, []Item{validRentalItem(1)})
	if err != nil {
		t.Fatalf("Restore(completed) error = %v", err)
	}
	if err := restored.RestoreSettlement(1, 75_000); err != nil {
		t.Fatalf("RestoreSettlement() error = %v", err)
	}
	if _, ok := restored.Settlement(); !ok {
		t.Fatal("restored Settlement() is missing")
	}
	if err := restored.RestoreSettlement(1, 50_000); !errors.Is(err, ErrInvalidSettlement) {
		t.Fatalf("RestoreSettlement(invalid) error = %v", err)
	}
}

func TestRentalRestoreSettlementPreservesHistoricalSlotCount(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.August, 14, 10, 0, 0, 0, time.UTC)
	interval := mustInterval(t, start, start.Add(time.Hour))
	issuedAt := start
	returnedAt := interval.End().Add(5 * time.Minute)
	value, err := Restore(7, 42, interval, StatusCompleted, &issuedAt, &returnedAt, []Item{validRentalItem(1)})
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}

	if err := value.RestoreSettlement(1, 75_000); err != nil {
		t.Fatalf("RestoreSettlement() error = %v", err)
	}
	settlement, ok := value.Settlement()
	if !ok || settlement.OverdueDuration != 5*time.Minute || settlement.OverdueSlots != 1 ||
		settlement.OverdueTotalKopecks != 25_000 || settlement.FinalTotalKopecks != 75_000 {
		t.Fatalf("Settlement() = %+v, %t", settlement, ok)
	}
}

func rentalWithDuration(t *testing.T, duration time.Duration, items []Item) Rental {
	t.Helper()

	start := time.Date(2026, time.August, 14, 10, 0, 0, 0, time.UTC)
	interval := mustInterval(t, start, start.Add(duration))
	rental, err := New(42, interval, items)
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
