package rental

import (
	"errors"
	"testing"

	"github.com/Dyuzhovsergey/sup-rental/internal/equipment"
)

func TestRentalAddItem(t *testing.T) {
	t.Parallel()

	rental := Rental{Status: StatusDraft}
	first := validRentalItem(1)
	second := validRentalItem(2)
	second.InventoryNumber = "SUP-CARBON-2"

	if err := rental.AddItem(first); err != nil {
		t.Fatalf("AddItem(first) error = %v", err)
	}
	if err := rental.AddItem(second); err != nil {
		t.Fatalf("AddItem(second) error = %v", err)
	}

	items := rental.Items()
	if rental.ItemCount() != 2 {
		t.Fatalf("ItemCount() = %d, want 2", rental.ItemCount())
	}
	if items[0] != first || items[1] != second {
		t.Fatalf("Items() = %#v, want %#v then %#v", items, first, second)
	}
}

func TestRentalAddItemRejectsInvalidItem(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		change  func(*Item)
		wantErr error
	}{
		{
			name:    "invalid equipment ID",
			change:  func(item *Item) { item.EquipmentID = 0 },
			wantErr: ErrInvalidEquipmentID,
		},
		{
			name:    "invalid kind",
			change:  func(item *Item) { item.Kind = equipment.Kind("unknown") },
			wantErr: ErrInvalidEquipmentKind,
		},
		{
			name:    "model code is not canonical",
			change:  func(item *Item) { item.ModelCode = "carbon" },
			wantErr: ErrInvalidItemModelCode,
		},
		{
			name:    "inventory number does not match kind",
			change:  func(item *Item) { item.InventoryNumber = "PADDLE-CARBON-1" },
			wantErr: ErrInvalidInventoryNumber,
		},
		{
			name:    "inventory number does not match model",
			change:  func(item *Item) { item.InventoryNumber = "SUP-TOURING-1" },
			wantErr: ErrInvalidInventoryNumber,
		},
		{
			name:    "inventory number has invalid sequence",
			change:  func(item *Item) { item.InventoryNumber = "SUP-CARBON-0" },
			wantErr: ErrInvalidInventoryNumber,
		},
		{
			name:    "hourly rate is not positive",
			change:  func(item *Item) { item.HourlyRateKopecks = 0 },
			wantErr: ErrInvalidItemHourlyRate,
		},
		{
			name:    "hourly rate cannot be split exactly",
			change:  func(item *Item) { item.HourlyRateKopecks = 50_001 },
			wantErr: ErrInexactHalfHourRate,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			item := validRentalItem(1)
			tt.change(&item)
			rental := Rental{Status: StatusDraft}
			err := rental.AddItem(item)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("AddItem() error = %v, want %v", err, tt.wantErr)
			}
			if rental.ItemCount() != 0 {
				t.Fatalf("ItemCount() = %d, want 0", rental.ItemCount())
			}
		})
	}
}

func TestRentalAddItemRejectsDuplicateEquipment(t *testing.T) {
	t.Parallel()

	rental := Rental{Status: StatusDraft}
	item := validRentalItem(1)
	if err := rental.AddItem(item); err != nil {
		t.Fatalf("AddItem() error = %v", err)
	}

	duplicate := item
	duplicate.InventoryNumber = "SUP-CARBON-2"
	err := rental.AddItem(duplicate)
	if !errors.Is(err, ErrEquipmentAlreadyAdded) {
		t.Fatalf("AddItem(duplicate) error = %v, want %v", err, ErrEquipmentAlreadyAdded)
	}
	if rental.ItemCount() != 1 {
		t.Fatalf("ItemCount() = %d, want 1", rental.ItemCount())
	}
}

