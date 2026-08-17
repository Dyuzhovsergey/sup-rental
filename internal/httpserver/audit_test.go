package httpserver

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Dyuzhovsergey/sup-rental/internal/audit"
	"github.com/Dyuzhovsergey/sup-rental/internal/session"
	"github.com/Dyuzhovsergey/sup-rental/internal/user"
)

func TestAuditPageShowsSafeEventsAndFilters(t *testing.T) {
	login := "admin"
	service := &auditServiceStub{list: func(_ context.Context, actor user.User, filter audit.Filter) (audit.Page, error) {
		if actor.Login != "admin" || filter.Category != audit.CategoryEquipment || filter.Page != 1 {
			t.Errorf("List() actor = %+v, filter = %+v", actor, filter)
		}
		return audit.Page{Total: 1, Page: 1, Events: []audit.Event{{
			OccurredAt: time.Date(2026, time.August, 12, 7, 30, 0, 0, time.UTC),
			ActorLogin: &login, Action: "equipment.updated", TargetLabel: "SUP-001",
			Result:  audit.ResultSuccess,
			Details: []byte(`{"before":{"inventory_number":"SUP-001","kind":"sup_board","status":"available"},"after":{"inventory_number":"SUP-002","kind":"paddle","status":"maintenance"}}`),
		}}}, nil
	}}
	request := authenticatedRequest(http.MethodGet, "/admin/audit?category=equipment&result=success", "")
	response := httptest.NewRecorder()

	newAuditTestHandler(t, service, authenticatedFixture()).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	for _, want := range []string{"Журнал действий", "12.08.2026 10:30:00", "Оборудование изменено", "SUP-001", "Номер: SUP-001 → SUP-002", "Страница 1 из 1"} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("body does not contain %q", want)
		}
	}
}

func TestAuditPageHidesUnknownDetails(t *testing.T) {
	service := &auditServiceStub{list: func(context.Context, user.User, audit.Filter) (audit.Page, error) {
		return audit.Page{Total: 1, Page: 1, Events: []audit.Event{{
			Action: "future.action", TargetLabel: "target", Result: audit.ResultFailure,
			Details: []byte(`{"password":"must-not-appear"}`),
		}}}, nil
	}}
	request := authenticatedRequest(http.MethodGet, "/admin/audit", "")
	response := httptest.NewRecorder()
	newAuditTestHandler(t, service, authenticatedFixture()).ServeHTTP(response, request)

	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "must-not-appear") {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
}

func TestAuditPageShowsEquipmentBatchSummary(t *testing.T) {
	service := &auditServiceStub{list: func(context.Context, user.User, audit.Filter) (audit.Page, error) {
		return audit.Page{Total: 1, Page: 1, Events: []audit.Event{{
			Action: "equipment.batch_created", TargetLabel: "PADDLE-CARBON-1 — PADDLE-CARBON-3", Result: audit.ResultSuccess,
			Details: []byte(`{"batch":{"kind":"paddle","model_code":"CARBON","hourly_rate_kopecks":35000,"quantity":3,"first_inventory_number":"PADDLE-CARBON-1","last_inventory_number":"PADDLE-CARBON-3"}}`),
		}}}, nil
	}}
	response := httptest.NewRecorder()
	newAuditTestHandler(t, service, authenticatedFixture()).ServeHTTP(response, authenticatedRequest(http.MethodGet, "/admin/audit", ""))
	for _, want := range []string{"Партия оборудования добавлена", "модель: CARBON", "тариф: 350 ₽/час", "количество: 3", "PADDLE-CARBON-1 — PADDLE-CARBON-3"} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("body does not contain %q", want)
		}
	}
}

func TestAuditPageShowsEquipmentModelAndRateChanges(t *testing.T) {
	login := "admin"
	service := &auditServiceStub{list: func(context.Context, user.User, audit.Filter) (audit.Page, error) {
		return audit.Page{Page: 1, Total: 2, Events: []audit.Event{
			{
				Action: "equipment.model_changed", ActorLogin: &login,
				TargetLabel: "VEST-TOURING-3", Result: audit.ResultSuccess,
				Details: []byte(`{"before":{"inventory_number":"PADDLE-CARBON-1","kind":"paddle","model_code":"CARBON","hourly_rate_kopecks":35000,"status":"available"},"after":{"inventory_number":"VEST-TOURING-3","kind":"life_jacket","model_code":"TOURING","hourly_rate_kopecks":25000,"status":"available"}}`),
			},
			{
				Action: "equipment.model_rate_changed", ActorLogin: &login,
				TargetLabel: "VEST-TOURING", Result: audit.ResultSuccess,
				Details: []byte(`{"model_rate":{"kind":"life_jacket","model_code":"TOURING","before_kopecks":25000,"after_kopecks":30000,"affected_items":3}}`),
			},
		}}, nil
	}}
	handler := newAuditTestHandler(t, service, authenticatedFixture())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, authenticatedRequest(http.MethodGet, "/admin/audit", ""))
	for _, want := range []string{
		"Модель оборудования изменена", "PADDLE-CARBON-1 → VEST-TOURING-3",
		"Модель: CARBON → TOURING", "Тариф модели оборудования изменён",
		"250 ₽/час → 300 ₽/час", "затронуто: 3 единицы",
	} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("body does not contain %q", want)
		}
	}
}

