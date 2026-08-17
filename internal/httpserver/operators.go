package httpserver

import (
	"bytes"
	"context"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Dyuzhovsergey/sup-rental/internal/password"
	"github.com/Dyuzhovsergey/sup-rental/internal/user"
)

type operatorService interface {
	List(ctx context.Context, actor user.User) ([]user.User, error)
	Get(ctx context.Context, actor user.User, id int64) (user.User, error)
	Create(ctx context.Context, actor user.User, login, plainPassword string) (user.User, error)
	Disable(ctx context.Context, actor user.User, id int64) (user.User, error)
	Activate(ctx context.Context, actor user.User, id int64) (user.User, error)
	ChangePassword(ctx context.Context, actor user.User, id int64, plainPassword string) (user.User, error)
}

type operatorView struct {
	ID          int64
	Login       string
	Role        string
	State       string
	Active      bool
	LastLoginAt string
}

type operatorFormData struct {
	Login string
}

type operatorsPageData struct {
	Authentication    *authenticationView
	Title             string
	Operators         []operatorView
	CountLabel        string
	Form              operatorFormData
	LoginError        string
	PasswordError     string
	ConfirmationError string
	Success           string
}

type operatorActionPageData struct {
	Authentication    *authenticationView
	Title             string
	Operator          operatorView
	PasswordError     string
	ConfirmationError string
}

var moscowTimeZone = time.FixedZone("МСК", 3*60*60)

func showOperatorsPage(
	logger *slog.Logger,
	service operatorService,
	pageTemplates *template.Template,
	w http.ResponseWriter,
	r *http.Request,
) {
	actor := currentUser(r)
	accounts, err := service.List(r.Context(), actor)
	if err != nil {
		handleOperatorError(logger, w, r, "list operators", err)
		return
	}
	renderOperatorsPage(logger, pageTemplates, w, r, http.StatusOK, accounts, operatorFormData{}, "", "", "")
}

func createOperator(
	logger *slog.Logger,
	service operatorService,
	pageTemplates *template.Template,
	w http.ResponseWriter,
	r *http.Request,
) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	form := operatorFormData{Login: strings.TrimSpace(r.PostForm.Get("login"))}
	plainPassword := r.PostForm.Get("password")
	confirmation := r.PostForm.Get("password_confirmation")

	if plainPassword != confirmation {
		renderOperatorsAfterCreateError(logger, service, pageTemplates, w, r, form, "", "", "Пароли не совпадают.")
		return
	}
	_, err := service.Create(r.Context(), currentUser(r), form.Login, plainPassword)
	if err == nil {
		http.Redirect(w, r, "/admin/operators?notice=created", http.StatusSeeOther)
		return
	}

	loginError, passwordError := operatorValidationErrors(err)
	if loginError != "" || passwordError != "" {
		renderOperatorsAfterCreateError(logger, service, pageTemplates, w, r, form, loginError, passwordError, "")
		return
	}
	handleOperatorError(logger, w, r, "create operator", err)
}

func renderOperatorsAfterCreateError(
	logger *slog.Logger,
	service operatorService,
	pageTemplates *template.Template,
	w http.ResponseWriter,
	r *http.Request,
	form operatorFormData,
	loginError, passwordError, confirmationError string,
) {
	accounts, err := service.List(r.Context(), currentUser(r))
	if err != nil {
		handleOperatorError(logger, w, r, "list operators after validation error", err)
		return
	}
	renderOperatorsPage(
		logger, pageTemplates, w, r, http.StatusUnprocessableEntity, accounts,
		form, loginError, passwordError, confirmationError,
	)
}

func showOperatorDisablePage(
	logger *slog.Logger,
	service operatorService,
	pageTemplates *template.Template,
	w http.ResponseWriter,
	r *http.Request,
) {
	account, ok := loadOperator(logger, service, w, r, "get operator for disabling")
	if !ok {
		return
	}
	if !account.Active {
		http.Error(w, "Оператор уже отключён.", http.StatusConflict)
		return
	}
	renderOperatorActionPage(logger, pageTemplates, w, r, http.StatusOK, "operator_disable.html", account, "", "")
}