func TestRentalRemoveItem(t *testing.T) {
	t.Parallel()

	rental := Rental{Status: StatusDraft}
	first := validRentalItem(1)
	second := validRentalItem(2)
	second.InventoryNumber = "SUP-CARBON-2"
	third := validRentalItem(3)
	third.InventoryNumber = "SUP-CARBON-3"
	for _, item := range []Item{first, second, third} {
		if err := rental.AddItem(item); err != nil {
			t.Fatalf("AddItem() error = %v", err)
		}
	}

	if err := rental.RemoveItem(second.EquipmentID); err != nil {
		t.Fatalf("RemoveItem() error = %v", err)
	}
	items := rental.Items()
	if len(items) != 2 || items[0] != first || items[1] != third {
		t.Fatalf("Items() = %#v, want first and third", items)
	}
}

func TestRentalRemoveItemRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		equipmentID int64
		wantErr     error
	}{
		{name: "invalid ID", equipmentID: 0, wantErr: ErrInvalidEquipmentID},
		{name: "not found", equipmentID: 2, wantErr: ErrRentalItemNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rental := Rental{Status: StatusDraft}
			if err := rental.AddItem(validRentalItem(1)); err != nil {
				t.Fatalf("AddItem() error = %v", err)
			}
			err := rental.RemoveItem(tt.equipmentID)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("RemoveItem() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestRentalCompositionIsLockedOutsideDraft(t *testing.T) {
	t.Parallel()

	for _, status := range []Status{StatusConfirmed, StatusActive, StatusCompleted, StatusCancelled} {
		status := status
		t.Run(string(status), func(t *testing.T) {
			t.Parallel()

			rental := Rental{Status: status, items: []Item{validRentalItem(1)}}
			if err := rental.AddItem(validRentalItem(2)); !errors.Is(err, ErrRentalCompositionLocked) {
				t.Errorf("AddItem() error = %v, want %v", err, ErrRentalCompositionLocked)
			}
			if err := rental.RemoveItem(1); !errors.Is(err, ErrRentalCompositionLocked) {
				t.Errorf("RemoveItem() error = %v, want %v", err, ErrRentalCompositionLocked)
			}
		})
	}
}

func TestRentalItemsReturnsCopy(t *testing.T) {
	t.Parallel()

	rental := Rental{Status: StatusDraft}
	item := validRentalItem(1)
	if err := rental.AddItem(item); err != nil {
		t.Fatalf("AddItem() error = %v", err)
	}

	items := rental.Items()
	items[0].InventoryNumber = "CHANGED"
	items = append(items, validRentalItem(2))

	got := rental.Items()
	if len(got) != 1 || got[0] != item {
		t.Fatalf("Items() = %#v, want unchanged %#v", got, []Item{item})
	}
}

func TestCopiedRentalCompositionChangesAreIndependent(t *testing.T) {
	t.Parallel()

	original := Rental{Status: StatusDraft}
	first := validRentalItem(1)
	second := validRentalItem(2)
	second.InventoryNumber = "SUP-CARBON-2"
	for _, item := range []Item{first, second} {
		if err := original.AddItem(item); err != nil {
			t.Fatalf("AddItem() error = %v", err)
		}
	}

	copied := original
	if err := copied.RemoveItem(first.EquipmentID); err != nil {
		t.Fatalf("copied.RemoveItem() error = %v", err)
	}
	third := validRentalItem(3)
	third.InventoryNumber = "SUP-CARBON-3"
	if err := copied.AddItem(third); err != nil {
		t.Fatalf("copied.AddItem() error = %v", err)
	}

	if got := original.Items(); len(got) != 2 || got[0] != first || got[1] != second {
		t.Fatalf("original.Items() = %#v, want unchanged first and second", got)
	}
}

func validRentalItem(equipmentID int64) Item {
	return Item{
		EquipmentID:       equipmentID,
		InventoryNumber:   "SUP-CARBON-1",
		Kind:              equipment.KindSUPBoard,
		ModelCode:         "CARBON",
		HourlyRateKopecks: 50_000,
	}
}
