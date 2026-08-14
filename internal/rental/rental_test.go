package rental

import (
	"errors"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.August, 14, 10, 0, 0, 0, time.UTC)
	interval := mustInterval(t, start, start.Add(time.Hour))

	rental, err := New(42, interval)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if rental.ID != 0 {
		t.Errorf("ID = %d, want 0", rental.ID)
	}
	if rental.ClientID != 42 {
		t.Errorf("ClientID = %d, want 42", rental.ClientID)
	}
	if rental.Interval != interval {
		t.Errorf("Interval = %v, want %v", rental.Interval, interval)
	}
	if rental.Status != StatusDraft {
		t.Errorf("Status = %q, want %q", rental.Status, StatusDraft)
	}
}

func TestNewRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.August, 14, 10, 0, 0, 0, time.UTC)
	interval := mustInterval(t, start, start.Add(time.Hour))

	tests := []struct {
		name     string
		clientID int64
		interval Interval
		wantErr  error
	}{
		{
			name:     "zero client ID",
			clientID: 0,
			interval: interval,
			wantErr:  ErrInvalidClientID,
		},
		{
			name:     "negative client ID",
			clientID: -1,
			interval: interval,
			wantErr:  ErrInvalidClientID,
		},
		{
			name:     "zero interval",
			clientID: 42,
			interval: Interval{},
			wantErr:  ErrStartTimeRequired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := New(tt.clientID, tt.interval)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("New() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestRestore(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.August, 14, 10, 0, 0, 0, time.UTC)
	interval := mustInterval(t, start, start.Add(time.Hour))
	items := []Item{validRentalItem(1)}

	restored, err := Restore(7, 42, interval, StatusConfirmed, items)
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	items[0].InventoryNumber = "CHANGED"
	if restored.ID != 7 || restored.ClientID != 42 || restored.Interval != interval ||
		restored.Status != StatusConfirmed || restored.ItemCount() != 1 ||
		restored.Items()[0].InventoryNumber != "SUP-CARBON-1" {
		t.Fatalf("Restore() = %#v, items = %#v", restored, restored.Items())
	}
}

func TestRestoreRejectsInvalidData(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.August, 14, 10, 0, 0, 0, time.UTC)
	interval := mustInterval(t, start, start.Add(time.Hour))
	validItems := []Item{validRentalItem(1)}
	duplicateItems := []Item{validRentalItem(1), validRentalItem(1)}

	tests := []struct {
		name     string
		id       int64
		clientID int64
		interval Interval
		status   Status
		items    []Item
		wantErr  error
	}{
		{name: "invalid rental ID", clientID: 42, interval: interval, status: StatusDraft, wantErr: ErrInvalidRentalID},
		{name: "invalid client ID", id: 7, interval: interval, status: StatusDraft, wantErr: ErrInvalidClientID},
		{name: "invalid interval", id: 7, clientID: 42, status: StatusDraft, wantErr: ErrStartTimeRequired},
		{name: "invalid status", id: 7, clientID: 42, interval: interval, status: Status("unknown"), wantErr: ErrInvalidStatus},
		{name: "confirmed without items", id: 7, clientID: 42, interval: interval, status: StatusConfirmed, wantErr: ErrRentalItemsRequired},
		{name: "active without items", id: 7, clientID: 42, interval: interval, status: StatusActive, wantErr: ErrRentalItemsRequired},
		{name: "completed without items", id: 7, clientID: 42, interval: interval, status: StatusCompleted, wantErr: ErrRentalItemsRequired},
		{name: "duplicate equipment", id: 7, clientID: 42, interval: interval, status: StatusDraft, items: duplicateItems, wantErr: ErrEquipmentAlreadyAdded},
		{name: "invalid item", id: 7, clientID: 42, interval: interval, status: StatusDraft, items: []Item{{}}, wantErr: ErrInvalidEquipmentID},
		{name: "valid cancelled without items", id: 7, clientID: 42, interval: interval, status: StatusCancelled},
		{name: "valid draft with items", id: 7, clientID: 42, interval: interval, status: StatusDraft, items: validItems},
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

func TestStatusValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status Status
		want   bool
	}{
		{status: StatusDraft, want: true},
		{status: StatusConfirmed, want: true},
		{status: StatusActive, want: true},
		{status: StatusCompleted, want: true},
		{status: StatusCancelled, want: true},
		{status: Status("unknown"), want: false},
		{status: Status(""), want: false},
	}

	for _, tt := range tests {
		if got := tt.status.Valid(); got != tt.want {
			t.Errorf("Status(%q).Valid() = %t, want %t", tt.status, got, tt.want)
		}
	}
}

func TestStatusCanTransitionTo(t *testing.T) {
	t.Parallel()

	allowed := map[Status]map[Status]bool{
		StatusDraft: {
			StatusConfirmed: true,
			StatusCancelled: true,
		},
		StatusConfirmed: {
			StatusActive:    true,
			StatusCancelled: true,
		},
		StatusActive: {
			StatusCompleted: true,
		},
	}
	statuses := []Status{
		StatusDraft,
		StatusConfirmed,
		StatusActive,
		StatusCompleted,
		StatusCancelled,
	}

	for _, from := range statuses {
		for _, to := range statuses {
			want := allowed[from][to]
			if got := from.CanTransitionTo(to); got != want {
				t.Errorf("Status(%q).CanTransitionTo(%q) = %t, want %t", from, to, got, want)
			}
		}
	}
}

func TestRentalChangeStatus(t *testing.T) {
	t.Parallel()

	rental := Rental{Status: StatusDraft}
	if err := rental.AddItem(validRentalItem(1)); err != nil {
		t.Fatalf("AddItem() error = %v", err)
	}
	if err := rental.ChangeStatus(StatusConfirmed); err != nil {
		t.Fatalf("ChangeStatus() error = %v", err)
	}
	if rental.Status != StatusConfirmed {
		t.Fatalf("Status = %q, want %q", rental.Status, StatusConfirmed)
	}
}

func TestRentalChangeStatusRequiresItemsForConfirmation(t *testing.T) {
	t.Parallel()

	rental := Rental{Status: StatusDraft}
	err := rental.ChangeStatus(StatusConfirmed)
	if !errors.Is(err, ErrRentalItemsRequired) {
		t.Fatalf("ChangeStatus() error = %v, want %v", err, ErrRentalItemsRequired)
	}
	if rental.Status != StatusDraft {
		t.Fatalf("Status = %q, want unchanged %q", rental.Status, StatusDraft)
	}
}

func TestRentalChangeStatusAllowsEmptyDraftCancellation(t *testing.T) {
	t.Parallel()

	rental := Rental{Status: StatusDraft}
	if err := rental.ChangeStatus(StatusCancelled); err != nil {
		t.Fatalf("ChangeStatus() error = %v", err)
	}
	if rental.Status != StatusCancelled {
		t.Fatalf("Status = %q, want %q", rental.Status, StatusCancelled)
	}
}

func TestRentalChangeStatusRejectsInvalidTransition(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		current Status
		target  Status
		wantErr error
	}{
		{
			name:    "transition is forbidden",
			current: StatusActive,
			target:  StatusCancelled,
			wantErr: ErrStatusTransitionNotAllowed,
		},
		{
			name:    "completed is terminal",
			current: StatusCompleted,
			target:  StatusActive,
			wantErr: ErrStatusTransitionNotAllowed,
		},
		{
			name:    "cancelled is terminal",
			current: StatusCancelled,
			target:  StatusDraft,
			wantErr: ErrStatusTransitionNotAllowed,
		},
		{
			name:    "unknown target",
			current: StatusDraft,
			target:  Status("unknown"),
			wantErr: ErrInvalidStatus,
		},
		{
			name:    "unknown current status",
			current: Status("unknown"),
			target:  StatusDraft,
			wantErr: ErrInvalidStatus,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rental := Rental{Status: tt.current}
			err := rental.ChangeStatus(tt.target)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ChangeStatus() error = %v, want %v", err, tt.wantErr)
			}
			if rental.Status != tt.current {
				t.Errorf("Status = %q, want unchanged %q", rental.Status, tt.current)
			}
		})
	}
}
