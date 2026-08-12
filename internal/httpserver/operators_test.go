package httpserver

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Dyuzhovsergey/sup-rental/internal/password"
	"github.com/Dyuzhovsergey/sup-rental/internal/session"
	"github.com/Dyuzhovsergey/sup-rental/internal/user"
)

func TestOperatorsPageShowsSafeAccountData(t *testing.T) {
	lastLogin := time.Date(2026, time.August, 11, 15, 30, 0, 0, time.UTC)
	service := &operatorServiceStub{list: func(context.Context, user.User) ([]user.User, error) {
		return []user.User{{ID: 12, Login: "rental.operator", Role: user.RoleOperator, Active: true, LastLoginAt: &lastLogin}}, nil
	}}
	request := authenticatedOperatorRequest(http.MethodGet, "/admin/operators", nil)
	response := httptest.NewRecorder()

	newOperatorTestHandler(t, service, authenticatedFixture()).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	for _, want := range []string{"rental.operator", "Оператор проката", "Активен", "11.08.2026 18:30 МСК", "Сменить пароль", "Отключить"} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("body does not contain %q", want)
		}
	}
}

func TestCreateOperatorRedirectsAndDoesNotEchoPassword(t *testing.T) {
	var gotLogin, gotPassword string
	service := &operatorServiceStub{
		create: func(_ context.Context, _ user.User, login, plainPassword string) (user.User, error) {
			gotLogin, gotPassword = login, plainPassword
			return user.User{ID: 12, Login: login, Role: user.RoleOperator, Active: true}, nil
		},
	}
	values := url.Values{
		"csrf_token": {"csrf-token"}, "login": {"new.operator"},
		"password": {"secret1"}, "password_confirmation": {"secret1"},
	}
	request := authenticatedOperatorRequest(http.MethodPost, "/admin/operators", values)
	response := httptest.NewRecorder()

	newOperatorTestHandler(t, service, authenticatedFixture()).ServeHTTP(response, request)

	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/admin/operators?notice=created" {
		t.Fatalf("response = %d, Location = %q", response.Code, response.Header().Get("Location"))
	}
	if gotLogin != "new.operator" || gotPassword != "secret1" {
		t.Errorf("service input = %q, %q", gotLogin, gotPassword)
	}
	if strings.Contains(response.Body.String(), "secret1") {
		t.Error("response echoed plaintext password")
	}
}

func TestCreateOperatorValidationDoesNotEchoPassword(t *testing.T) {
	service := &operatorServiceStub{
		create: func(context.Context, user.User, string, string) (user.User, error) {
			return user.User{}, password.ErrTooShort
		},
	}
	values := url.Values{
		"csrf_token": {"csrf-token"}, "login": {"new.operator"},
		"password": {"short"}, "password_confirmation": {"short"},
	}
	request := authenticatedOperatorRequest(http.MethodPost, "/admin/operators", values)
	response := httptest.NewRecorder()

	newOperatorTestHandler(t, service, authenticatedFixture()).ServeHTTP(response, request)

	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", response.Code)
	}
	if !strings.Contains(response.Body.String(), "не менее 6 символов") || strings.Contains(response.Body.String(), `value="short"`) {
		t.Errorf("validation response contains unexpected body: %s", response.Body.String())
	}
}

func TestOperatorCannotAccessOperatorManagement(t *testing.T) {
	authenticated := authenticatedFixture()
	authenticated.User.Role = user.RoleOperator
	request := authenticatedOperatorRequest(http.MethodGet, "/admin/operators", nil)
	response := httptest.NewRecorder()

	newOperatorTestHandler(t, &operatorServiceStub{}, authenticated).ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", response.Code)
	}
}

func TestDisableOperatorRedirects(t *testing.T) {
	calls := 0
	service := &operatorServiceStub{disable: func(_ context.Context, _ user.User, id int64) (user.User, error) {
		calls++
		return user.User{ID: id}, nil
	}}
	values := url.Values{"csrf_token": {"csrf-token"}}
	request := authenticatedOperatorRequest(http.MethodPost, "/admin/operators/12/disable", values)
	response := httptest.NewRecorder()

	newOperatorTestHandler(t, service, authenticatedFixture()).ServeHTTP(response, request)

	if response.Code != http.StatusSeeOther || calls != 1 {
		t.Fatalf("response = %d, calls = %d", response.Code, calls)
	}
}

func TestOperatorMutationsRequireCSRF(t *testing.T) {
	calls := 0
	service := &operatorServiceStub{activate: func(context.Context, user.User, int64) (user.User, error) {
		calls++
		return user.User{}, nil
	}}
	request := authenticatedOperatorRequest(http.MethodPost, "/admin/operators/12/activate", url.Values{})
	response := httptest.NewRecorder()

	newOperatorTestHandler(t, service, authenticatedFixture()).ServeHTTP(response, request)

	if response.Code != http.StatusForbidden || calls != 0 {
		t.Fatalf("response = %d, calls = %d", response.Code, calls)
	}
}

func TestOperatorMutationMapsNotFoundAndConflict(t *testing.T) {
	tests := []struct {
		name       string
		target     string
		service    *operatorServiceStub
		wantStatus int
	}{
		{
			name: "invalid ID", target: "/admin/operators/not-an-id/activate",
			service: &operatorServiceStub{}, wantStatus: http.StatusNotFound,
		},
		{
			name: "missing operator", target: "/admin/operators/99/activate",
			service: &operatorServiceStub{activate: func(context.Context, user.User, int64) (user.User, error) {
				return user.User{}, user.ErrOperatorNotFound
			}}, wantStatus: http.StatusNotFound,
		},
		{
			name: "already active", target: "/admin/operators/12/activate",
			service: &operatorServiceStub{activate: func(context.Context, user.User, int64) (user.User, error) {
				return user.User{}, user.ErrOperatorAlreadyActive
			}}, wantStatus: http.StatusConflict,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := authenticatedOperatorRequest(http.MethodPost, tt.target, url.Values{"csrf_token": {"csrf-token"}})
			response := httptest.NewRecorder()
			newOperatorTestHandler(t, tt.service, authenticatedFixture()).ServeHTTP(response, request)
			if response.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", response.Code, tt.wantStatus)
			}
		})
	}
}

func TestOperatorInfrastructureErrorIsHidden(t *testing.T) {
	service := &operatorServiceStub{list: func(context.Context, user.User) ([]user.User, error) {
		return nil, errors.New("database secret detail")
	}}
	request := authenticatedOperatorRequest(http.MethodGet, "/admin/operators", nil)
	response := httptest.NewRecorder()

	newOperatorTestHandler(t, service, authenticatedFixture()).ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "database secret detail") {
		t.Fatalf("response = %d, body = %q", response.Code, response.Body.String())
	}
}

func newOperatorTestHandler(t *testing.T, operators operatorService, authenticated session.AuthenticatedSession) http.Handler {
	t.Helper()
	resolver := &sessionResolverStub{resolve: func(context.Context, string) (session.AuthenticatedSession, error) {
		return authenticated, nil
	}}
	handler, err := NewHandler(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		&equipmentServiceStub{}, &authServiceStub{}, resolver, operators,
		&auditServiceStub{}, CookieSettings{},
	)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	return handler
}

func authenticatedOperatorRequest(method, target string, values url.Values) *http.Request {
	var body io.Reader
	if values != nil {
		body = strings.NewReader(values.Encode())
	}
	request := httptest.NewRequest(method, target, body)
	if values != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "raw-session-token"})
	return request
}
