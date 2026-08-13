package httpserver

import (
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/Dyuzhovsergey/sup-rental/internal/client"
	"github.com/Dyuzhovsergey/sup-rental/internal/user"
)

type clientEditPageData struct {
	Authentication *authenticationView
	Title          string
	ID             int64
	Form           clientFormData
}

func showClientEditPage(
	logger *slog.Logger,
	service clientService,
	pageTemplates *template.Template,
	w http.ResponseWriter,
	r *http.Request,
) {
	id, ok := clientID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	customer, err := service.Get(r.Context(), id)
	if err != nil {
		handleClientEditLoadError(logger, w, r, "get client for edit", err)
		return
	}
	renderClientEditPage(logger, pageTemplates, w, r, http.StatusOK, id, clientFormData{
		FullName: customer.FullName,
		Phone:    string(customer.Phone),
	})
}

func updateClient(
	logger *slog.Logger,
	service clientService,
	pageTemplates *template.Template,
	w http.ResponseWriter,
	r *http.Request,
) {
	id, ok := clientID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	form := clientFormData{
		FullName: r.PostForm.Get("full_name"),
		Phone:    r.PostForm.Get("phone"),
	}
	updated, err := service.Update(
		r.Context(), currentUser(r), id, form.FullName, form.Phone,
	)
	if err == nil {
		http.Redirect(
			w, r, "/clients?updated="+strconv.FormatInt(updated.ID, 10),
			http.StatusSeeOther,
		)
		return
	}

	status := http.StatusUnprocessableEntity
	switch {
	case errors.Is(err, client.ErrFullNameRequired):
		form.FullNameError = "Укажите ФИО клиента."
	case errors.Is(err, client.ErrFullNameTooLong):
		form.FullNameError = "ФИО должно содержать не более 200 символов."
	case errors.Is(err, client.ErrInvalidFullName):
		form.FullNameError = "ФИО содержит недопустимые символы."
	case errors.Is(err, client.ErrPhoneRequired):
		form.PhoneError = "Укажите номер телефона."
	case errors.Is(err, client.ErrInvalidPhone):
		form.PhoneError = "Введите корректный номер телефона."
	case errors.Is(err, client.ErrPhoneExists):
		form.PhoneError = "Клиент с таким номером уже существует."
		status = http.StatusConflict
	case errors.Is(err, client.ErrClientNotFound):
		http.NotFound(w, r)
		return
	case errors.Is(err, user.ErrAccessDenied):
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	default:
		logger.Error("update client", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	renderClientEditPage(logger, pageTemplates, w, r, status, id, form)
}

func renderClientEditPage(
	logger *slog.Logger,
	pageTemplates *template.Template,
	w http.ResponseWriter,
	r *http.Request,
	status int,
	id int64,
	form clientFormData,
) {
	data := clientEditPageData{
		Authentication: authenticationForPage(r),
		Title:          "Редактирование клиента — SUP Rental",
		ID:             id,
		Form:           form,
	}
	renderPage(
		logger, pageTemplates, w, status, "client_edit.html", data,
		"render client edit page", "write client edit response",
	)
}

func handleClientEditLoadError(
	logger *slog.Logger,
	w http.ResponseWriter,
	r *http.Request,
	message string,
	err error,
) {
	if errors.Is(err, client.ErrClientNotFound) {
		http.NotFound(w, r)
		return
	}
	logger.Error(message, slog.Any("error", err))
	http.Error(w, "Internal Server Error", http.StatusInternalServerError)
}
