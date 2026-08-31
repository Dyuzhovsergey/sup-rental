package httpserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Dyuzhovsergey/sup-rental/internal/dashboard"
	"github.com/Dyuzhovsergey/sup-rental/internal/session"
	"github.com/Dyuzhovsergey/sup-rental/internal/user"
)

func TestAdminDashboardShowsActualMetricsAndNavigation(t *testing.T) {
	service := &adminDashboardServiceStub{snapshot: func(_ context.Context, period dashboard.FinancialPeriod) (dashboard.Snapshot, error) {
		if !period.Valid() || period.End.Sub(period.Start) != 24*time.Hour {
			t.Fatalf("period = %+v", period)
		}
		return dashboard.Snapshot{
			EquipmentTotal: 12, EquipmentAvailable: 5, EquipmentMaintenance: 2,
			EquipmentRetired: 1, EquipmentIssued: 4, RentalsActive: 3,
			RentalsOverdue: 1, RentalsStartingToday: 2, RentalsEndingToday: 4,
			PaymentsBaseKopecks: 1_200_000, PaymentsOverdueKopecks: 50_000,
			PaymentsRefundKopecks: 1_500_000, PaymentsNetKopecks: -250_000,
		}, nil
	}}
	response := httptest.NewRecorder()
	newAdminDashboardTestHandler(t, service, user.RoleAdmin).ServeHTTP(
		response, authenticatedRequest(http.MethodGet, "/admin", ""),
	)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body %q", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, want := range []string{
		"Панель администратора", "Оборудование", "Всего единиц", ">12<",
		"Доступно", ">5<", "На обслуживании", "Выдано", "Списано",
		"Аренды", "Активные", "Просроченные", "Начинаются сегодня",
		"Завершаются сегодня", `href="/equipment"`, `href="/rentals"`,
		"Финансы сегодня", "Фактически полученные и возвращённые средства",
		"Основные оплаты", "12 000 ₽", "Получено при создании аренд",
		"Доплаты за просрочку", "500 ₽", "Получено при завершении",
		"Возвраты", "15 000 ₽", "Возвращено при отмене",
		"Итого за сегодня", "−2 500 ₽", "Оплаты &#43; доплаты − возвраты",
		`admin-finance-metric--danger`,
		`href="/admin/payments?period=today"`, "Открыть операции", "Платежи",
		"Готовые финансовые периоды", "Сегодня", "Вчера", "7 дней",
		`aria-current="true"`, `name="period" value="custom"`,
		`<fieldset class="finance-period-filter__range">`, "Произвольный период",
		`<label class="visually-hidden" for="finance-period-from">Дата начала</label>`,
		`<label class="visually-hidden" for="finance-period-to">Дата окончания</label>`,
		`class="finance-period-filter__separator" aria-hidden="true"`,
		`href="/admin" aria-current="page"`,
		`class="app-theme-control"`, `data-theme-toggle`,
		`data-theme-icon="light"`, `data-theme-icon="dark"`,
		`aria-label="Включить тёмную тему"`,
		`data-mobile-nav-open`, `aria-controls="app-navigation"`,
		`aria-expanded="false"`, `data-mobile-nav`, `data-mobile-nav-close`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q", want)
		}
	}
	for _, forbidden := range []string{"Состояние приложения", "PostgreSQL", "health"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("body contains technical information %q", forbidden)
		}
	}
}

func TestAdminMoneyLabel(t *testing.T) {
	tests := []struct {
		name    string
		kopecks int64
		want    string
	}{
		{name: "zero", kopecks: 0, want: "0 ₽"},
		{name: "whole rubles", kopecks: 1_234_500, want: "12 345 ₽"},
		{name: "kopecks", kopecks: 1_250_050, want: "12 500,50 ₽"},
		{name: "negative", kopecks: -150_000, want: "−1 500 ₽"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := adminMoneyLabel(test.kopecks); got != test.want {
				t.Fatalf("adminMoneyLabel(%d) = %q, want %q", test.kopecks, got, test.want)
			}
		})
	}
}

func TestAdminDashboardRequiresAdminRole(t *testing.T) {
	response := httptest.NewRecorder()
	newAdminDashboardTestHandler(t, &adminDashboardServiceStub{}, user.RoleOperator).ServeHTTP(
		response, authenticatedRequest(http.MethodGet, "/admin", ""),
	)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
	}
}

func TestAdminDashboardHidesInternalError(t *testing.T) {
	internalError := errors.New("postgres password leaked")
	service := &adminDashboardServiceStub{snapshot: func(context.Context, dashboard.FinancialPeriod) (dashboard.Snapshot, error) {
		return dashboard.Snapshot{}, internalError
	}}
	response := httptest.NewRecorder()
	newAdminDashboardTestHandler(t, service, user.RoleAdmin).ServeHTTP(
		response, authenticatedRequest(http.MethodGet, "/admin", ""),
	)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
	if strings.Contains(response.Body.String(), internalError.Error()) {
		t.Fatalf("body leaks internal error: %q", response.Body.String())
	}
}

func TestAdminDashboardRejectsUnsupportedMethod(t *testing.T) {
	response := httptest.NewRecorder()
	newAdminDashboardTestHandler(t, &adminDashboardServiceStub{}, user.RoleAdmin).ServeHTTP(
		response, authenticatedRequest(http.MethodPost, "/admin", ""),
	)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}
}

func TestAdminDashboardFiltersOnlyFinanceByCustomPeriod(t *testing.T) {
	service := &adminDashboardServiceStub{snapshot: func(_ context.Context, period dashboard.FinancialPeriod) (dashboard.Snapshot, error) {
		if period.Start.Format(time.RFC3339) != "2026-08-01T00:00:00+03:00" ||
			period.End.Format(time.RFC3339) != "2026-09-01T00:00:00+03:00" {
			t.Fatalf("period = %+v", period)
		}
		return dashboard.Snapshot{}, nil
	}}
	response := httptest.NewRecorder()
	newAdminDashboardTestHandler(t, service, user.RoleAdmin).ServeHTTP(
		response, authenticatedRequest(http.MethodGet, "/admin?period=custom&from=2026-08-01&to=2026-08-31", ""),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body %q", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, want := range []string{"Финансы за 01.08.2026–31.08.2026", "Итого за период", "Начинаются сегодня", `href="/admin/payments?from=2026-08-01&amp;period=custom&amp;to=2026-08-31"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q", want)
		}
	}
}

func TestAdminDashboardShowsPeriodValidationError(t *testing.T) {
	response := httptest.NewRecorder()
	newAdminDashboardTestHandler(t, &adminDashboardServiceStub{}, user.RoleAdmin).ServeHTTP(
		response, authenticatedRequest(http.MethodGet, "/admin?period=custom&from=2026-08-10&to=2026-08-08", ""),
	)
	if response.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(response.Body.String(), "Дата начала не может быть позже даты окончания.") {
		t.Fatalf("status = %d body %q", response.Code, response.Body.String())
	}
}

func newAdminDashboardTestHandler(t *testing.T, service adminDashboardService, role user.Role) http.Handler {
	t.Helper()
	authenticated := authenticatedFixture()
	authenticated.User.Role = role
	resolver := &sessionResolverStub{resolve: func(context.Context, string) (session.AuthenticatedSession, error) {
		return authenticated, nil
	}}
	handler, err := NewHandler(
		discardLogger(), &equipmentServiceStub{}, &authServiceStub{}, resolver,
		&operatorServiceStub{}, &auditServiceStub{}, &clientServiceStub{},
		&rentalServiceStub{}, service, CookieSettings{},
	)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	return handler
}