func TestAuditPageShowsClientCategoryAndAction(t *testing.T) {
	service := &auditServiceStub{list: func(_ context.Context, _ user.User, filter audit.Filter) (audit.Page, error) {
		if filter.Category != audit.CategoryClients {
			t.Errorf("category = %q", filter.Category)
		}
		return audit.Page{Total: 1, Page: 1, Events: []audit.Event{{
			Action: "client.created", TargetLabel: "Анна Иванова", Result: audit.ResultSuccess,
		}}}, nil
	}}
	response := httptest.NewRecorder()
	newAuditTestHandler(t, service, authenticatedFixture()).ServeHTTP(response, authenticatedRequest(http.MethodGet, "/admin/audit?category=clients", ""))
	for _, want := range []string{`value="clients" selected`, "Клиенты", "Клиент создан", "Анна Иванова"} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("body does not contain %q", want)
		}
	}
}

func TestAuditPageShowsSafeClientUpdateSummary(t *testing.T) {
	service := &auditServiceStub{list: func(context.Context, user.User, audit.Filter) (audit.Page, error) {
		return audit.Page{Total: 1, Page: 1, Events: []audit.Event{{
			Action: "client.updated", TargetLabel: "Анна Петрова", Result: audit.ResultSuccess,
			Details: []byte(`{"before_full_name":"Анна Иванова","after_full_name":"Анна Петрова","phone_changed":true}`),
		}}}, nil
	}}
	response := httptest.NewRecorder()
	newAuditTestHandler(t, service, authenticatedFixture()).ServeHTTP(
		response, authenticatedRequest(http.MethodGet, "/admin/audit?category=clients", ""),
	)
	for _, want := range []string{
		"Данные клиента изменены", "ФИО: Анна Иванова → Анна Петрова", "Телефон изменён",
	} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("body does not contain %q", want)
		}
	}
	if strings.Contains(response.Body.String(), "+7999") {
		t.Error("audit page exposes client phone")
	}
}

func TestAuditPageShowsRentalCategoryAndSafeSummary(t *testing.T) {
	service := &auditServiceStub{list: func(_ context.Context, _ user.User, filter audit.Filter) (audit.Page, error) {
		if filter.Category != audit.CategoryRentals {
			t.Errorf("category = %q", filter.Category)
		}
		return audit.Page{Total: 1, Page: 1, Events: []audit.Event{{
			Action: "rental.confirmed", TargetLabel: "Аренда №24", Result: audit.ResultSuccess,
			Details: []byte(`{"client_id":18,"planned_start":"2026-08-15T07:08:00Z","planned_end":"2026-08-15T08:38:00Z","equipment_count":3}`),
		}}}, nil
	}}
	response := httptest.NewRecorder()
	newAuditTestHandler(t, service, authenticatedFixture()).ServeHTTP(
		response, authenticatedRequest(http.MethodGet, "/admin/audit?category=rentals", ""),
	)
	for _, want := range []string{
		`value="rentals" selected`, "Аренда создана и подтверждена", "Аренда №24",
		"Клиент ID: 18", "15.08.2026 10:08 — 15.08.2026 11:38", "оборудование: 3",
	} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("body does not contain %q", want)
		}
	}
}

func TestAuditPaginationPreservesFilters(t *testing.T) {
	service := &auditServiceStub{list: func(context.Context, user.User, audit.Filter) (audit.Page, error) {
		return audit.Page{Total: 51, Page: 1, Events: []audit.Event{{
			Action: "auth.login_succeeded", TargetLabel: "admin", Result: audit.ResultSuccess,
		}}}, nil
	}}
	request := authenticatedRequest(http.MethodGet, "/admin/audit?category=auth&actor=admin", "")
	response := httptest.NewRecorder()
	newAuditTestHandler(t, service, authenticatedFixture()).ServeHTTP(response, request)

	body := response.Body.String()
	for _, want := range []string{"Страница 1 из 2", "actor=admin", "category=auth", "page=2"} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q", want)
		}
	}
}

func TestOperatorCannotViewAuditPage(t *testing.T) {
	authenticated := authenticatedFixture()
	authenticated.User.Role = user.RoleOperator
	request := authenticatedRequest(http.MethodGet, "/admin/audit", "")
	response := httptest.NewRecorder()
	newAuditTestHandler(t, &auditServiceStub{}, authenticated).ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", response.Code)
	}
}

func newAuditTestHandler(t *testing.T, service auditService, authenticated session.AuthenticatedSession) http.Handler {
	t.Helper()
	resolver := &sessionResolverStub{resolve: func(context.Context, string) (session.AuthenticatedSession, error) {
		return authenticated, nil
	}}
	handler, err := NewHandler(
		slog.New(slog.NewTextHandler(io.Discard, nil)), &equipmentServiceStub{},
		&authServiceStub{}, resolver, &operatorServiceStub{}, service, &clientServiceStub{}, &rentalServiceStub{}, CookieSettings{},
	)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	return handler
}
