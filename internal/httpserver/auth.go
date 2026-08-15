package httpserver

import (
	"bytes"
	"context"
	"crypto/subtle"
	"errors"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	appauth "github.com/Dyuzhovsergey/sup-rental/internal/auth"
	"github.com/Dyuzhovsergey/sup-rental/internal/session"
	"github.com/Dyuzhovsergey/sup-rental/internal/user"
)

const (
	sessionCookieName             = "sup_rental_session"
	maxLoginBodyBytes             = 16 * 1024
	maxAuthenticatedFormBodyBytes = 64 * 1024
)

type authService interface {
	Login(ctx context.Context, input appauth.LoginInput) (appauth.LoginResult, error)
	Logout(ctx context.Context, authenticated session.AuthenticatedSession) error
}

type sessionResolver interface {
	Resolve(ctx context.Context, token string) (session.AuthenticatedSession, error)
}

// CookieSettings задаёт безопасные параметры session cookie HTTP-слоя.
type CookieSettings struct {
	// Secure требует HTTPS для передачи cookie.
	Secure bool
}

type authenticationContextKey struct{}

type authenticationView struct {
	Login              string
	Role               string
	CSRFToken          string
	HomePath           string
	IsAdmin            bool
	IsOperator         bool
	HomeActive         bool
	EquipmentActive    bool
	ClientsActive      bool
	RentalsActive      bool
	OperatorsActive    bool
	AuditActive        bool
	CanManageEquipment bool
}

type loginPageData struct {
	Title     string
	Login     string
	Error     string
	LoggedOut bool
}

