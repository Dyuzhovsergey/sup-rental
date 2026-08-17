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

func TestClientPhoneLabelFormatsRussianAndKeepsInternationalNumbers(t *testing.T) {
	tests := []struct {
		name  string
		phone client.Phone
		want  string
	}{
		{name: "russian", phone: "+79991234567", want: "+7 (999) 123-45-67"},
		{name: "international", phone: "+4915123456789", want: "+4915123456789"},
		{name: "empty", phone: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clientPhoneLabel(tt.phone); got != tt.want {
				t.Errorf("clientPhoneLabel(%q) = %q, want %q", tt.phone, got, tt.want)
			}
		})
	}
}

func TestClientsPageShowsOperatorCreateFormAndAdminReadOnlyList(t *testing.T) {
	service := &clientServiceStub{list: func(context.Context, int, int) (client.Page, error) {
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
		for _, want := range []string{
			"Клиенты", "Анна Иванова", `href="tel:&#43;79991234567">&#43;7 (999) 123-45-67</a>`, "1 клиент",
			`href="/clients"`, `class="client-name-link" href="/clients/3"`,
			`aria-current="page"`, "Строк на странице", `value="5" selected`,
			`value="10"`, `value="15"`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s body does not contain %q", tt.role, want)
			}
		}
		if got := strings.Contains(body, `method="post" action="/clients"`); got != tt.wantForm {
			t.Errorf("%s create form = %t, want %t", tt.role, got, tt.wantForm)
		}
	}
}

func TestClientDetailIsAvailableToBothRoles(t *testing.T) {
	service := &clientServiceStub{get: func(_ context.Context, id int64) (client.Client, error) {
		if id != 23 {
			t.Errorf("Get() id = %d", id)
		}
		return client.Client{ID: id, FullName: "Анна Иванова", Phone: "+79991234567"}, nil
	}}
	for _, tt := range []struct {
		role     user.Role
		wantEdit bool
	}{
		{role: user.RoleOperator, wantEdit: true},
		{role: user.RoleAdmin, wantEdit: false},
	} {
		t.Run(string(tt.role), func(t *testing.T) {
			response := httptest.NewRecorder()
			newClientTestHandler(t, service, tt.role).ServeHTTP(response, clientRequest(http.MethodGet, "/clients/23", ""))
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d", response.Code)
			}
			body := response.Body.String()
			for _, want := range []string{"Карточка клиента", "Анна Иванова", `href="tel:&#43;79991234567">&#43;7 (999) 123-45-67</a>`, "Внутренний ID", "23", "Назад к списку"} {
				if !strings.Contains(body, want) {
					t.Errorf("body does not contain %q", want)
				}
			}
			if got := strings.Contains(body, `href="/clients/23/edit"`); got != tt.wantEdit {
				t.Errorf("edit link = %t, want %t", got, tt.wantEdit)
			}
		})
	}
}

func TestClientsPageShowsUpdatedClientMessage(t *testing.T) {
	service := &clientServiceStub{get: func(context.Context, int64) (client.Client, error) {
		return client.Client{ID: 23, FullName: "Анна Петрова", Phone: "+79991234567"}, nil
	}, list: func(context.Context, int, int) (client.Page, error) { return client.Page{Page: 1}, nil }}
	response := httptest.NewRecorder()
	newClientTestHandler(t, service, user.RoleOperator).ServeHTTP(response, clientRequest(http.MethodGet, "/clients?updated=23", ""))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Данные клиента Анна Петрова обновлены.") {
		t.Errorf("status = %d body = %q", response.Code, response.Body.String())
	}
}

