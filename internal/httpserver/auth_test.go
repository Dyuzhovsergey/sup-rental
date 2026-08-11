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

	appauth "github.com/Dyuzhovsergey/sup-rental/internal/auth"
	"github.com/Dyuzhovsergey/sup-rental/internal/session"
	"github.com/Dyuzhovsergey/sup-rental/internal/user"
)

func TestLoginPage(t *testing.T) {
	handler := newAuthenticationTestHandler(t, &authServiceStub{}, &sessionResolverStub{}, CookieSettings{})
	request := httptest.NewRequest(http.MethodGet, "/login", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	for _, want := range []string{
		`<h1 id="login-heading">Вход в систему</h1>`,
		`name="login"`,
		`name="password"`,
		`autocomplete="username"`,
		`autocomplete="current-password"`,
	} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("body does not contain %q", want)
		}
	}
	if strings.Contains(response.Body.String(), `class="sidebar"`) {
		t.Error("login page contains application sidebar")
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

func TestLoginSuccessSetsSessionCookie(t *testing.T) {
	service := &authServiceStub{
		login: func(_ context.Context, input appauth.LoginInput) (appauth.LoginResult, error) {
			if input.Login != "admin" || input.Password != "secret1" || input.RemoteIP != "192.0.2.1" {
				t.Errorf("Login() input = %+v", input)
			}
			return appauth.LoginResult{Token: "raw-session-token"}, nil
		},
	}
	handler := newAuthenticationTestHandler(
		t,
		service,
		&sessionResolverStub{},
		CookieSettings{Secure: true},
	)
	request := loginRequest(url.Values{"login": {"admin"}, "password": {"secret1"}})
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/equipment" {
		t.Fatalf("response = %d Location %q", response.Code, response.Header().Get("Location"))
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %d, want 1", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != sessionCookieName || cookie.Value != "raw-session-token" ||
		cookie.Path != "/" || !cookie.HttpOnly || !cookie.Secure ||
		cookie.SameSite != http.SameSiteStrictMode || cookie.Domain != "" ||
		cookie.MaxAge != 0 || !cookie.Expires.IsZero() {
		t.Errorf("session cookie = %+v", cookie)
	}
}

func TestLoginHidesInternalError(t *testing.T) {
	service := &authServiceStub{
		login: func(context.Context, appauth.LoginInput) (appauth.LoginResult, error) {
			return appauth.LoginResult{}, errors.New("database contains a secret")
		},
	}
	handler := newAuthenticationTestHandler(t, service, &sessionResolverStub{}, CookieSettings{})
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, loginRequest(url.Values{"login": {"admin"}, "password": {"secret1"}}))

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
	if strings.Contains(response.Body.String(), "database contains a secret") {
		t.Error("response contains internal error")
	}
}

func TestInvalidSessionCookieIsCleared(t *testing.T) {
	resolver := &sessionResolverStub{
		resolve: func(context.Context, string) (session.AuthenticatedSession, error) {
			return session.AuthenticatedSession{}, session.ErrSessionNotFound
		},
	}
	handler := newAuthenticationTestHandler(t, &authServiceStub{}, resolver, CookieSettings{Secure: true})
	request := httptest.NewRequest(http.MethodGet, "/login", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "expired-token"})
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != sessionCookieName ||
		cookies[0].MaxAge >= 0 || !cookies[0].Secure {
		t.Errorf("cleared cookies = %+v", cookies)
	}
}

func TestLoginFailureIsGenericAndDoesNotEchoPassword(t *testing.T) {
	service := &authServiceStub{
		login: func(context.Context, appauth.LoginInput) (appauth.LoginResult, error) {
			return appauth.LoginResult{}, appauth.ErrInvalidCredentials
		},
	}
	handler := newAuthenticationTestHandler(t, service, &sessionResolverStub{}, CookieSettings{})
	request := loginRequest(url.Values{"login": {" unknown "}, "password": {"visible-secret"}})
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	if !strings.Contains(response.Body.String(), "Неверный логин или пароль.") ||
		!strings.Contains(response.Body.String(), `value="unknown"`) {
		t.Errorf("body = %q", response.Body.String())
	}
	if strings.Contains(response.Body.String(), "visible-secret") {
		t.Error("response contains submitted password")
	}
}

