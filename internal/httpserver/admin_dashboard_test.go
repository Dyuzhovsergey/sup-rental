package httpserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Dyuzhovsergey/sup-rental/internal/dashboard"
	"github.com/Dyuzhovsergey/sup-rental/internal/session"
	"github.com/Dyuzhovsergey/sup-rental/internal/user"
)

func TestAdminDashboardShowsActualMetricsAndNavigation(t *testing.T) {
	service := &adminDashboardServiceStub{snapshot: func(context.Context) (dashboard.Snapshot, error) {
		return dashboard.Snapshot{
			EquipmentTotal: 12, EquipmentAvailable: 5, EquipmentMaintenance: 2,
			EquipmentRetired: 1, EquipmentIssued: 4, RentalsActive: 3,
			RentalsOverdue: 1, RentalsStartingToday: 2, RentalsEndingToday: 4,
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
		`href="/admin" aria-current="page"`,
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
	service := &adminDashboardServiceStub{snapshot: func(context.Context) (dashboard.Snapshot, error) {
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
