package rental

import (
	"errors"
	"testing"
	"time"
)

func TestNewCreatesConfirmedRental(t *testing.T) {
	t.Parallel()

	interval := mustInterval(t, time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC), time.Date(2026, 8, 14, 11, 0, 0, 0, time.UTC))
	items := []Item{validRentalItem(1)}
	value, err := New(42, interval, items)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	items[0].InventoryNumber = "CHANGED"
	if value.ID != 0 || value.ClientID != 42 || value.Status != StatusConfirmed ||
		value.ItemCount() != 1 || value.Items()[0].InventoryNumber != "SUP-CARBON-1" {
		t.Fatalf("New() = %#v items %#v", value, value.Items())
	}
}

func TestNewRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)
	interval := mustInterval(t, start, start.Add(time.Hour))
	tests := []struct {
		name     string
		clientID int64
		interval Interval
		items    []Item
		wantErr  error
	}{
		{name: "invalid client", interval: interval, items: []Item{validRentalItem(1)}, wantErr: ErrInvalidClientID},
		{name: "invalid interval", clientID: 42, items: []Item{validRentalItem(1)}, wantErr: ErrStartTimeRequired},
		{name: "empty composition", clientID: 42, interval: interval, wantErr: ErrRentalItemsRequired},
		{name: "duplicate equipment", clientID: 42, interval: interval, items: []Item{validRentalItem(1), validRentalItem(1)}, wantErr: ErrEquipmentAlreadyAdded},
		{name: "invalid item", clientID: 42, interval: interval, items: []Item{{}}, wantErr: ErrInvalidEquipmentID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := New(tt.clientID, tt.interval, tt.items)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("New() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestRestore(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)
	interval := mustInterval(t, start, start.Add(time.Hour))
	items := []Item{validRentalItem(1)}
	restored, err := Restore(7, 42, interval, StatusActive, items)
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	items[0].InventoryNumber = "CHANGED"
	if restored.ID != 7 || restored.Status != StatusActive || restored.Items()[0].InventoryNumber != "SUP-CARBON-1" {
		t.Fatalf("Restore() = %#v items %#v", restored, restored.Items())
	}
}

func TestRestoreRejectsInvalidData(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC)
	interval := mustInterval(t, start, start.Add(time.Hour))
	items := []Item{validRentalItem(1)}
	tests := []struct {
		name     string
		id       int64
		clientID int64
		interval Interval
		status   Status
		items    []Item
		wantErr  error
	}{
		{name: "invalid ID", clientID: 42, interval: interval, status: StatusConfirmed, items: items, wantErr: ErrInvalidRentalID},
		{name: "invalid client", id: 7, interval: interval, status: StatusConfirmed, items: items, wantErr: ErrInvalidClientID},
		{name: "invalid interval", id: 7, clientID: 42, status: StatusConfirmed, items: items, wantErr: ErrStartTimeRequired},
		{name: "invalid status", id: 7, clientID: 42, interval: interval, status: Status("draft"), items: items, wantErr: ErrInvalidStatus},
		{name: "empty composition", id: 7, clientID: 42, interval: interval, status: StatusCancelled, wantErr: ErrRentalItemsRequired},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := Restore(tt.id, tt.clientID, tt.interval, tt.status, tt.items)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Restore() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestStatusRules(t *testing.T) {
	t.Parallel()

	statuses := []Status{StatusConfirmed, StatusActive, StatusCompleted, StatusCancelled}
	allowed := map[Status]map[Status]bool{
		StatusConfirmed: {StatusActive: true, StatusCancelled: true},
		StatusActive:    {StatusCompleted: true},
	}
	for _, status := range statuses {
		if !status.Valid() {
			t.Errorf("Status(%q).Valid() = false", status)
		}
		for _, target := range statuses {
			if got := status.CanTransitionTo(target); got != allowed[status][target] {
				t.Errorf("%q -> %q = %t", status, target, got)
			}
		}
	}
	if Status("draft").Valid() || Status("unknown").Valid() {
		t.Error("removed or unknown status is valid")
	}
}

func TestRentalChangeStatus(t *testing.T) {
	t.Parallel()

	value := Rental{Status: StatusConfirmed, items: []Item{validRentalItem(1)}}
	if err := value.ChangeStatus(StatusActive); err != nil || value.Status != StatusActive {
		t.Fatalf("ChangeStatus(active) = %q, %v", value.Status, err)
	}
	if err := value.ChangeStatus(StatusCancelled); !errors.Is(err, ErrStatusTransitionNotAllowed) {
		t.Fatalf("ChangeStatus(cancelled) error = %v", err)
	}
	if err := value.ChangeStatus(Status("draft")); !errors.Is(err, ErrInvalidStatus) {
		t.Fatalf("ChangeStatus(draft) error = %v", err)
	}
}