func optionalSession(
	logger *slog.Logger,
	resolver sessionResolver,
	cookieSettings CookieSettings,
	next http.Handler,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" || strings.HasPrefix(r.URL.Path, "/static/") {
			next.ServeHTTP(w, r)
			return
		}

		cookie, err := r.Cookie(sessionCookieName)
		if errors.Is(err, http.ErrNoCookie) {
			next.ServeHTTP(w, r)
			return
		}
		if err != nil {
			clearSessionCookie(w, cookieSettings)
			next.ServeHTTP(w, r)
			return
		}

		authenticated, err := resolver.Resolve(r.Context(), cookie.Value)
		if errors.Is(err, session.ErrInvalidToken) || errors.Is(err, session.ErrSessionNotFound) {
			clearSessionCookie(w, cookieSettings)
			next.ServeHTTP(w, r)
			return
		}
		if err != nil {
			logger.Error("resolve HTTP session", slog.Any("error", err))
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Cache-Control", "no-store")
		ctx := context.WithValue(r.Context(), authenticationContextKey{}, authenticated)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func showLoginPage(
	logger *slog.Logger,
	pageTemplates *template.Template,
	w http.ResponseWriter,
	r *http.Request,
) {
	if authenticated, ok := authenticatedSession(r); ok {
		http.Redirect(w, r, homePathForRole(authenticated.User.Role), http.StatusFound)
		return
	}

	renderLoginPage(
		logger,
		pageTemplates,
		w,
		http.StatusOK,
		loginPageData{
			Title:     "Вход — SUP Rental",
			LoggedOut: r.URL.Query().Get("logged_out") == "1",
		},
	)
}

func login(
	logger *slog.Logger,
	service authService,
	pageTemplates *template.Template,
	cookieSettings CookieSettings,
	w http.ResponseWriter,
	r *http.Request,
) {
	w.Header().Set("Cache-Control", "no-store")
	if authenticated, ok := authenticatedSession(r); ok {
		http.Redirect(w, r, homePathForRole(authenticated.User.Role), http.StatusSeeOther)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxLoginBodyBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	loginValue := r.PostForm.Get("login")
	remoteIP, err := remoteIPFromRequest(r)
	if err != nil {
		logger.Error("read login remote IP", slog.Any("error", err))
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	result, err := service.Login(r.Context(), appauth.LoginInput{
		Login:    loginValue,
		Password: r.PostForm.Get("password"),
		RemoteIP: remoteIP,
	})
	if err == nil {
		setSessionCookie(w, result.Token, cookieSettings)
		http.Redirect(w, r, homePathForRole(result.User.Role), http.StatusSeeOther)
		return
	}

	data := loginPageData{
		Title: "Вход — SUP Rental",
		Login: strings.TrimSpace(loginValue),
	}
	switch {
	case errors.Is(err, appauth.ErrInvalidCredentials):
		data.Error = "Неверный логин или пароль."
		renderLoginPage(logger, pageTemplates, w, http.StatusUnauthorized, data)
	case errors.Is(err, appauth.ErrLoginThrottled):
		data.Error = "Слишком много неудачных попыток. Повторите вход позднее."
		var throttled *appauth.ThrottledError
		if errors.As(err, &throttled) {
			seconds := int(time.Until(throttled.Until).Seconds())
			if seconds < 1 {
				seconds = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
		}
		renderLoginPage(logger, pageTemplates, w, http.StatusTooManyRequests, data)
	default:
		logger.Error("authenticate user", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

func logout(
	logger *slog.Logger,
	service authService,
	cookieSettings CookieSettings,
	w http.ResponseWriter,
	r *http.Request,
) {
	w.Header().Set("Cache-Control", "no-store")
	authenticated, ok := authenticatedSession(r)
	if !ok {
		clearSessionCookie(w, cookieSettings)
		http.Redirect(w, r, "/login?logged_out=1", http.StatusSeeOther)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxLoginBodyBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	if !equalSecret(r.PostForm.Get("csrf_token"), authenticated.Session.CSRFToken) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	if err := service.Logout(r.Context(), authenticated); err != nil {
		logger.Error("logout user", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	clearSessionCookie(w, cookieSettings)
	http.Redirect(w, r, "/login?logged_out=1", http.StatusSeeOther)
}

func renderLoginPage(
	logger *slog.Logger,
	pageTemplates *template.Template,
	w http.ResponseWriter,
	statusCode int,
	data loginPageData,
) {
	var body bytes.Buffer
	if err := pageTemplates.ExecuteTemplate(&body, "login.html", data); err != nil {
		logger.Error("render login page", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(statusCode)
	if _, err := w.Write(body.Bytes()); err != nil {
		logger.Error("write login response", slog.Any("error", err))
	}
}

func authenticatedSession(r *http.Request) (session.AuthenticatedSession, bool) {
	authenticated, ok := r.Context().Value(authenticationContextKey{}).(session.AuthenticatedSession)
	return authenticated, ok
}

func authenticationForPage(r *http.Request) *authenticationView {
	authenticated, ok := authenticatedSession(r)
	if !ok {
		return nil
	}

	isAdmin := authenticated.User.Role == user.RoleAdmin
	isOperator := authenticated.User.Role == user.RoleOperator

	return &authenticationView{
		Login:              authenticated.User.Login,
		Role:               roleLabel(authenticated.User.Role),
		CSRFToken:          authenticated.Session.CSRFToken,
		HomePath:           homePathForRole(authenticated.User.Role),
		IsAdmin:            isAdmin,
		IsOperator:         isOperator,
		HomeActive:         r.URL.Path == "/operator",
		EquipmentActive:    strings.HasPrefix(r.URL.Path, "/equipment"),
		ClientsActive:      strings.HasPrefix(r.URL.Path, "/clients"),
		RentalsActive:      strings.HasPrefix(r.URL.Path, "/rentals"),
		OperatorsActive:    strings.HasPrefix(r.URL.Path, "/admin/operators"),
		AuditActive:        strings.HasPrefix(r.URL.Path, "/admin/audit"),
		CanManageEquipment: isAdmin,
	}
}

func requireAuthentication(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := authenticatedSession(r); ok {
			next.ServeHTTP(w, r)
			return
		}

		statusCode := http.StatusFound
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			statusCode = http.StatusSeeOther
		}
		http.Redirect(w, r, "/login", statusCode)
	})
}

func requireRole(
	logger *slog.Logger,
	pageTemplates *template.Template,
	role user.Role,
	next http.Handler,
) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authenticated, ok := authenticatedSession(r)
		if ok && authenticated.User.Role == role {
			next.ServeHTTP(w, r)
			return
		}

		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			renderForbiddenPage(logger, pageTemplates, w, r)
			return
		}
		http.Error(w, "Forbidden", http.StatusForbidden)
	})
}

func requireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authenticated, ok := authenticatedSession(r)
		if !ok {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxAuthenticatedFormBodyBytes)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		}
		if !equalSecret(r.PostForm.Get("csrf_token"), authenticated.Session.CSRFToken) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func homePathForRole(role user.Role) string {
	if role == user.RoleOperator {
		return "/operator"
	}
	return "/equipment"
}

func roleLabel(role user.Role) string {
	if role == user.RoleAdmin {
		return "Администратор"
	}
	return "Оператор проката"
}

func setSessionCookie(w http.ResponseWriter, token string, settings CookieSettings) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   settings.Secure,
		SameSite: http.SameSiteStrictMode,
	})
}

func clearSessionCookie(w http.ResponseWriter, settings CookieSettings) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   settings.Secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

func equalSecret(actual, expected string) bool {
	if actual == "" || len(actual) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) == 1
}

func remoteIPFromRequest(r *http.Request) (string, error) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return "", err
	}
	parsed := net.ParseIP(host)
	if parsed == nil {
		return "", errors.New("RemoteAddr host is not an IP address")
	}

	return parsed.String(), nil
}
