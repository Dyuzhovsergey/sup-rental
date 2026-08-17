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
	issuedAt := start.Add(5 * time.Minute)
	restored, err := Restore(7, 42, interval, StatusActive, &issuedAt, items)
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	items[0].InventoryNumber = "CHANGED"
	gotIssuedAt, ok := restored.IssuedAt()
	issuedAt = issuedAt.Add(time.Hour)
	if restored.ID != 7 || restored.Status != StatusActive || !ok || !gotIssuedAt.Equal(start.Add(5*time.Minute)) || restored.Items()[0].InventoryNumber != "SUP-CARBON-1" {
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
		issuedAt *time.Time
		items    []Item
		wantErr  error
	}{
		{name: "invalid ID", clientID: 42, interval: interval, status: StatusConfirmed, items: items, wantErr: ErrInvalidRentalID},
		{name: "invalid client", id: 7, interval: interval, status: StatusConfirmed, items: items, wantErr: ErrInvalidClientID},
		{name: "invalid interval", id: 7, clientID: 42, status: StatusConfirmed, items: items, wantErr: ErrStartTimeRequired},
		{name: "invalid status", id: 7, clientID: 42, interval: interval, status: Status("draft"), items: items, wantErr: ErrInvalidStatus},
		{name: "empty composition", id: 7, clientID: 42, interval: interval, status: StatusCancelled, wantErr: ErrRentalItemsRequired},
		{name: "active without issued time", id: 7, clientID: 42, interval: interval, status: StatusActive, items: items, wantErr: ErrIssuedAtRequired},
		{name: "confirmed with issued time", id: 7, clientID: 42, interval: interval, status: StatusConfirmed, issuedAt: &start, items: items, wantErr: ErrUnexpectedIssuedAt},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := Restore(tt.id, tt.clientID, tt.interval, tt.status, tt.issuedAt, tt.items)
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
	if err := value.ChangeStatus(StatusActive); !errors.Is(err, ErrIssuedAtRequired) || value.Status != StatusConfirmed {
		t.Fatalf("ChangeStatus(active) = %q, %v", value.Status, err)
	}
	issuedAt := time.Date(2026, 8, 14, 9, 55, 0, 0, time.UTC)
	if err := value.Issue(issuedAt); err != nil || value.Status != StatusActive {
		t.Fatalf("Issue() = %q, %v", value.Status, err)
	}
	if err := value.ChangeStatus(StatusCancelled); !errors.Is(err, ErrStatusTransitionNotAllowed) {
		t.Fatalf("ChangeStatus(cancelled) error = %v", err)
	}
	if err := value.ChangeStatus(Status("draft")); !errors.Is(err, ErrInvalidStatus) {
		t.Fatalf("ChangeStatus(draft) error = %v", err)
	}
}

func TestRentalIssueRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name   string
		status Status
		at     time.Time
		want   error
	}{
		{name: "zero time", status: StatusConfirmed, want: ErrIssuedAtRequired},
		{name: "already active", status: StatusActive, at: time.Now(), want: ErrStatusTransitionNotAllowed},
		{name: "cancelled", status: StatusCancelled, at: time.Now(), want: ErrStatusTransitionNotAllowed},
		{name: "invalid status", status: Status("draft"), at: time.Now(), want: ErrInvalidStatus},
	} {
		t.Run(tt.name, func(t *testing.T) {
			value := Rental{Status: tt.status, items: []Item{validRentalItem(1)}}
			if err := value.Issue(tt.at); !errors.Is(err, tt.want) {
				t.Fatalf("Issue() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestRentalCancel(t *testing.T) {
	t.Parallel()

	value := Rental{Status: StatusConfirmed, items: []Item{validRentalItem(1)}}
	if err := value.Cancel(); err != nil || value.Status != StatusCancelled {
		t.Fatalf("Cancel() = %q, %v", value.Status, err)
	}
	if err := value.Cancel(); !errors.Is(err, ErrStatusTransitionNotAllowed) {
		t.Fatalf("second Cancel() error = %v", err)
	}
	active := Rental{Status: StatusActive, items: []Item{validRentalItem(1)}}
	if err := active.Cancel(); !errors.Is(err, ErrStatusTransitionNotAllowed) {
		t.Fatalf("active Cancel() error = %v", err)
	}
}
