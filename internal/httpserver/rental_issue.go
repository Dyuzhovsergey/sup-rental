package httpserver

import (
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/Dyuzhovsergey/sup-rental/internal/client"
	"github.com/Dyuzhovsergey/sup-rental/internal/rental"
	"github.com/Dyuzhovsergey/sup-rental/internal/user"
)

type rentalIssuePageData struct {
	Authentication   *authenticationView
	Title            string
	RentalID         int64
	Client           client.Client
	Period           string
	Duration         string
	Items            []rentalItemView
	ItemCount        string
	IssuedAt         string
	ExpectedReturnAt string
	Conflicts        []rentalIssueConflictView
	HasConflicts     bool
	CanIssue         bool
	Changed          bool
}

type rentalIssueConflictView struct {
	OriginalID      int64
	InventoryNumber string
	ModelCode       string
	Options         []rentalIssueReplacementView
}

type rentalIssueReplacementView struct {
	ID              int64
	InventoryNumber string
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
	preview, err := rentals.PreviewIssue(r.Context(), id)
	if errors.Is(err, rental.ErrRentalNotFound) || errors.Is(err, rental.ErrInvalidRentalID) {
		http.NotFound(w, r)
		return
	}
	if errors.Is(err, rental.ErrStatusTransitionNotAllowed) {
		http.Error(w, "Выдать можно только подтверждённую аренду.", http.StatusConflict)
		return
	}
	if err != nil {
		logger.Error("get rental for issue", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	value := preview.Rental
	customer, err := clients.Get(r.Context(), value.ClientID)
	if err != nil {
		logger.Error("get rental client for issue", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	conflicts := make([]rentalIssueConflictView, 0, len(preview.Conflicts))
	canIssue := true
	for _, conflict := range preview.Conflicts {
		view := rentalIssueConflictView{OriginalID: conflict.Item.EquipmentID, InventoryNumber: conflict.Item.InventoryNumber, ModelCode: conflict.Item.ModelCode}
		for _, candidate := range conflict.Replacements {
			view.Options = append(view.Options, rentalIssueReplacementView{ID: candidate.ID, InventoryNumber: candidate.InventoryNumber})
		}
		if len(view.Options) == 0 {
			canIssue = false
		}
		conflicts = append(conflicts, view)
	}
	renderPage(logger, pageTemplates, w, http.StatusOK, "rental_issue.html", rentalIssuePageData{
		Authentication:   authenticationForPage(r),
		Title:            fmt.Sprintf("Выдача аренды №%d — SUP Rental", id),
		RentalID:         id,
		Client:           customer,
		Period:           rentalPeriodLabel(value.Interval),
		Duration:         rentalDurationLabel(value.Interval),
		Items:            rentalItemViews(value.Items()),
		ItemCount:        rentalItemCountLabel(value.ItemCount()),
		IssuedAt:         rentalDateTimeLabel(preview.IssuedAt),
		ExpectedReturnAt: rentalDateTimeLabel(preview.ExpectedReturnAt),
		Conflicts:        conflicts, HasConflicts: len(conflicts) > 0, CanIssue: canIssue,
		Changed: r.URL.Query().Get("changed") == "1",
	}, "render rental issue", "write rental issue response")
}

func issueRental(logger *slog.Logger, rentals rentalService, w http.ResponseWriter, r *http.Request) {
	id, ok := rentalIDFromPath(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Некорректные данные формы.", http.StatusBadRequest)
		return
	}
	replacements := make([]rental.EquipmentReplacement, 0)
	for name, values := range r.PostForm {
		if !strings.HasPrefix(name, "replacement_") || len(values) != 1 {
			continue
		}
		originalID, originalErr := strconv.ParseInt(strings.TrimPrefix(name, "replacement_"), 10, 64)
		replacementID, replacementErr := strconv.ParseInt(values[0], 10, 64)
		if originalErr != nil || replacementErr != nil || originalID <= 0 || replacementID <= 0 {
			http.Error(w, "Выберите корректную замену оборудования.", http.StatusUnprocessableEntity)
			return
		}
		replacements = append(replacements, rental.EquipmentReplacement{OriginalEquipmentID: originalID, ReplacementEquipmentID: replacementID})
	}
	_, err := rentals.IssueWithReplacements(r.Context(), currentUser(r), id, replacements)
	switch {
	case err == nil:
		http.Redirect(w, r, fmt.Sprintf("/rentals?issued=%d", id), http.StatusSeeOther)
	case errors.Is(err, rental.ErrRentalNotFound), errors.Is(err, rental.ErrInvalidRentalID):
		http.NotFound(w, r)
	case errors.Is(err, rental.ErrStatusTransitionNotAllowed):
		http.Error(w, "Выдать можно только подтверждённую аренду.", http.StatusConflict)
	case errors.Is(err, rental.ErrIssueConflict), errors.Is(err, rental.ErrInvalidReplacement), errors.Is(err, rental.ErrEquipmentUnavailable):
		http.Redirect(w, r, fmt.Sprintf("/rentals/%d/issue?changed=1", id), http.StatusSeeOther)
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
