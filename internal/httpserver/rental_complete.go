package httpserver

import (
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/Dyuzhovsergey/sup-rental/internal/client"
	"github.com/Dyuzhovsergey/sup-rental/internal/rental"
	"github.com/Dyuzhovsergey/sup-rental/internal/user"
)

type rentalCompletePageData struct {
	Authentication      *authenticationView
	Title               string
	RentalID            int64
	Client              client.Client
	Period              string
	Duration            string
	IssuedAt            string
	Items               []rentalItemView
	ItemCount           string
	ReturnedAt          string
	PlannedTotal        string
	Overdue             string
	BillableOverdue     string
	OverdueTotalInput   string
	PlannedTotalKopecks int64
	FinalTotal          string
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
	preview, err := rentals.PreviewSettlement(r.Context(), id)
	if errors.Is(err, rental.ErrRentalNotFound) || errors.Is(err, rental.ErrInvalidRentalID) {
		http.NotFound(w, r)
		return
	}
	if errors.Is(err, rental.ErrSettlementNotAvailable) || errors.Is(err, rental.ErrStatusTransitionNotAllowed) {
		http.Error(w, "Принять возврат можно только по активной аренде.", http.StatusConflict)
		return
	}
	if err != nil {
		logger.Error("get rental for completion", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	value := preview.Rental
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
		Authentication:      authenticationForPage(r),
		Title:               fmt.Sprintf("Возврат аренды №%d — SUP Rental", id),
		RentalID:            id,
		Client:              customer,
		Period:              rentalPeriodLabel(value.Interval),
		Duration:            rentalDurationLabel(value.Interval),
		IssuedAt:            rentalDateTimeLabel(issuedAt),
		Items:               rentalItemViews(value.Items()),
		ItemCount:           rentalItemCountLabel(value.ItemCount()),
		ReturnedAt:          rentalDateTimeLabel(preview.ReturnedAt),
		PlannedTotal:        rentalMoneyLabel(preview.Settlement.PlannedTotalKopecks),
		Overdue:             rentalOverdueLabel(preview.Settlement.OverdueDuration),
		BillableOverdue:     rentalBillableOverdueLabel(preview.Settlement.OverdueSlots),
		OverdueTotalInput:   rentalMoneyInputValue(preview.Settlement.CalculatedOverdueTotalKopecks),
		PlannedTotalKopecks: preview.Settlement.PlannedTotalKopecks,
		FinalTotal:          rentalMoneyLabel(preview.Settlement.FinalTotalKopecks),
	}, "render rental completion", "write rental completion response")
}

func completeRental(logger *slog.Logger, rentals rentalService, w http.ResponseWriter, r *http.Request) {
	id, ok := rentalIDFromPath(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Некорректные данные формы.", http.StatusBadRequest)
		return
	}
	overdueTotalKopecks, err := rentalMoneyKopecks(r.PostFormValue("overdue_total_rubles"))
	if err != nil {
		http.Error(w, "Введите неотрицательную сумму доплаты в рублях.", http.StatusUnprocessableEntity)
		return
	}
	_, err = rentals.Complete(r.Context(), currentUser(r), id, overdueTotalKopecks)
	switch {
	case err == nil:
		http.Redirect(w, r, "/rentals", http.StatusSeeOther)
	case errors.Is(err, rental.ErrRentalNotFound), errors.Is(err, rental.ErrInvalidRentalID):
		http.NotFound(w, r)
	case errors.Is(err, rental.ErrStatusTransitionNotAllowed):
		http.Error(w, "Принять возврат можно только по активной аренде.", http.StatusConflict)
	case errors.Is(err, rental.ErrEquipmentUnavailable):
		http.Error(w, "Состояние оборудования аренды изменилось. Возврат не выполнен.", http.StatusConflict)
	case errors.Is(err, rental.ErrInvalidOverdueTotal):
		http.Error(w, "Сумма доплаты слишком велика.", http.StatusUnprocessableEntity)
	case errors.Is(err, user.ErrAccessDenied):
		http.Error(w, "Forbidden", http.StatusForbidden)
	default:
		logger.Error("complete rental", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

func rentalMoneyInputValue(kopecks int64) string {
	if kopecks%100 == 0 {
		return strconv.FormatInt(kopecks/100, 10)
	}
	return fmt.Sprintf("%d.%02d", kopecks/100, kopecks%100)
}

func rentalMoneyKopecks(raw string) (int64, error) {
	value := strings.Replace(strings.TrimSpace(raw), ",", ".", 1)
	if value == "" || strings.Count(value, ".") > 1 || strings.HasPrefix(value, "-") {
		return 0, rental.ErrInvalidOverdueTotal
	}
	rublesPart, kopecksPart, hasFraction := strings.Cut(value, ".")
	if rublesPart == "" {
		return 0, rental.ErrInvalidOverdueTotal
	}
	rubles, err := strconv.ParseInt(rublesPart, 10, 64)
	if err != nil || rubles < 0 {
		return 0, rental.ErrInvalidOverdueTotal
	}
	kopecks := int64(0)
	if hasFraction {
		if len(kopecksPart) == 0 || len(kopecksPart) > 2 {
			return 0, rental.ErrInvalidOverdueTotal
		}
		if len(kopecksPart) == 1 {
			kopecksPart += "0"
		}
		kopecks, err = strconv.ParseInt(kopecksPart, 10, 64)
		if err != nil {
			return 0, rental.ErrInvalidOverdueTotal
		}
	}
	if rubles > (math.MaxInt64-kopecks)/100 {
		return 0, rental.ErrInvalidOverdueTotal
	}
	return rubles*100 + kopecks, nil
}
