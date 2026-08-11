package httpserver

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Dyuzhovsergey/sup-rental/internal/equipment"
	"github.com/Dyuzhovsergey/sup-rental/internal/session"
	"github.com/Dyuzhovsergey/sup-rental/internal/user"
)

func TestPublicRoutesRemainAvailableWithoutSession(t *testing.T) {
	handler := newUnauthenticatedTestHandler(t, discardLogger())
	tests := []struct {
		path       string
		wantStatus int
	}{
		{path: "/health", wantStatus: http.StatusOK},
		{path: "/static/app.css", wantStatus: http.StatusOK},
		{path: "/login", wantStatus: http.StatusOK},
	}

	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
			if response.Code != test.wantStatus {
				t.Errorf("status = %d, want %d", response.Code, test.wantStatus)
			}
		})
	}
}

func TestProtectedRoutesRedirectWithoutSession(t *testing.T) {
	handler := newUnauthenticatedTestHandler(t, discardLogger())
	for _, path := range []string{"/", "/operator", "/equipment", "/equipment/17"} {
		t.Run(path, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
			if response.Code != http.StatusFound || response.Header().Get("Location") != "/login" {
				t.Errorf("response = %d Location %q", response.Code, response.Header().Get("Location"))
			}
		})
	}

	request := httptest.NewRequest(http.MethodPost, "/equipment", strings.NewReader("csrf_token=unused"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/login" {
		t.Errorf("POST response = %d Location %q", response.Code, response.Header().Get("Location"))
	}
}

func TestRootRedirectsAccordingToRole(t *testing.T) {
	tests := []struct {
		role     user.Role
		wantPath string
	}{
		{role: user.RoleAdmin, wantPath: "/equipment"},
		{role: user.RoleOperator, wantPath: "/operator"},
	}

	for _, test := range tests {
		t.Run(string(test.role), func(t *testing.T) {
			handler := newRoleTestHandler(t, test.role, &equipmentServiceStub{})
			request := authenticatedRequest(http.MethodGet, "/", "")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusFound || response.Header().Get("Location") != test.wantPath {
				t.Errorf("response = %d Location %q, want %q", response.Code, response.Header().Get("Location"), test.wantPath)
			}
		})
	}
}

func TestOperatorHomeAndEquipmentAreReadOnly(t *testing.T) {
	service := &equipmentServiceStub{
		list: func(context.Context) ([]equipment.Item, error) {
			return []equipment.Item{{
				ID: 17, InventoryNumber: "SUP-017", Kind: equipment.KindSUPBoard, Status: equipment.StatusAvailable,
			}}, nil
		},
		get: func(context.Context, int64) (equipment.Item, error) {
			return equipment.Item{
				ID: 17, InventoryNumber: "SUP-017", Kind: equipment.KindSUPBoard, Status: equipment.StatusAvailable,
			}, nil
		},
	}
	handler := newRoleTestHandler(t, user.RoleOperator, service)

	homeResponse := httptest.NewRecorder()
	handler.ServeHTTP(homeResponse, authenticatedRequest(http.MethodGet, "/operator", ""))
	if homeResponse.Code != http.StatusOK || !strings.Contains(homeResponse.Body.String(), "Рабочее место оператора") {
		t.Errorf("operator home = %d body %q", homeResponse.Code, homeResponse.Body.String())
	}

	for _, path := range []string{"/equipment", "/equipment/17"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, authenticatedRequest(http.MethodGet, path, ""))
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want %d", path, response.Code, http.StatusOK)
		}
		for _, forbidden := range []string{"Добавить оборудование", "Редактировать", "Списать", "Удалить"} {
			if strings.Contains(response.Body.String(), forbidden) {
				t.Errorf("GET %s contains operator-forbidden action %q", path, forbidden)
			}
		}
	}
}

