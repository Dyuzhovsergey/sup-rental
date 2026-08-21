package httpserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Dyuzhovsergey/sup-rental/internal/rental"
	"github.com/Dyuzhovsergey/sup-rental/internal/user"
)

func TestOperatorDashboardShowsMetricsRentalsAndProgress(t *testing.T) {
	location := time.FixedZone("test-msk", 3*60*60)
	now := time.Date(2026, 8, 20, 15, 0, 0, 0, location)
	confirmed := operatorMonitoringEntryFixture(
		t, 41, rental.StatusConfirmed, "Анна Петрова",
		now.Add(-30*time.Minute), now.Add(30*time.Minute),
		rental.MonitoringTiming{State: rental.MonitoringIssueDelayed, Percent: 50, Delta: 30 * time.Minute},
	)
	active := operatorMonitoringEntryFixture(
		t, 42, rental.StatusActive, "Иван Сидоров",
		now.Add(-30*time.Minute), now.Add(90*time.Minute),
		rental.MonitoringTiming{State: rental.MonitoringActive, Percent: 25, Delta: 90 * time.Minute},
	)
	overdue := operatorMonitoringEntryFixture(
		t, 43, rental.StatusActive, "Ольга Смирнова",
		now.Add(-2*time.Hour), now.Add(-30*time.Minute),
		rental.MonitoringTiming{State: rental.MonitoringOverdue, Percent: 100, Delta: 30 * time.Minute},
	)
	rentals := &rentalServiceStub{monitoring: func(context.Context) (rental.MonitoringSnapshot, error) {
		return rental.MonitoringSnapshot{
			GeneratedAt: now, TodayTotal: 4, ConfirmedTotal: 1, ActiveTotal: 2, OverdueTotal: 1,
			Confirmed: []rental.MonitoringEntry{confirmed},
			Active:    []rental.MonitoringEntry{overdue, active},
		}, nil
	}}
	handler := newRentalTestHandler(t, user.RoleOperator, rentals, rentalClientsStub())
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, rentalRequest(http.MethodGet, "/operator", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body %q", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, want := range []string{
		"20 августа 2026",
		"Аренды сегодня", ">4<", "Ожидают выдачи", "Активные", "Просрочены",
		"№41", "Анна Петрова", "Выдача задерживается на 30 мин",
		"№42", "Иван Сидоров", "Осталось 1 час 30 мин",
		"№43", "Ольга Смирнова", "Просрочена на 30 мин",
		"value=\"50\"", "aria-label=\"Плановый период аренды №43: 100%\"",
		"href=\"/rentals/new\"", "href=\"/rentals\"",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q", want)
		}
	}
	if strings.Contains(body, "обновите страницу") || strings.Contains(body, "Данные на") {
		t.Errorf("dashboard contains redundant freshness message")
	}
	for _, forbidden := range []string{"/issue", "/complete", "/cancel"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("read-only dashboard contains %q", forbidden)
		}
	}
}

func TestOperatorDashboardShowsEmptyStates(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	rentals := &rentalServiceStub{monitoring: func(context.Context) (rental.MonitoringSnapshot, error) {
		return rental.MonitoringSnapshot{GeneratedAt: now}, nil
	}}
	handler := newRentalTestHandler(t, user.RoleOperator, rentals, rentalClientsStub())
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, rentalRequest(http.MethodGet, "/operator", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body %q", response.Code, response.Body.String())
	}
	for _, want := range []string{"Выдач на сегодня нет", "Активных аренд нет"} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("body does not contain %q", want)
		}
	}
}

func TestOperatorDashboardHidesMonitoringError(t *testing.T) {
	internalError := errors.New("postgres password leaked")
	rentals := &rentalServiceStub{monitoring: func(context.Context) (rental.MonitoringSnapshot, error) {
		return rental.MonitoringSnapshot{}, internalError
	}}
	handler := newRentalTestHandler(t, user.RoleOperator, rentals, rentalClientsStub())
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, rentalRequest(http.MethodGet, "/operator", nil))

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
	if strings.Contains(response.Body.String(), internalError.Error()) {
		t.Fatalf("body leaks internal error: %q", response.Body.String())
	}
}

func TestMonitoringDurationLabel(t *testing.T) {
	tests := []struct {
		duration time.Duration
		want     string
	}{
		{duration: 20 * time.Second, want: "1 мин"},
		{duration: 2*time.Hour + 15*time.Minute, want: "2 часа 15 мин"},
		{duration: 25*time.Hour + time.Minute, want: "1 день 1 час 1 мин"},
	}
	for _, test := range tests {
		if got := monitoringDurationLabel(test.duration); got != test.want {
			t.Errorf("monitoringDurationLabel(%s) = %q, want %q", test.duration, got, test.want)
		}
	}
}

func operatorMonitoringEntryFixture(
	t *testing.T,
	id int64,
	status rental.Status,
	clientName string,
	start, end time.Time,
	timing rental.MonitoringTiming,
) rental.MonitoringEntry {
	t.Helper()
	interval, err := rental.NewInterval(start, end)
	if err != nil {
		t.Fatalf("NewInterval() error = %v", err)
	}
	return rental.MonitoringEntry{
		Summary: rental.Summary{
			ID: id, ClientID: id + 100, ClientName: clientName,
			Interval: interval, Status: status, ItemCount: 2, PlannedTotalKopecks: 100_000,
		},
		Timing: timing,
	}
}
