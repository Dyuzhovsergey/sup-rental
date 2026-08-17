package httpserver

import (
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/Dyuzhovsergey/sup-rental/internal/rental"
	"github.com/Dyuzhovsergey/sup-rental/internal/user"
)

const bulkSelectionMessage = "Выберите от 1 до 15 подтверждённых аренд."

type rentalBulkPageData struct {
	Authentication *authenticationView
	Title          string
	Heading        string
	Description    string
	Warning        string
	SubmitLabel    string
	Action         string
	Rentals        []rentalBulkView
	RentalCount    string
	EquipmentCount string
	IsCancellation bool
}

type rentalBulkView struct {
	ID           int64
	ClientName   string
	Period       string
	ItemCount    string
	PlannedTotal string
}

func showBulkRentalIssuePage(
	logger *slog.Logger,
	rentals rentalService,
	clients clientService,
	pageTemplates *template.Template,
	w http.ResponseWriter,
	r *http.Request,
) {
	showRentalBulkPage(logger, rentals, clients, pageTemplates, w, r, false)
}

func showBulkRentalCancelPage(
	logger *slog.Logger,
	rentals rentalService,
	clients clientService,
	pageTemplates *template.Template,
	w http.ResponseWriter,
	r *http.Request,
) {
	showRentalBulkPage(logger, rentals, clients, pageTemplates, w, r, true)
}

func showRentalBulkPage(
	logger *slog.Logger,
	rentals rentalService,
	clients clientService,
	pageTemplates *template.Template,
	w http.ResponseWriter,
	r *http.Request,
	isCancellation bool,
) {
	ids, err := rentalIDsFromValues(r.URL.Query())
	if err != nil {
		http.Error(w, bulkSelectionMessage, http.StatusUnprocessableEntity)
		return
	}
	views := make([]rentalBulkView, 0, len(ids))
	totalEquipment := 0
	for _, id := range ids {
		value, err := rentals.Get(r.Context(), id)
		switch {
		case errors.Is(err, rental.ErrRentalNotFound), errors.Is(err, rental.ErrInvalidRentalID):
			http.NotFound(w, r)
			return
		case err != nil:
			logger.Error("get rental for bulk action", slog.Int64("rental_id", id), slog.Any("error", err))
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		case value.Status != rental.StatusConfirmed:
			http.Error(w, "Выбранные аренды изменились. Вернитесь к списку и повторите выбор.", http.StatusConflict)
			return
		}
		customer, err := clients.Get(r.Context(), value.ClientID)
		if err != nil {
			logger.Error("get client for bulk rental action", slog.Int64("rental_id", id), slog.Any("error", err))
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		total, err := value.PlannedTotalKopecks()
		if err != nil {
			logger.Error("calculate bulk rental total", slog.Int64("rental_id", id), slog.Any("error", err))
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		views = append(views, rentalBulkView{
			ID: id, ClientName: customer.FullName, Period: rentalPeriodLabel(value.Interval),
			ItemCount: rentalItemCountLabel(value.ItemCount()), PlannedTotal: rentalMoneyLabel(total),
		})
		totalEquipment += value.ItemCount()
	}

	data := rentalBulkPageData{
		Authentication: authenticationForPage(r), Rentals: views,
		RentalCount: rentalCountLabel(len(views)), EquipmentCount: rentalItemCountLabel(totalEquipment),
	}
	if isCancellation {
		data.Title = "Массовая отмена аренд — SUP Rental"
		data.Heading = "Отменить выбранные аренды?"
		data.Description = "Проверьте список перед снятием резервирования оборудования."
		data.Warning = "Все выбранные аренды будут отменены. Аренды и их состав останутся в истории."
		data.SubmitLabel = "Подтвердить отмену"
		data.Action = "/rentals/bulk/cancel"
		data.IsCancellation = true
	} else {
		data.Title = "Массовая выдача аренд — SUP Rental"
		data.Heading = "Выдать оборудование по выбранным арендам?"
		data.Description = "Проверьте клиентов, периоды и состав перед фактической выдачей."
		data.Warning = "Все аренды будут выданы одновременно. Если одна аренда или единица оборудования недоступна, вся операция будет отменена."
		data.SubmitLabel = "Подтвердить выдачу"
		data.Action = "/rentals/bulk/issue"
	}
	renderPage(
		logger, pageTemplates, w, http.StatusOK, "rental_bulk.html", data,
		"render bulk rental confirmation", "write bulk rental confirmation response",
	)
}

func issueSelectedRentals(logger *slog.Logger, rentals rentalService, w http.ResponseWriter, r *http.Request) {
	ids, ok := bulkRentalIDsFromPost(w, r)
	if !ok {
		return
	}
	values, err := rentals.IssueMany(r.Context(), currentUser(r), ids)
	if writeBulkRentalError(logger, w, r, err, "issue selected rentals") {
		return
	}
	http.Redirect(w, r, "/rentals?bulk_issued="+strconv.Itoa(len(values)), http.StatusSeeOther)
}

func cancelSelectedRentals(logger *slog.Logger, rentals rentalService, w http.ResponseWriter, r *http.Request) {
	ids, ok := bulkRentalIDsFromPost(w, r)
	if !ok {
		return
	}
	values, err := rentals.CancelMany(r.Context(), currentUser(r), ids)
	if writeBulkRentalError(logger, w, r, err, "cancel selected rentals") {
		return
	}
	http.Redirect(w, r, "/rentals?bulk_cancelled="+strconv.Itoa(len(values)), http.StatusSeeOther)
}

func bulkRentalIDsFromPost(w http.ResponseWriter, r *http.Request) ([]int64, bool) {
	ids, err := rentalIDsFromValues(r.PostForm)
	if err != nil {
		http.Error(w, bulkSelectionMessage, http.StatusUnprocessableEntity)
		return nil, false
	}
	return ids, true
}

func rentalIDsFromValues(values url.Values) ([]int64, error) {
	rawIDs := values["rental_id"]
	if len(rawIDs) == 0 || len(rawIDs) > rental.MaxBulkSelection {
		return nil, rental.ErrInvalidBulkSelection
	}
	ids := make([]int64, 0, len(rawIDs))
	seen := make(map[int64]struct{}, len(rawIDs))
	for _, raw := range rawIDs {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			return nil, rental.ErrInvalidBulkSelection
		}
		if _, exists := seen[id]; exists {
			return nil, rental.ErrInvalidBulkSelection
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids, nil
}

func writeBulkRentalError(logger *slog.Logger, w http.ResponseWriter, r *http.Request, err error, message string) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, rental.ErrInvalidBulkSelection):
		http.Error(w, bulkSelectionMessage, http.StatusUnprocessableEntity)
	case errors.Is(err, rental.ErrRentalNotFound), errors.Is(err, rental.ErrInvalidRentalID):
		http.NotFound(w, r)
	case errors.Is(err, rental.ErrStatusTransitionNotAllowed), errors.Is(err, rental.ErrEquipmentUnavailable):
		http.Error(w, "Выбранные аренды изменились. Вернитесь к списку и повторите выбор.", http.StatusConflict)
	case errors.Is(err, user.ErrAccessDenied):
		http.Error(w, "Forbidden", http.StatusForbidden)
	default:
		logger.Error(message, slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
	return true
}