func disableOperator(logger *slog.Logger, service operatorService, w http.ResponseWriter, r *http.Request) {
	id, ok := operatorID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	_, err := service.Disable(r.Context(), currentUser(r), id)
	if err == nil {
		http.Redirect(w, r, "/admin/operators?notice=disabled", http.StatusSeeOther)
		return
	}
	handleOperatorMutationError(logger, w, r, "disable operator", err)
}

func activateOperator(logger *slog.Logger, service operatorService, w http.ResponseWriter, r *http.Request) {
	id, ok := operatorID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	_, err := service.Activate(r.Context(), currentUser(r), id)
	if err == nil {
		http.Redirect(w, r, "/admin/operators?notice=activated", http.StatusSeeOther)
		return
	}
	handleOperatorMutationError(logger, w, r, "activate operator", err)
}

func showOperatorPasswordPage(
	logger *slog.Logger,
	service operatorService,
	pageTemplates *template.Template,
	w http.ResponseWriter,
	r *http.Request,
) {
	account, ok := loadOperator(logger, service, w, r, "get operator for password change")
	if !ok {
		return
	}
	renderOperatorActionPage(logger, pageTemplates, w, r, http.StatusOK, "operator_password.html", account, "", "")
}

func changeOperatorPassword(
	logger *slog.Logger,
	service operatorService,
	pageTemplates *template.Template,
	w http.ResponseWriter,
	r *http.Request,
) {
	id, ok := operatorID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	account, err := service.Get(r.Context(), currentUser(r), id)
	if err != nil {
		handleOperatorError(logger, w, r, "get operator for password change", err)
		return
	}
	plainPassword := r.PostForm.Get("password")
	if plainPassword != r.PostForm.Get("password_confirmation") {
		renderOperatorActionPage(logger, pageTemplates, w, r, http.StatusUnprocessableEntity, "operator_password.html", account, "", "Пароли не совпадают.")
		return
	}
	_, err = service.ChangePassword(r.Context(), currentUser(r), id, plainPassword)
	if err == nil {
		http.Redirect(w, r, "/admin/operators?notice=password_changed", http.StatusSeeOther)
		return
	}
	_, passwordError := operatorValidationErrors(err)
	if passwordError != "" {
		renderOperatorActionPage(logger, pageTemplates, w, r, http.StatusUnprocessableEntity, "operator_password.html", account, passwordError, "")
		return
	}
	handleOperatorMutationError(logger, w, r, "change operator password", err)
}

func loadOperator(logger *slog.Logger, service operatorService, w http.ResponseWriter, r *http.Request, message string) (user.User, bool) {
	id, ok := operatorID(r)
	if !ok {
		http.NotFound(w, r)
		return user.User{}, false
	}
	account, err := service.Get(r.Context(), currentUser(r), id)
	if err != nil {
		handleOperatorError(logger, w, r, message, err)
		return user.User{}, false
	}
	return account, true
}

func operatorID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

func currentUser(r *http.Request) user.User {
	authenticated, _ := authenticatedSession(r)
	return authenticated.User
}

func operatorValidationErrors(err error) (string, string) {
	switch {
	case errors.Is(err, user.ErrLoginRequired):
		return "Введите логин.", ""
	case errors.Is(err, user.ErrLoginTooShort):
		return "Логин должен содержать не менее 3 символов.", ""
	case errors.Is(err, user.ErrLoginTooLong):
		return "Логин должен содержать не более 32 символов.", ""
	case errors.Is(err, user.ErrInvalidLogin):
		return "Используйте латинские буквы, цифры, точку, дефис или подчёркивание.", ""
	case errors.Is(err, user.ErrLoginExists):
		return "Этот логин уже занят.", ""
	case errors.Is(err, password.ErrTooShort):
		return "", "Пароль должен содержать не менее 6 символов."
	case errors.Is(err, password.ErrTooLong):
		return "", "Пароль должен содержать не более 128 символов."
	case errors.Is(err, password.ErrInvalidUTF8):
		return "", "Пароль содержит недопустимые символы."
	default:
		return "", ""
	}
}

