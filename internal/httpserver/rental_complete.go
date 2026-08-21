package httpserver

import (
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"

	"github.com/Dyuzhovsergey/sup-rental/internal/client"
	"github.com/Dyuzhovsergey/sup-rental/internal/rental"
	"github.com/Dyuzhovsergey/sup-rental/internal/user"
)

type rentalCompletePageData struct {
	Authentication *authenticationView
	Title          string
	RentalID       int64
	Client         client.Client
	Period         string
	Duration       string
	IssuedAt       string
	Items          []rentalItemView
	ItemCount      string
}

func showRentalCompletePage(
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
		logger.Error("get rental for completion", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	if value.Status != rental.StatusActive {
		http.Error(w, "Принять возврат можно только по активной аренде.", http.StatusConflict)
		return
	}
	customer, err := clients.Get(r.Context(), value.ClientID)
	if err != nil {
		logger.Error("get rental client for completion", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	issuedAt, ok := value.IssuedAt()
	if !ok {
		logger.Error("active rental has no issued time", slog.Int64("rental_id", id))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	renderPage(logger, pageTemplates, w, http.StatusOK, "rental_complete.html", rentalCompletePageData{
		Authentication: authenticationForPage(r),
		Title:          fmt.Sprintf("Возврат аренды №%d — SUP Rental", id),
		RentalID:       id,
		Client:         customer,
		Period:         rentalPeriodLabel(value.Interval),
		Duration:       rentalDurationLabel(value.Interval),
		IssuedAt:       rentalDateTimeLabel(issuedAt),
		Items:          rentalItemViews(value.Items()),
		ItemCount:      rentalItemCountLabel(value.ItemCount()),
	}, "render rental completion", "write rental completion response")
}

func completeRental(logger *slog.Logger, rentals rentalService, w http.ResponseWriter, r *http.Request) {
	id, ok := rentalIDFromPath(w, r)
	if !ok {
		return
	}
	_, err := rentals.Complete(r.Context(), currentUser(r), id)
	switch {
	case err == nil:
		http.Redirect(w, r, "/rentals", http.StatusSeeOther)
	case errors.Is(err, rental.ErrRentalNotFound), errors.Is(err, rental.ErrInvalidRentalID):
		http.NotFound(w, r)
	case errors.Is(err, rental.ErrStatusTransitionNotAllowed):
		http.Error(w, "Принять возврат можно только по активной аренде.", http.StatusConflict)
	case errors.Is(err, rental.ErrEquipmentUnavailable):
		http.Error(w, "Состояние оборудования аренды изменилось. Возврат не выполнен.", http.StatusConflict)
	case errors.Is(err, user.ErrAccessDenied):
		http.Error(w, "Forbidden", http.StatusForbidden)
	default:
		logger.Error("complete rental", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}