func TestAdminCannotOpenOperatorHome(t *testing.T) {
	handler := newRoleTestHandler(t, user.RoleAdmin, &equipmentServiceStub{})
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, authenticatedRequest(http.MethodGet, "/operator", ""))

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
	}
	for _, want := range []string{"Недостаточно прав", `role="alert"`, `href="/equipment"`} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("body does not contain %q", want)
		}
	}
}

func TestOperatorCannotAccessAdministrativeEquipmentRoutes(t *testing.T) {
	calls := 0
	service := &equipmentServiceStub{
		create: func(context.Context, equipment.CreateInput) (equipment.Item, error) {
			calls++
			return equipment.Item{}, nil
		},
		get: func(context.Context, int64) (equipment.Item, error) {
			calls++
			return equipment.Item{}, nil
		},
		update: func(context.Context, int64, equipment.UpdateInput) (equipment.Item, error) {
			calls++
			return equipment.Item{}, nil
		},
		changeStatus: func(context.Context, int64, equipment.Status) (equipment.Item, error) {
			calls++
			return equipment.Item{}, nil
		},
		delete: func(context.Context, int64) (equipment.Item, error) {
			calls++
			return equipment.Item{}, nil
		},
	}
	handler := newRoleTestHandler(t, user.RoleOperator, service)
	tests := []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: "/equipment"},
		{method: http.MethodGet, path: "/equipment/17/edit"},
		{method: http.MethodPost, path: "/equipment/17/edit"},
		{method: http.MethodGet, path: "/equipment/17/retire"},
		{method: http.MethodPost, path: "/equipment/17/retire"},
		{method: http.MethodGet, path: "/equipment/17/delete"},
		{method: http.MethodPost, path: "/equipment/17/delete"},
	}

	for _, test := range tests {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, authenticatedRequest(test.method, test.path, "csrf_token=csrf-token"))
		if response.Code != http.StatusForbidden {
			t.Errorf("%s %s status = %d, want %d", test.method, test.path, response.Code, http.StatusForbidden)
		}
	}
	if calls != 0 {
		t.Errorf("equipment service calls = %d, want 0", calls)
	}
}

func TestEquipmentMutationsRequireSessionCSRFToken(t *testing.T) {
	calls := 0
	service := &equipmentServiceStub{
		create: func(context.Context, equipment.CreateInput) (equipment.Item, error) {
			calls++
			return equipment.Item{}, nil
		},
		update: func(context.Context, int64, equipment.UpdateInput) (equipment.Item, error) {
			calls++
			return equipment.Item{}, nil
		},
		changeStatus: func(context.Context, int64, equipment.Status) (equipment.Item, error) {
			calls++
			return equipment.Item{}, nil
		},
		delete: func(context.Context, int64) (equipment.Item, error) { calls++; return equipment.Item{}, nil },
	}
	handler := newRoleTestHandler(t, user.RoleAdmin, service)

	for _, path := range []string{"/equipment", "/equipment/17/edit", "/equipment/17/retire", "/equipment/17/delete"} {
		for _, body := range []string{"", "csrf_token=wrong-token"} {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, authenticatedRequest(http.MethodPost, path, body))
			if response.Code != http.StatusForbidden {
				t.Errorf("POST %s body %q status = %d, want %d", path, body, response.Code, http.StatusForbidden)
			}
		}
	}
	if calls != 0 {
		t.Errorf("equipment service calls = %d, want 0", calls)
	}
}

func newRoleTestHandler(t *testing.T, role user.Role, service equipmentService) http.Handler {
	t.Helper()
	authenticated := authenticatedFixture()
	authenticated.User.Role = role
	if role == user.RoleOperator {
		authenticated.User.Login = "operator"
	}
	resolver := &sessionResolverStub{
		resolve: func(context.Context, string) (session.AuthenticatedSession, error) {
			return authenticated, nil
		},
	}

	return newHandlerWithDependencies(
		t,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		service,
		resolver,
	)
}

func authenticatedRequest(method, path, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "raw-session-token"})
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	return request
}
