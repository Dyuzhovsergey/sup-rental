package httpserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Dyuzhovsergey/sup-rental/internal/client"
	"github.com/Dyuzhovsergey/sup-rental/internal/session"
	"github.com/Dyuzhovsergey/sup-rental/internal/user"
)

func TestClientsPageShowsOperatorCreateFormAndAdminReadOnlyList(t *testing.T) {
	service := &clientServiceStub{list: func(context.Context, int) (client.Page, error) {
		return client.Page{Page: 1, Total: 1, Clients: []client.Client{{ID: 3, FullName: "Анна Иванова", Phone: "+79991234567"}}}, nil
	}}
	for _, tt := range []struct {
		role     user.Role
		wantForm bool
	}{
		{role: user.RoleOperator, wantForm: true},
		{role: user.RoleAdmin, wantForm: false},
	} {
		response := httptest.NewRecorder()
		newClientTestHandler(t, service, tt.role).ServeHTTP(response, clientRequest(http.MethodGet, "/clients", ""))
		if response.Code != http.StatusOK {
			t.Fatalf("%s status = %d", tt.role, response.Code)
		}
		body := response.Body.String()
		for _, want := range []string{"Клиенты", "Анна Иванова", "&#43;79991234567", "1 клиент", `href="/clients"`, `aria-current="page"`} {
			if !strings.Contains(body, want) {
				t.Errorf("%s body does not contain %q", tt.role, want)
			}
		}
		if got := strings.Contains(body, `method="post" action="/clients"`); got != tt.wantForm {
			t.Errorf("%s create form = %t, want %t", tt.role, got, tt.wantForm)
		}
	}
}

func TestClientsPageSearchesByRawPhone(t *testing.T) {
	service := &clientServiceStub{
		list: func(context.Context, int) (client.Page, error) { return client.Page{Page: 1, Total: 4}, nil },
		find: func(_ context.Context, phone string) (client.Client, error) {
			if phone != "8 (999) 123-45-67" {
				t.Errorf("search phone = %q", phone)
			}
			return client.Client{ID: 8, FullName: "Найденный Клиент", Phone: "+79991234567"}, nil
		},
	}
	response := httptest.NewRecorder()
	newClientTestHandler(t, service, user.RoleOperator).ServeHTTP(response, clientRequest(http.MethodGet, "/clients?phone=8+%28999%29+123-45-67", ""))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Найденный Клиент") || !strings.Contains(response.Body.String(), "Сбросить") {
		t.Errorf("status = %d body = %q", response.Code, response.Body.String())
	}
}

func TestClientsPageHandlesSearchOutcomes(t *testing.T) {
	tests := []struct {
		name, phone string
		err         error
		status      int
		want        string
	}{
		{name: "not found", phone: "+79990000000", err: client.ErrClientNotFound, status: 200, want: "Клиент не найден"},
		{name: "invalid", phone: "bad", err: client.ErrInvalidPhone, status: 422, want: "Введите корректный номер телефона"},
		{name: "infrastructure", phone: "+79990000001", err: errors.New("database secret"), status: 500, want: "Internal Server Error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &clientServiceStub{
				list: func(context.Context, int) (client.Page, error) { return client.Page{Page: 1}, nil },
				find: func(context.Context, string) (client.Client, error) { return client.Client{}, tt.err },
			}
			response := httptest.NewRecorder()
			newClientTestHandler(t, service, user.RoleOperator).ServeHTTP(response, clientRequest(http.MethodGet, "/clients?phone="+url.QueryEscape(tt.phone), ""))
			if response.Code != tt.status || !strings.Contains(response.Body.String(), tt.want) {
				t.Errorf("status = %d body = %q", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), "database secret") {
				t.Error("response exposes internal error")
			}
		})
	}
}

func TestCreateClientRedirectsAndReceivesOperator(t *testing.T) {
	service := &clientServiceStub{create: func(_ context.Context, actor user.User, fullName, phone string) (client.Client, error) {
		if actor.Role != user.RoleOperator || actor.Login != "operator" || fullName != "Анна Иванова" || phone != "8 (999) 123-45-67" {
			t.Errorf("Create() actor=%+v name=%q phone=%q", actor, fullName, phone)
		}
		return client.Client{ID: 23}, nil
	}}
	response := httptest.NewRecorder()
	body := "csrf_token=csrf-token&full_name=" + url.QueryEscape("Анна Иванова") + "&phone=" + url.QueryEscape("8 (999) 123-45-67")
	newClientTestHandler(t, service, user.RoleOperator).ServeHTTP(response, clientRequest(http.MethodPost, "/clients", body))
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/clients?created=23" {
		t.Errorf("status = %d location = %q", response.Code, response.Header().Get("Location"))
	}
}

