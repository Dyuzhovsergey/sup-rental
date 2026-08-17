package httpserver

import (
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/Dyuzhovsergey/sup-rental/internal/client"
	"github.com/Dyuzhovsergey/sup-rental/internal/rental"
	"github.com/Dyuzhovsergey/sup-rental/internal/user"
)

type rentalIssuePageData struct {
	Authentication *authenticationView
	Title          string
	RentalID       int64
	Client         client.Client
	Period         string
	Duration       string
	Items          []rentalItemView
	ItemCount      string
}

func showRentalIssuePage(
	logger *slog.Logger,
	rentals rentalService,
	clients clientService,
	pageTemplates *template.Template,
	w http.ResponseWriter,
	r *http.Request,
) {
	id, ok := rentalIDFromPath(w, r)
	if !ok {
		return
	}
	value, err := rentals.Get(r.Context(), id)
	if errors.Is(err, rental.ErrRentalNotFound) || errors.Is(err, rental.ErrInvalidRentalID) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		logger.Error("get rental for issue", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	if value.Status != rental.StatusConfirmed {
		http.Error(w, "Выдать можно только подтверждённую аренду.", http.StatusConflict)
		return
	}
	customer, err := clients.Get(r.Context(), value.ClientID)
	if err != nil {
		logger.Error("get rental client for issue", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	renderPage(logger, pageTemplates, w, http.StatusOK, "rental_issue.html", rentalIssuePageData{
		Authentication: authenticationForPage(r),
		Title:          fmt.Sprintf("Выдача аренды №%d — SUP Rental", id),
		RentalID:       id,
		Client:         customer,
		Period:         rentalPeriodLabel(value.Interval),
		Duration:       rentalDurationLabel(value.Interval),
		Items:          rentalItemViews(value.Items()),
		ItemCount:      rentalItemCountLabel(value.ItemCount()),
	}, "render rental issue", "write rental issue response")
}

func issueRental(logger *slog.Logger, rentals rentalService, w http.ResponseWriter, r *http.Request) {
	id, ok := rentalIDFromPath(w, r)
	if !ok {
		return
	}
	_, err := rentals.Issue(r.Context(), currentUser(r), id)
	switch {
	case err == nil:
		http.Redirect(w, r, fmt.Sprintf("/rentals?issued=%d", id), http.StatusSeeOther)
	case errors.Is(err, rental.ErrRentalNotFound), errors.Is(err, rental.ErrInvalidRentalID):
		http.NotFound(w, r)
	case errors.Is(err, rental.ErrStatusTransitionNotAllowed):
		http.Error(w, "Выдать можно только подтверждённую аренду.", http.StatusConflict)
	case errors.Is(err, rental.ErrEquipmentUnavailable):
		http.Error(w, "Оборудование аренды сейчас недоступно для выдачи.", http.StatusConflict)
	case errors.Is(err, user.ErrAccessDenied):
		http.Error(w, "Forbidden", http.StatusForbidden)
	default:
		logger.Error("issue rental", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

func rentalIDFromPath(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return 0, false
	}
	return id, true
}
