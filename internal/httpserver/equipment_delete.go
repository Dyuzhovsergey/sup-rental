package httpserver

import (
	"bytes"
	"errors"
	"html/template"
	"log/slog"
	"net/http"

	"github.com/Dyuzhovsergey/sup-rental/internal/equipment"
)

const deletionUnavailableMessage = "Перед удалением оборудование необходимо списать."

type equipmentDeletePageData struct {
	Title           string
	ID              int64
	InventoryNumber string
	Kind            string
	Status          string
	CanDelete       bool
}

func equipmentDeletion(
	logger *slog.Logger,
	service equipmentService,
	pageTemplates *template.Template,
	w http.ResponseWriter,
	r *http.Request,
) {
	switch r.Method {
	case http.MethodGet:
		showEquipmentDeletePage(logger, service, pageTemplates, w, r)
	case http.MethodPost:
		deleteEquipment(logger, service, w, r)
	default:
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func showEquipmentDeletePage(
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
		logger.Error("get equipment for deletion", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	data := equipmentDeletePageData{
		Title:           "Удаление оборудования — SUP Rental",
		ID:              item.ID,
		InventoryNumber: item.InventoryNumber,
		Kind:            equipmentKindLabel(item.Kind),
		Status:          equipmentStatusLabel(item.Status),
		CanDelete:       item.Status == equipment.StatusRetired,
	}

	var body bytes.Buffer
	if err := pageTemplates.ExecuteTemplate(&body, "equipment_delete.html", data); err != nil {
		logger.Error("render equipment deletion page", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	statusCode := http.StatusOK
	if !data.CanDelete {
		statusCode = http.StatusConflict
	}
	w.WriteHeader(statusCode)

	if _, err := w.Write(body.Bytes()); err != nil {
		logger.Error("write equipment deletion response", slog.Any("error", err))
	}
}

func deleteEquipment(
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

	deleted, err := service.Delete(r.Context(), id)
	if err == nil {
		http.Redirect(
			w,
			r,
			equipmentRedirectURL(equipmentNoticeDeleted, deleted),
			http.StatusSeeOther,
		)
		return
	}

	switch {
	case errors.Is(err, equipment.ErrEquipmentNotFound):
		http.NotFound(w, r)
	case errors.Is(err, equipment.ErrEquipmentDeleteNotAllowed):
		http.Error(w, deletionUnavailableMessage, http.StatusConflict)
	case errors.Is(err, equipment.ErrEquipmentHasHistory):
		http.Error(
			w,
			"Оборудование связано с историей операций и не может быть удалено.",
			http.StatusConflict,
		)
	default:
		logger.Error("delete equipment", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}
