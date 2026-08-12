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
	for _, want := range []string{"Журнал действий", "12.08.2026 10:30:00 МСК", "Оборудование изменено", "SUP-001", "Номер: SUP-001 → SUP-002", "Страница 1 из 1"} {
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
		&authServiceStub{}, resolver, &operatorServiceStub{}, service, &clientServiceStub{}, CookieSettings{},
	)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	return handler
}