func TestClientDetailHandlesInvalidMissingAndInfrastructureErrors(t *testing.T) {
	for _, tt := range []struct {
		name   string
		target string
		err    error
		status int
	}{
		{name: "invalid", target: "/clients/no", status: 404},
		{name: "non-positive", target: "/clients/0", status: 404},
		{name: "missing", target: "/clients/23", err: client.ErrClientNotFound, status: 404},
		{name: "infrastructure", target: "/clients/23", err: errors.New("postgres secret"), status: 500},
	} {
		t.Run(tt.name, func(t *testing.T) {
			service := &clientServiceStub{get: func(context.Context, int64) (client.Client, error) { return client.Client{}, tt.err }}
			response := httptest.NewRecorder()
			newClientTestHandler(t, service, user.RoleAdmin).ServeHTTP(response, clientRequest(http.MethodGet, tt.target, ""))
			if response.Code != tt.status {
				t.Errorf("status = %d, want %d", response.Code, tt.status)
			}
			if strings.Contains(response.Body.String(), "postgres secret") {
				t.Error("response exposes internal error")
			}
		})
	}
}

func TestClientEditShowsPrefilledAccessibleFormForOperator(t *testing.T) {
	service := &clientServiceStub{get: func(context.Context, int64) (client.Client, error) {
		return client.Client{ID: 23, FullName: "Анна Иванова", Phone: "+79991234567"}, nil
	}}
	response := httptest.NewRecorder()
	newClientTestHandler(t, service, user.RoleOperator).ServeHTTP(response, clientRequest(http.MethodGet, "/clients/23/edit", ""))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	for _, want := range []string{
		"Редактировать клиента", `action="/clients/23/edit"`, `name="csrf_token" value="csrf-token"`,
		`value="Анна Иванова"`, `value="&#43;7 (999) 123-45-67"`, `for="edit-client-full-name"`,
		`for="edit-client-phone"`, `href="/clients/23">Отмена`,
	} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("body does not contain %q", want)
		}
	}
}

func TestUpdateClientRedirectsAndReceivesOperator(t *testing.T) {
	service := &clientServiceStub{update: func(_ context.Context, actor user.User, id int64, fullName, phone string) (client.Client, error) {
		if actor.Role != user.RoleOperator || actor.Login != "operator" || id != 23 || fullName != "Анна Петрова" || phone != "8 (999) 765-43-21" {
			t.Errorf("Update() actor=%+v id=%d name=%q phone=%q", actor, id, fullName, phone)
		}
		return client.Client{ID: id, FullName: fullName, Phone: "+79997654321"}, nil
	}}
	form := url.Values{"csrf_token": {"csrf-token"}, "full_name": {"Анна Петрова"}, "phone": {"8 (999) 765-43-21"}}
	response := httptest.NewRecorder()
	newClientTestHandler(t, service, user.RoleOperator).ServeHTTP(response, clientRequest(http.MethodPost, "/clients/23/edit", form.Encode()))
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/clients?updated=23" {
		t.Errorf("status = %d location = %q", response.Code, response.Header().Get("Location"))
	}
}

func TestUpdateClientRendersValidationAndRepositoryErrors(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		want   string
		aria   string
	}{
		{name: "name", err: client.ErrFullNameRequired, status: 422, want: "Укажите ФИО клиента", aria: `aria-describedby="edit-client-full-name-error"`},
		{name: "phone", err: client.ErrInvalidPhone, status: 422, want: "Введите корректный номер телефона", aria: `aria-describedby="edit-client-phone-error"`},
		{name: "duplicate", err: client.ErrPhoneExists, status: 409, want: "Клиент с таким номером уже существует", aria: `aria-describedby="edit-client-phone-error"`},
		{name: "missing", err: client.ErrClientNotFound, status: 404, want: "404 page not found"},
		{name: "internal", err: errors.New("postgres secret"), status: 500, want: "Internal Server Error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &clientServiceStub{update: func(context.Context, user.User, int64, string, string) (client.Client, error) {
				return client.Client{}, tt.err
			}}
			form := url.Values{"csrf_token": {"csrf-token"}, "full_name": {"Анна"}, "phone": {"bad"}}
			response := httptest.NewRecorder()
			newClientTestHandler(t, service, user.RoleOperator).ServeHTTP(response, clientRequest(http.MethodPost, "/clients/23/edit", form.Encode()))
			if response.Code != tt.status || !strings.Contains(response.Body.String(), tt.want) {
				t.Errorf("status = %d body = %q", response.Code, response.Body.String())
			}
			if tt.aria != "" && !strings.Contains(response.Body.String(), tt.aria) {
				t.Errorf("body does not contain %q", tt.aria)
			}
			if strings.Contains(response.Body.String(), "postgres secret") {
				t.Error("response exposes internal error")
			}
		})
	}
}

