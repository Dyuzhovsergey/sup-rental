package httpserver

import (
	"context"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Dyuzhovsergey/sup-rental/internal/client"
	"github.com/Dyuzhovsergey/sup-rental/internal/user"
)

type clientService interface {
	Create(context.Context, user.User, string, string) (client.Client, error)
	Get(context.Context, int64) (client.Client, error)
	FindByPhone(context.Context, string) (client.Client, error)
	ListPage(context.Context, int) (client.Page, error)
}

type clientFormData struct {
	FullName      string
	Phone         string
	FullNameError string
	PhoneError    string
}

type clientsPageData struct {
	Authentication *authenticationView
	Title          string
	Clients        []client.Client
	Form           clientFormData
	SearchPhone    string
	SearchError    string
	SearchActive   bool
	SearchEmpty    bool
	Success        string
	TotalLabel     string
	PageLabel      string
	PreviousURL    string
	NextURL        string
	HasPrevious    bool
	HasNext        bool
}

func clientsPage(logger *slog.Logger, service clientService, pageTemplates *template.Template, w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		createClient(logger, service, pageTemplates, w, r)
		return
	}
	showClientsPage(logger, service, pageTemplates, w, r, http.StatusOK, clientFormData{})
}

func showClientsPage(logger *slog.Logger, service clientService, pageTemplates *template.Template, w http.ResponseWriter, r *http.Request, status int, form clientFormData) {
	page, ok := positiveQueryPage(r.URL.Query().Get("page"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	result, err := service.ListPage(r.Context(), page)
	if err != nil {
		if errors.Is(err, client.ErrInvalidPage) {
			http.NotFound(w, r)
			return
		}
		logger.Error("list clients", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	data := clientsPageData{
		Authentication: authenticationForPage(r), Title: "Клиенты — SUP Rental",
		Clients: result.Clients, Form: form, TotalLabel: clientCountLabel(result.Total),
	}
	phone := strings.TrimSpace(r.URL.Query().Get("phone"))
	if phone != "" {
		data.SearchActive = true
		data.SearchPhone = phone
		found, findErr := service.FindByPhone(r.Context(), phone)
		switch {
		case findErr == nil:
			data.Clients = []client.Client{found}
		case errors.Is(findErr, client.ErrClientNotFound):
			data.Clients = nil
			data.SearchEmpty = true
		case errors.Is(findErr, client.ErrPhoneRequired), errors.Is(findErr, client.ErrInvalidPhone):
			data.Clients = nil
			data.SearchError = "Введите корректный номер телефона."
			status = http.StatusUnprocessableEntity
		default:
			logger.Error("find client by phone", slog.Any("error", findErr))
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
	} else {
		totalPages := pageCount(result.Total, client.PageSize)
		if page > totalPages {
			http.NotFound(w, r)
			return
		}
		data.PageLabel = pageLabel(page, totalPages)
		data.HasPrevious = page > 1
		data.HasNext = page < totalPages
		data.PreviousURL = clientPageURL(page - 1)
		data.NextURL = clientPageURL(page + 1)
	}

	if createdID, parseErr := positiveOptionalID(r.URL.Query().Get("created")); parseErr == nil && createdID > 0 {
		created, getErr := service.Get(r.Context(), createdID)
		if getErr == nil {
			data.Success = "Клиент " + created.FullName + " создан."
		} else if !errors.Is(getErr, client.ErrClientNotFound) {
			logger.Error("load created client notice", slog.Any("error", getErr))
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
	}
	renderPage(logger, pageTemplates, w, status, "clients.html", data, "render clients page", "write clients response")
}

func createClient(logger *slog.Logger, service clientService, pageTemplates *template.Template, w http.ResponseWriter, r *http.Request) {
	form := clientFormData{FullName: r.PostForm.Get("full_name"), Phone: r.PostForm.Get("phone")}
	authenticated, ok := authenticatedSession(r)
	if !ok {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	created, err := service.Create(r.Context(), authenticated.User, form.FullName, form.Phone)
	if err == nil {
		http.Redirect(w, r, "/clients?created="+strconv.FormatInt(created.ID, 10), http.StatusSeeOther)
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
	case errors.Is(err, user.ErrAccessDenied):
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	default:
		logger.Error("create client", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	showClientsPage(logger, service, pageTemplates, w, r, status, form)
}

func positiveQueryPage(raw string) (int, bool) {
	if raw == "" {
		return 1, true
	}
	page, err := strconv.Atoi(raw)
	return page, err == nil && page > 0
}

func positiveOptionalID(raw string) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid ID")
	}
	return id, nil
}

func clientPageURL(page int) string {
	if page <= 1 {
		return "/clients"
	}
	query := url.Values{"page": {strconv.Itoa(page)}}
	return "/clients?" + query.Encode()
}

func clientCountLabel(count int) string {
	word := "клиентов"
	if count%10 == 1 && count%100 != 11 {
		word = "клиент"
	} else if count%10 >= 2 && count%10 <= 4 && (count%100 < 12 || count%100 > 14) {
		word = "клиента"
	}
	return strconv.Itoa(count) + " " + word
}
