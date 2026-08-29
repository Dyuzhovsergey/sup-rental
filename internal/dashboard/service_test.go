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
	payments func(context.Context, PaymentOperationsQuery) (PaymentOperationsPage, error)
}

func (s *repositoryStub) Snapshot(ctx context.Context, query Query) (Snapshot, error) {
	return s.snapshot(ctx, query)
}

func (s *repositoryStub) PaymentOperations(ctx context.Context, query PaymentOperationsQuery) (PaymentOperationsPage, error) {
	if s.payments == nil {
		return PaymentOperationsPage{}, nil
	}
	return s.payments(ctx, query)
}

func TestServiceSnapshotUsesMoscowDay(t *testing.T) {
	now := time.Date(2026, 8, 20, 22, 15, 0, 0, time.UTC)
	want := Snapshot{
		EquipmentTotal: 10, EquipmentAvailable: 4, EquipmentMaintenance: 2,
		EquipmentRetired: 1, EquipmentIssued: 3, RentalsActive: 2,
		RentalsOverdue: 1, RentalsStartingToday: 3, RentalsEndingToday: 4,
		PaymentsBaseTodayKopecks: 120_000, PaymentsOverdueTodayKopecks: 30_000,
		PaymentsRefundTodayKopecks: 20_000, PaymentsNetTodayKopecks: 130_000,
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
		{name: "negative payment", snapshot: Snapshot{PaymentsBaseTodayKopecks: -1, PaymentsNetTodayKopecks: -1}, want: "negative count"},
		{name: "payment net", snapshot: Snapshot{PaymentsBaseTodayKopecks: 100, PaymentsNetTodayKopecks: 99}, want: "payment net"},
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

func TestServicePaymentOperationsUsesMoscowDayAndPagination(t *testing.T) {
	now := time.Date(2026, 8, 20, 22, 15, 0, 0, time.UTC)
	want := PaymentOperationsPage{Total: 7}
	var gotQuery PaymentOperationsQuery
	service := NewService(&repositoryStub{
		snapshot: func(context.Context, Query) (Snapshot, error) { return Snapshot{}, nil },
		payments: func(_ context.Context, query PaymentOperationsQuery) (PaymentOperationsPage, error) {
			gotQuery = query
			return want, nil
		},
	})
	service.now = func() time.Time { return now }

	got, err := service.PaymentOperations(context.Background(), 2, 5)
	if err != nil {
		t.Fatalf("PaymentOperations() error = %v", err)
	}
	if got.Total != want.Total || got.Page != 2 || got.PageSize != 5 {
		t.Fatalf("PaymentOperations() = %+v", got)
	}
	if gotQuery.DayStart.Format(time.RFC3339) != "2026-08-21T00:00:00+03:00" ||
		gotQuery.DayEnd.Format(time.RFC3339) != "2026-08-22T00:00:00+03:00" ||
		gotQuery.Page != 2 || gotQuery.PageSize != 5 {
		t.Fatalf("query = %+v", gotQuery)
	}
}

func TestServicePaymentOperationsRejectsInvalidPagination(t *testing.T) {
	service := NewService(&repositoryStub{
		snapshot: func(context.Context, Query) (Snapshot, error) { return Snapshot{}, nil },
	})
	for _, input := range []struct{ page, pageSize int }{{0, 5}, {1, 0}, {1, 20}} {
		if _, err := service.PaymentOperations(context.Background(), input.page, input.pageSize); !errors.Is(err, ErrInvalidPaymentPagination) {
			t.Fatalf("PaymentOperations(%d, %d) error = %v", input.page, input.pageSize, err)
		}
	}
}

func TestServicePaymentOperationsPreservesRepositoryErrorAndRejectsNegativeTotal(t *testing.T) {
	repositoryError := errors.New("database unavailable")
	service := NewService(&repositoryStub{
		snapshot: func(context.Context, Query) (Snapshot, error) { return Snapshot{}, nil },
		payments: func(context.Context, PaymentOperationsQuery) (PaymentOperationsPage, error) {
			return PaymentOperationsPage{}, repositoryError
		},
	})
	if _, err := service.PaymentOperations(context.Background(), 1, 10); !errors.Is(err, repositoryError) {
		t.Fatalf("PaymentOperations() error = %v", err)
	}

	service.repository = &repositoryStub{
		snapshot: func(context.Context, Query) (Snapshot, error) { return Snapshot{}, nil },
		payments: func(context.Context, PaymentOperationsQuery) (PaymentOperationsPage, error) {
			return PaymentOperationsPage{Total: -1}, nil
		},
	}
	if _, err := service.PaymentOperations(context.Background(), 1, 10); err == nil || !strings.Contains(err.Error(), "negative total") {
		t.Fatalf("PaymentOperations() error = %v", err)
	}
}