func TestLoginThrottled(t *testing.T) {
	service := &authServiceStub{
		login: func(context.Context, appauth.LoginInput) (appauth.LoginResult, error) {
			return appauth.LoginResult{}, &appauth.ThrottledError{Until: time.Now().Add(10 * time.Minute)}
		},
	}
	handler := newAuthenticationTestHandler(t, service, &sessionResolverStub{}, CookieSettings{})
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, loginRequest(url.Values{"login": {"admin"}, "password": {"wrong1"}}))

	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusTooManyRequests)
	}
	if response.Header().Get("Retry-After") == "" {
		t.Error("Retry-After is empty")
	}
	if !strings.Contains(response.Body.String(), "Слишком много неудачных попыток") {
		t.Errorf("body = %q", response.Body.String())
	}
}

func TestLogoutRequiresCSRFAndClearsCookie(t *testing.T) {
	authenticated := authenticatedFixture()
	resolver := &sessionResolverStub{
		resolve: func(_ context.Context, token string) (session.AuthenticatedSession, error) {
			if token != "raw-session-token" {
				t.Errorf("Resolve() token = %q", token)
			}
			return authenticated, nil
		},
	}
	logoutCalls := 0
	service := &authServiceStub{
		logout: func(_ context.Context, got session.AuthenticatedSession) error {
			logoutCalls++
			if got.Session.ID != authenticated.Session.ID {
				t.Errorf("Logout() = %+v", got)
			}
			return nil
		},
	}
	handler := newAuthenticationTestHandler(t, service, resolver, CookieSettings{})

	wrongRequest := logoutRequest("wrong-token")
	wrongResponse := httptest.NewRecorder()
	handler.ServeHTTP(wrongResponse, wrongRequest)
	if wrongResponse.Code != http.StatusForbidden || logoutCalls != 0 {
		t.Errorf("wrong CSRF response = %d, logout calls = %d", wrongResponse.Code, logoutCalls)
	}

	request := logoutRequest(authenticated.Session.CSRFToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/login?logged_out=1" {
		t.Fatalf("logout response = %d Location %q", response.Code, response.Header().Get("Location"))
	}
	if logoutCalls != 1 {
		t.Errorf("logout calls = %d, want 1", logoutCalls)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != sessionCookieName || cookies[0].MaxAge >= 0 {
		t.Errorf("cleared cookies = %+v", cookies)
	}
}

func TestAuthenticatedSidebarShowsUserAndLogout(t *testing.T) {
	resolver := &sessionResolverStub{
		resolve: func(context.Context, string) (session.AuthenticatedSession, error) {
			return authenticatedFixture(), nil
		},
	}
	handler := newAuthenticationTestHandler(t, &authServiceStub{}, resolver, CookieSettings{})
	request := httptest.NewRequest(http.MethodGet, "/equipment", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "raw-session-token"})
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	for _, want := range []string{"admin", "Администратор", `action="/logout"`, `value="csrf-token"`} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("body does not contain %q", want)
		}
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

func TestCrossOriginProtectionRejectsLogin(t *testing.T) {
	calls := 0
	service := &authServiceStub{
		login: func(context.Context, appauth.LoginInput) (appauth.LoginResult, error) {
			calls++
			return appauth.LoginResult{}, nil
		},
	}
	handler := newAuthenticationTestHandler(t, service, &sessionResolverStub{}, CookieSettings{})
	request := loginRequest(url.Values{"login": {"admin"}, "password": {"secret1"}})
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden || calls != 0 {
		t.Errorf("response = %d, login calls = %d", response.Code, calls)
	}
}

func newAuthenticationTestHandler(
	t *testing.T,
	authenticationService authService,
	resolver sessionResolver,
	settings CookieSettings,
) http.Handler {
	t.Helper()

	handler, err := NewHandler(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		&equipmentServiceStub{},
		authenticationService,
		resolver,
		settings,
	)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	return handler
}

func loginRequest(values url.Values) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(values.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return request
}

func logoutRequest(csrfToken string) *http.Request {
	values := url.Values{"csrf_token": {csrfToken}}
	request := httptest.NewRequest(http.MethodPost, "/logout", strings.NewReader(values.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "raw-session-token"})
	return request
}

func authenticatedFixture() session.AuthenticatedSession {
	return session.AuthenticatedSession{
		Session: session.Session{ID: 11, UserID: 7, CSRFToken: "csrf-token"},
		User: user.User{
			ID: 7, Login: "admin", Role: user.RoleAdmin, Active: true,
		},
	}
}
