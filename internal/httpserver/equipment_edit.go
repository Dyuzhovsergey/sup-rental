package httpserver

import (
	"bytes"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/Dyuzhovsergey/sup-rental/internal/equipment"
)

type equipmentEditPageData struct {
	Authentication       *authenticationView
	Title                string
	ID                   int64
	Kinds                []equipmentKindOption
	Statuses             []equipmentStatusOption
	Form                 equipmentFormData
	CanRetire            bool
	InventoryNumberError string
	KindError            string
	StatusError          string
}

func showEquipmentEditPage(
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
	if err != nil {
		handleEquipmentEditError(logger, w, r, "get equipment for edit", err)
		return
	}

	if !item.Status.CanEditDetails() {
		http.Error(
			w,
			"Редактирование оборудования в текущем состоянии недоступно.",
			http.StatusConflict,
		)
		return
	}

	renderEquipmentEditPage(
		logger,
		pageTemplates,
		w,
		r,
		http.StatusOK,
		id,
		equipmentFormData{
			InventoryNumber: item.InventoryNumber,
			Kind:            string(item.Kind),
			Status:          string(item.Status),
		},
		"",
		"",
		"",
	)
}

func updateEquipment(
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

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	form := equipmentFormData{
		InventoryNumber: r.PostForm.Get("inventory_number"),
		Kind:            r.PostForm.Get("kind"),
		Status:          r.PostForm.Get("status"),
	}

	updated, err := service.Update(r.Context(), id, equipment.UpdateInput{
		InventoryNumber: form.InventoryNumber,
		Kind:            equipment.Kind(form.Kind),
		Status:          equipment.Status(form.Status),
	})
	if err == nil {
		http.Redirect(
			w,
			r,
			equipmentRedirectURL(equipmentNoticeUpdated, updated),
			http.StatusSeeOther,
		)
		return
	}

	switch {
	case errors.Is(err, equipment.ErrEquipmentNotFound):
		http.NotFound(w, r)
	case errors.Is(err, equipment.ErrEquipmentUpdateNotAllowed):
		http.Error(
			w,
			"Редактирование оборудования в текущем состоянии недоступно.",
			http.StatusConflict,
		)
	case errors.Is(err, equipment.ErrInventoryNumberRequired):
		renderEquipmentEditPage(
			logger,
			pageTemplates,
			w,
			r,
			http.StatusUnprocessableEntity,
			id,
			form,
			"Введите инвентарный номер.",
			"",
			"",
		)
	case errors.Is(err, equipment.ErrInvalidKind):
		renderEquipmentEditPage(
			logger,
			pageTemplates,
			w,
			r,
			http.StatusUnprocessableEntity,
			id,
			form,
			"",
			"Выберите тип оборудования.",
			"",
		)
	case errors.Is(err, equipment.ErrInvalidStatus):
		renderEquipmentEditPage(
			logger,
			pageTemplates,
			w,
			r,
			http.StatusUnprocessableEntity,
			id,
			form,
			"",
			"",
			"Выберите допустимый статус оборудования.",
		)
	case errors.Is(err, equipment.ErrStatusTransitionNotAllowed):
		renderEquipmentEditPage(
			logger,
			pageTemplates,
			w,
			r,
			http.StatusUnprocessableEntity,
			id,
			form,
			"",
			"",
			"Этот переход статуса недоступен в форме редактирования.",
		)
	case errors.Is(err, equipment.ErrInventoryNumberExists):
		renderEquipmentEditPage(
			logger,
			pageTemplates,
			w,
			r,
			http.StatusConflict,
			id,
			form,
			"Оборудование с таким инвентарным номером уже существует.",
			"",
			"",
		)
	default:
		handleEquipmentEditError(logger, w, r, "update equipment", err)
	}
}

func equipmentID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

func handleEquipmentEditError(
	logger *slog.Logger,
	w http.ResponseWriter,
	r *http.Request,
	message string,
	err error,
) {
	if errors.Is(err, equipment.ErrEquipmentNotFound) {
		http.NotFound(w, r)
		return
	}

	logger.Error(message, slog.Any("error", err))
	http.Error(w, "Internal Server Error", http.StatusInternalServerError)
}

func renderEquipmentEditPage(
	logger *slog.Logger,
	pageTemplates *template.Template,
	w http.ResponseWriter,
	r *http.Request,
	statusCode int,
	id int64,
	form equipmentFormData,
	inventoryNumberError string,
	kindError string,
	statusError string,
) {
	data := equipmentEditPageData{
		Authentication:       authenticationForPage(r),
		Title:                "Редактирование оборудования — SUP Rental",
		ID:                   id,
		Kinds:                equipmentKindOptions(),
		Statuses:             equipmentEditableStatusOptions(equipment.Status(form.Status)),
		Form:                 form,
		CanRetire:            equipment.Status(form.Status).CanTransitionTo(equipment.StatusRetired),
		InventoryNumberError: inventoryNumberError,
		KindError:            kindError,
		StatusError:          statusError,
	}

	var body bytes.Buffer
	if err := pageTemplates.ExecuteTemplate(&body, "equipment_edit.html", data); err != nil {
		logger.Error("render equipment edit page", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(statusCode)

	if _, err := w.Write(body.Bytes()); err != nil {
		logger.Error("write equipment edit response", slog.Any("error", err))
	}
}