func TestClientsPageShowsCreatedClientMessage(t *testing.T) {
	service := &clientServiceStub{
		list: func(context.Context, int) (client.Page, error) { return client.Page{Page: 1}, nil },
		get: func(context.Context, int64) (client.Client, error) {
			return client.Client{ID: 23, FullName: "Анна Иванова"}, nil
		},
	}
	response := httptest.NewRecorder()
	newClientTestHandler(t, service, user.RoleOperator).ServeHTTP(response, clientRequest(http.MethodGet, "/clients?created=23", ""))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Клиент Анна Иванова создан.") {
		t.Errorf("status = %d body = %q", response.Code, response.Body.String())
	}
}

func TestCreateClientRendersValidationAndRepositoryErrors(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		want   string
	}{
		{name: "name", err: client.ErrFullNameRequired, status: 422, want: "Укажите ФИО клиента"},
		{name: "phone", err: client.ErrInvalidPhone, status: 422, want: "Введите корректный номер телефона"},
		{name: "duplicate", err: client.ErrPhoneExists, status: 409, want: "Клиент с таким номером уже существует"},
		{name: "internal", err: errors.New("postgres secret"), status: 500, want: "Internal Server Error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &clientServiceStub{
				create: func(context.Context, user.User, string, string) (client.Client, error) {
					return client.Client{}, tt.err
				},
				list: func(context.Context, int) (client.Page, error) { return client.Page{Page: 1}, nil },
			}
			response := httptest.NewRecorder()
			newClientTestHandler(t, service, user.RoleOperator).ServeHTTP(response, clientRequest(http.MethodPost, "/clients", "csrf_token=csrf-token&full_name=Анна&phone=bad"))
			if response.Code != tt.status || !strings.Contains(response.Body.String(), tt.want) {
				t.Errorf("status = %d body = %q", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), "postgres secret") {
				t.Error("response exposes internal error")
			}
		})
	}
}

func TestClientCreationRequiresOperatorAndCSRF(t *testing.T) {
	service := &clientServiceStub{create: func(context.Context, user.User, string, string) (client.Client, error) {
		t.Fatal("Create() must not be called")
		return client.Client{}, nil
	}}
	for _, tt := range []struct {
		role user.Role
		body string
	}{
		{role: user.RoleAdmin, body: "csrf_token=csrf-token"},
		{role: user.RoleOperator, body: "csrf_token=wrong"},
	} {
		response := httptest.NewRecorder()
		newClientTestHandler(t, service, tt.role).ServeHTTP(response, clientRequest(http.MethodPost, "/clients", tt.body))
		if response.Code != http.StatusForbidden {
			t.Errorf("role=%s body=%q status=%d", tt.role, tt.body, response.Code)
		}
	}
}

func TestClientCreationRejectsMalformedForm(t *testing.T) {
	service := &clientServiceStub{create: func(context.Context, user.User, string, string) (client.Client, error) {
		t.Fatal("Create() must not be called")
		return client.Client{}, nil
	}}
	response := httptest.NewRecorder()
	newClientTestHandler(t, service, user.RoleOperator).ServeHTTP(response, clientRequest(http.MethodPost, "/clients", "csrf_token=%zz"))
	if response.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", response.Code)
	}
}

func TestClientsPagePaginatesAndRejectsInvalidPage(t *testing.T) {
	service := &clientServiceStub{list: func(_ context.Context, page int) (client.Page, error) {
		return client.Page{Page: page, Total: 21, Clients: []client.Client{{ID: int64(page), FullName: "Клиент"}}}, nil
	}}
	response := httptest.NewRecorder()
	newClientTestHandler(t, service, user.RoleAdmin).ServeHTTP(response, clientRequest(http.MethodGet, "/clients?page=2", ""))
	for _, want := range []string{"Страница 2 из 3", "page=3", `href="/clients"`} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("body does not contain %q", want)
		}
	}
	bad := httptest.NewRecorder()
	newClientTestHandler(t, service, user.RoleAdmin).ServeHTTP(bad, clientRequest(http.MethodGet, "/clients?page=bad", ""))
	if bad.Code != http.StatusNotFound {
		t.Errorf("invalid page status = %d", bad.Code)
	}
}

func newClientTestHandler(t *testing.T, clients clientService, role user.Role) http.Handler {
	t.Helper()
	authenticated := authenticatedFixture()
	authenticated.User.Role = role
	authenticated.User.Login = "admin"
	if role == user.RoleOperator {
		authenticated.User.Login = "operator"
	}
	resolver := &sessionResolverStub{resolve: func(context.Context, string) (session.AuthenticatedSession, error) { return authenticated, nil }}
	handler, err := NewHandler(discardLogger(), &equipmentServiceStub{}, &authServiceStub{}, resolver, &operatorServiceStub{}, &auditServiceStub{}, clients, CookieSettings{})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	return handler
}

func clientRequest(method, target, body string) *http.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "raw-session-token"})
	return request
}