func TestClientEditRequiresOperatorAndCSRF(t *testing.T) {
	service := &clientServiceStub{
		get: func(context.Context, int64) (client.Client, error) {
			return client.Client{ID: 23, FullName: "Анна", Phone: "+79991234567"}, nil
		},
		update: func(context.Context, user.User, int64, string, string) (client.Client, error) {
			t.Fatal("Update() must not be called")
			return client.Client{}, nil
		},
	}
	adminGet := httptest.NewRecorder()
	newClientTestHandler(t, service, user.RoleAdmin).ServeHTTP(adminGet, clientRequest(http.MethodGet, "/clients/23/edit", ""))
	if adminGet.Code != http.StatusForbidden {
		t.Errorf("admin GET status = %d", adminGet.Code)
	}
	for _, tt := range []struct {
		name string
		role user.Role
		body string
		want int
	}{
		{name: "admin", role: user.RoleAdmin, body: "csrf_token=csrf-token", want: 403},
		{name: "CSRF", role: user.RoleOperator, body: "csrf_token=wrong", want: 403},
		{name: "malformed", role: user.RoleOperator, body: "csrf_token=%zz", want: 400},
	} {
		t.Run(tt.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			newClientTestHandler(t, service, tt.role).ServeHTTP(response, clientRequest(http.MethodPost, "/clients/23/edit", tt.body))
			if response.Code != tt.want {
				t.Errorf("status = %d, want %d", response.Code, tt.want)
			}
		})
	}
}

func TestClientsPageSearchesByRawPhone(t *testing.T) {
	service := &clientServiceStub{
		list: func(context.Context, int, int) (client.Page, error) { return client.Page{Page: 1, Total: 4}, nil },
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
	if !strings.Contains(response.Body.String(), `value="&#43;7 (999) 123-45-67"`) {
		t.Errorf("search field does not show normalized phone: %q", response.Body.String())
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
				list: func(context.Context, int, int) (client.Page, error) { return client.Page{Page: 1}, nil },
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
		list: func(context.Context, int, int) (client.Page, error) { return client.Page{Page: 1}, nil },
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
				list: func(context.Context, int, int) (client.Page, error) { return client.Page{Page: 1}, nil },
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
	service := &clientServiceStub{list: func(_ context.Context, page, pageSize int) (client.Page, error) {
		if pageSize != 10 {
			t.Errorf("page size = %d, want 10", pageSize)
		}
		return client.Page{Page: page, Total: 21, Clients: []client.Client{{ID: int64(page), FullName: "Клиент"}}}, nil
	}}
	response := httptest.NewRecorder()
	newClientTestHandler(t, service, user.RoleAdmin).ServeHTTP(response, clientRequest(http.MethodGet, "/clients?page=2&page_size=10", ""))
	for _, want := range []string{"Страница 2 из 3", "page=3", "page_size=10", `value="10" selected`, "Строк на странице"} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("body does not contain %q", want)
		}
	}
	bad := httptest.NewRecorder()
	newClientTestHandler(t, service, user.RoleAdmin).ServeHTTP(bad, clientRequest(http.MethodGet, "/clients?page=bad", ""))
	if bad.Code != http.StatusNotFound {
		t.Errorf("invalid page status = %d", bad.Code)
	}
	badSize := httptest.NewRecorder()
	newClientTestHandler(t, service, user.RoleAdmin).ServeHTTP(badSize, clientRequest(http.MethodGet, "/clients?page_size=7", ""))
	if badSize.Code != http.StatusNotFound {
		t.Errorf("invalid page size status = %d", badSize.Code)
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
	handler, err := NewHandler(discardLogger(), &equipmentServiceStub{}, &authServiceStub{}, resolver, &operatorServiceStub{}, &auditServiceStub{}, clients, &rentalServiceStub{}, CookieSettings{})
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
