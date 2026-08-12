package httpserver

import (
	"bytes"
	"errors"
	"html/template"
	"log/slog"
	"net/http"

	"github.com/Dyuzhovsergey/sup-rental/internal/equipment"
)

type equipmentDetailPageData struct {
	Title           string
	ID              int64
	InventoryNumber string
	Kind            string
	Status          string
	CanEdit         bool
	CanDelete       bool
}

func showEquipmentDetailPage(
	logger *slog.Logger,
	service equipmentService,
	pageTemplates *template.Template,
	w http.ResponseWriter,
	r *http.Request,
) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

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
		logger.Error("get equipment details", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	data := equipmentDetailPageData{
		Title:           item.InventoryNumber + " — SUP Rental",
		ID:              item.ID,
		InventoryNumber: item.InventoryNumber,
		Kind:            equipmentKindLabel(item.Kind),
		Status:          equipmentStatusLabel(item.Status),
		CanEdit:         item.Status.CanEditDetails(),
		CanDelete:       item.Status == equipment.StatusRetired,
	}

	var body bytes.Buffer
	if err := pageTemplates.ExecuteTemplate(&body, "equipment_detail.html", data); err != nil {
		logger.Error("render equipment detail page", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	if _, err := w.Write(body.Bytes()); err != nil {
		logger.Error("write equipment detail response", slog.Any("error", err))
	}
}
