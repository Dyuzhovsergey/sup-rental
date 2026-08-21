package dashboard

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type repositoryStub struct {
	snapshot func(context.Context, Query) (Snapshot, error)
}

func (s *repositoryStub) Snapshot(ctx context.Context, query Query) (Snapshot, error) {
	return s.snapshot(ctx, query)
}

func TestServiceSnapshotUsesMoscowDay(t *testing.T) {
	now := time.Date(2026, 8, 20, 22, 15, 0, 0, time.UTC)
	want := Snapshot{
		EquipmentTotal: 10, EquipmentAvailable: 4, EquipmentMaintenance: 2,
		EquipmentRetired: 1, EquipmentIssued: 3, RentalsActive: 2,
		RentalsOverdue: 1, RentalsStartingToday: 3, RentalsEndingToday: 4,
	}
	var gotQuery Query
	service := NewService(&repositoryStub{snapshot: func(_ context.Context, query Query) (Snapshot, error) {
		gotQuery = query
		return want, nil
	}})
	service.now = func() time.Time { return now }

	got, err := service.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if got != want {
		t.Fatalf("Snapshot() = %+v, want %+v", got, want)
	}
	if !gotQuery.Now.Equal(now) || gotQuery.DayStart.Format(time.RFC3339) != "2026-08-21T00:00:00+03:00" ||
		gotQuery.DayEnd.Format(time.RFC3339) != "2026-08-22T00:00:00+03:00" {
		t.Fatalf("query = %+v", gotQuery)
	}
}

func TestServiceSnapshotPreservesRepositoryError(t *testing.T) {
	repositoryError := errors.New("database unavailable")
	service := NewService(&repositoryStub{snapshot: func(context.Context, Query) (Snapshot, error) {
		return Snapshot{}, repositoryError
	}})
	if _, err := service.Snapshot(context.Background()); !errors.Is(err, repositoryError) {
		t.Fatalf("Snapshot() error = %v", err)
	}
}

func TestServiceSnapshotRejectsInconsistentCounts(t *testing.T) {
	tests := []struct {
		name     string
		snapshot Snapshot
		want     string
	}{
		{name: "negative", snapshot: Snapshot{EquipmentTotal: -1}, want: "negative count"},
		{name: "equipment sum", snapshot: Snapshot{EquipmentTotal: 2, EquipmentAvailable: 1}, want: "do not match"},
		{name: "overdue", snapshot: Snapshot{RentalsActive: 1, RentalsOverdue: 2}, want: "exceed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := NewService(&repositoryStub{snapshot: func(context.Context, Query) (Snapshot, error) {
				return test.snapshot, nil
			}})
			if _, err := service.Snapshot(context.Background()); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Snapshot() error = %v, want %q", err, test.want)
			}
		})
	}
}