func handleOperatorMutationError(logger *slog.Logger, w http.ResponseWriter, r *http.Request, message string, err error) {
	switch {
	case errors.Is(err, user.ErrOperatorNotFound):
		http.NotFound(w, r)
	case errors.Is(err, user.ErrOperatorAlreadyActive):
		http.Error(w, "Оператор уже активен.", http.StatusConflict)
	case errors.Is(err, user.ErrOperatorAlreadyDisabled):
		http.Error(w, "Оператор уже отключён.", http.StatusConflict)
	default:
		handleOperatorError(logger, w, r, message, err)
	}
}

func handleOperatorError(logger *slog.Logger, w http.ResponseWriter, r *http.Request, message string, err error) {
	switch {
	case errors.Is(err, user.ErrOperatorNotFound):
		http.NotFound(w, r)
	case errors.Is(err, user.ErrAccessDenied):
		http.Error(w, "Forbidden", http.StatusForbidden)
	default:
		logger.Error(message, slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

func renderOperatorsPage(
	logger *slog.Logger,
	pageTemplates *template.Template,
	w http.ResponseWriter,
	r *http.Request,
	statusCode int,
	accounts []user.User,
	form operatorFormData,
	loginError, passwordError, confirmationError string,
) {
	data := operatorsPageData{
		Authentication: authenticationForPage(r), Title: "Операторы — SUP Rental",
		Operators: operatorViews(accounts), CountLabel: operatorCountLabel(len(accounts)), Form: form,
		LoginError: loginError, PasswordError: passwordError, ConfirmationError: confirmationError,
		Success: operatorSuccessMessage(r.URL.Query().Get("notice")),
	}
	renderOperatorTemplate(logger, pageTemplates, w, statusCode, "operators.html", data)
}

func renderOperatorActionPage(
	logger *slog.Logger, pageTemplates *template.Template, w http.ResponseWriter, r *http.Request,
	statusCode int, templateName string, account user.User, passwordError, confirmationError string,
) {
	data := operatorActionPageData{
		Authentication: authenticationForPage(r), Title: "Управление оператором — SUP Rental",
		Operator: operatorViewFor(account), PasswordError: passwordError, ConfirmationError: confirmationError,
	}
	renderOperatorTemplate(logger, pageTemplates, w, statusCode, templateName, data)
}

func renderOperatorTemplate(logger *slog.Logger, templates *template.Template, w http.ResponseWriter, statusCode int, name string, data any) {
	var body bytes.Buffer
	if err := templates.ExecuteTemplate(&body, name, data); err != nil {
		logger.Error("render operator page", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(statusCode)
	if _, err := w.Write(body.Bytes()); err != nil {
		logger.Error("write operator response", slog.Any("error", err))
	}
}

func operatorViews(accounts []user.User) []operatorView {
	views := make([]operatorView, 0, len(accounts))
	for _, account := range accounts {
		views = append(views, operatorViewFor(account))
	}
	return views
}

func operatorViewFor(account user.User) operatorView {
	lastLogin := "Ещё не входил"
	if account.LastLoginAt != nil {
		lastLogin = account.LastLoginAt.In(moscowTimeZone).Format("02.01.2006 15:04")
	}
	state := "Отключён"
	if account.Active {
		state = "Активен"
	}
	return operatorView{ID: account.ID, Login: account.Login, Role: "Оператор проката", State: state, Active: account.Active, LastLoginAt: lastLogin}
}

func operatorCountLabel(count int) string {
	word := "операторов"
	if count%10 == 1 && count%100 != 11 {
		word = "оператор"
	} else if count%10 >= 2 && count%10 <= 4 && (count%100 < 12 || count%100 > 14) {
		word = "оператора"
	}
	return strconv.Itoa(count) + " " + word
}

func operatorSuccessMessage(notice string) string {
	switch notice {
	case "created":
		return "Оператор создан."
	case "disabled":
		return "Оператор отключён, его активные сессии завершены."
	case "activated":
		return "Оператор снова активен."
	case "password_changed":
		return "Пароль оператора изменён, его активные сессии завершены."
	default:
		return ""
	}
}
