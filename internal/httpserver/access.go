package httpserver

import (
	"bytes"
	"html/template"
	"log/slog"
	"net/http"
)

type operatorPageData struct {
	Authentication *authenticationView
	Title          string
}

type forbiddenPageData struct {
	Authentication *authenticationView
	Title          string
	HomePath       string
}

func redirectToRoleHome(w http.ResponseWriter, r *http.Request) {
	authenticated, ok := authenticatedSession(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	http.Redirect(w, r, homePathForRole(authenticated.User.Role), http.StatusFound)
}

func showOperatorPage(
	logger *slog.Logger,
	pageTemplates *template.Template,
	w http.ResponseWriter,
	r *http.Request,
) {
	renderPage(
		logger,
		pageTemplates,
		w,
		http.StatusOK,
		"operator.html",
		operatorPageData{
			Authentication: authenticationForPage(r),
			Title:          "Рабочее место оператора — SUP Rental",
		},
		"render operator page",
		"write operator response",
	)
}

func renderForbiddenPage(
	logger *slog.Logger,
	pageTemplates *template.Template,
	w http.ResponseWriter,
	r *http.Request,
) {
	authentication := authenticationForPage(r)
	homePath := "/login"
	if authentication != nil {
		homePath = authentication.HomePath
	}

	renderPage(
		logger,
		pageTemplates,
		w,
		http.StatusForbidden,
		"forbidden.html",
		forbiddenPageData{
			Authentication: authentication,
			Title:          "Доступ запрещён — SUP Rental",
			HomePath:       homePath,
		},
		"render forbidden page",
		"write forbidden response",
	)
}

func renderPage(
	logger *slog.Logger,
	pageTemplates *template.Template,
	w http.ResponseWriter,
	statusCode int,
	templateName string,
	data any,
	renderLogMessage string,
	writeLogMessage string,
) {
	var body bytes.Buffer
	if err := pageTemplates.ExecuteTemplate(&body, templateName, data); err != nil {
		logger.Error(renderLogMessage, slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(statusCode)
	if _, err := w.Write(body.Bytes()); err != nil {
		logger.Error(writeLogMessage, slog.Any("error", err))
	}
}
