package rental

import (
	"errors"
	"testing"
	"time"

	"github.com/Dyuzhovsergey/sup-rental/internal/equipment"
)

func TestNewRejectsInvalidRentalItem(t *testing.T) {
	t.Parallel()

	interval := mustInterval(t, time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC), time.Date(2026, 8, 14, 11, 0, 0, 0, time.UTC))
	tests := []struct {
		name    string
		change  func(*Item)
		wantErr error
	}{
		{name: "invalid equipment ID", change: func(item *Item) { item.EquipmentID = 0 }, wantErr: ErrInvalidEquipmentID},
		{name: "invalid kind", change: func(item *Item) { item.Kind = equipment.Kind("unknown") }, wantErr: ErrInvalidEquipmentKind},
		{name: "model not canonical", change: func(item *Item) { item.ModelCode = "carbon" }, wantErr: ErrInvalidItemModelCode},
		{name: "number mismatches kind", change: func(item *Item) { item.InventoryNumber = "PADDLE-CARBON-1" }, wantErr: ErrInvalidInventoryNumber},
		{name: "number mismatches model", change: func(item *Item) { item.InventoryNumber = "SUP-TOURING-1" }, wantErr: ErrInvalidInventoryNumber},
		{name: "invalid sequence", change: func(item *Item) { item.InventoryNumber = "SUP-CARBON-0" }, wantErr: ErrInvalidInventoryNumber},
		{name: "non-positive rate", change: func(item *Item) { item.HourlyRateKopecks = 0 }, wantErr: ErrInvalidItemHourlyRate},
		{name: "odd rate", change: func(item *Item) { item.HourlyRateKopecks = 50_001 }, wantErr: ErrInexactHalfHourRate},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			item := validRentalItem(1)
			tt.change(&item)
			_, err := New(42, interval, []Item{item})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("New() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestRentalItemsReturnsCopy(t *testing.T) {
	t.Parallel()

	interval := mustInterval(t, time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC), time.Date(2026, 8, 14, 11, 0, 0, 0, time.UTC))
	item := validRentalItem(1)
	value, err := New(42, interval, []Item{item})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	items := value.Items()
	items[0].InventoryNumber = "CHANGED"
	items = append(items, validRentalItem(2))
	if got := value.Items(); len(got) != 1 || got[0] != item {
		t.Fatalf("Items() = %#v", got)
	}
}

func validRentalItem(equipmentID int64) Item {
	return Item{
		EquipmentID: equipmentID, InventoryNumber: "SUP-CARBON-1",
		Kind: equipment.KindSUPBoard, ModelCode: "CARBON", HourlyRateKopecks: 50_000,
	}
}
