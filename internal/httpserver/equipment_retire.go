package httpserver

import (
	"bytes"
	"errors"
	"html/template"
	"log/slog"
	"net/http"

	"github.com/Dyuzhovsergey/sup-rental/internal/equipment"
)

const retirementUnavailableMessage = "Списание оборудования в текущем состоянии недоступно."

type equipmentRetirePageData struct {
	Authentication  *authenticationView
	Title           string
	ID              int64
	InventoryNumber string
	Kind            string
	Status          string
}

func equipmentRetirement(
	logger *slog.Logger,
	service equipmentService,
	pageTemplates *template.Template,
	w http.ResponseWriter,
	r *http.Request,
) {
	switch r.Method {
	case http.MethodGet:
		showEquipmentRetirePage(logger, service, pageTemplates, w, r)
	case http.MethodPost:
		retireEquipment(logger, service, w, r)
	default:
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func showEquipmentRetirePage(
	logger *slog.Logger,
	service equipmentService,
	pageTemplates *template.Template,
	w http.ResponseWriter,
	r *http.Request,
) {
	id, ok := equipmentID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}

	item, err := service.Get(r.Context(), id)
	if errors.Is(err, equipment.ErrEquipmentNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		logger.Error("get equipment for retirement", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if !item.Status.CanTransitionTo(equipment.StatusRetired) {
		http.Error(w, retirementUnavailableMessage, http.StatusConflict)
		return
	}

	data := equipmentRetirePageData{
		Authentication:  authenticationForPage(r),
		Title:           "Списание оборудования — SUP Rental",
		ID:              item.ID,
		InventoryNumber: item.InventoryNumber,
		Kind:            equipmentKindLabel(item.Kind),
		Status:          equipmentStatusLabel(item.Status),
	}

	var body bytes.Buffer
	if err := pageTemplates.ExecuteTemplate(&body, "equipment_retire.html", data); err != nil {
		logger.Error("render equipment retirement page", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	if _, err := w.Write(body.Bytes()); err != nil {
		logger.Error("write equipment retirement response", slog.Any("error", err))
	}
}

func retireEquipment(
	logger *slog.Logger,
	service equipmentService,
	w http.ResponseWriter,
	r *http.Request,
) {
	id, ok := equipmentID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}

	retired, err := service.ChangeStatus(
		r.Context(),
		currentUser(r),
		id,
		equipment.StatusRetired,
	)
	if err == nil {
		http.Redirect(
			w,
			r,
			equipmentRedirectURL(equipmentNoticeRetired, retired),
			http.StatusSeeOther,
		)
		return
	}

	switch {
	case errors.Is(err, equipment.ErrEquipmentNotFound):
		http.NotFound(w, r)
	case errors.Is(err, equipment.ErrStatusTransitionNotAllowed):
		http.Error(w, retirementUnavailableMessage, http.StatusConflict)
	default:
		logger.Error("retire equipment", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}
