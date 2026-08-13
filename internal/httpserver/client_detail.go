package httpserver

import (
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/Dyuzhovsergey/sup-rental/internal/client"
)

type clientDetailPageData struct {
	Authentication *authenticationView
	Title          string
	Client         client.Client
	CanEdit        bool
}

func showClientDetailPage(
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
	if errors.Is(err, client.ErrClientNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		logger.Error("get client details", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	authentication := authenticationForPage(r)
	data := clientDetailPageData{
		Authentication: authentication,
		Title:          customer.FullName + " — SUP Rental",
		Client:         customer,
		CanEdit:        authentication != nil && authentication.IsOperator,
	}
	renderPage(
		logger, pageTemplates, w, http.StatusOK, "client_detail.html", data,
		"render client detail page", "write client detail response",
	)
}

func clientID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}
