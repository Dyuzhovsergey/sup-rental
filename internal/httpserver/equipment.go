package httpserver

import (
	"bytes"
	"context"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/Dyuzhovsergey/sup-rental/internal/equipment"
)

type equipmentService interface {
	Create(ctx context.Context, input equipment.CreateInput) (equipment.Item, error)
	List(ctx context.Context) ([]equipment.Item, error)
	Get(ctx context.Context, id int64) (equipment.Item, error)
	Update(ctx context.Context, id int64, input equipment.UpdateInput) (equipment.Item, error)
	ChangeStatus(ctx context.Context, id int64, target equipment.Status) (equipment.Item, error)
}

type equipmentPageData struct {
	Title   string
	Items   []equipmentItemView
	Kinds   []equipmentKindOption
	Form    equipmentFormData
	Error   string
	Success string
}

type equipmentItemView struct {
	ID              int64
	InventoryNumber string
	Kind            string
	Status          string
	StatusOptions   []equipmentStatusOption
	CanEdit         bool
}

type equipmentKindOption struct {
	Value string
	Label string
}

type equipmentStatusOption struct {
	Value string
	Label string
}

type equipmentFormData struct {
	InventoryNumber string
	Kind            string
}

func equipmentPage(
	logger *slog.Logger,
	service equipmentService,
	pageTemplates *template.Template,
	w http.ResponseWriter,
	r *http.Request,
) {
	switch r.Method {
	case http.MethodGet:
		renderEquipmentPage(
			logger,
			service,
			pageTemplates,
			w,
			r,
			http.StatusOK,
			equipmentFormData{},
			"",
		)
	case http.MethodPost:
		createEquipment(logger, service, pageTemplates, w, r)
	default:
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func createEquipment(
	logger *slog.Logger,
	service equipmentService,
	pageTemplates *template.Template,
	w http.ResponseWriter,
	r *http.Request,
) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	form := equipmentFormData{
		InventoryNumber: r.PostForm.Get("inventory_number"),
		Kind:            r.PostForm.Get("kind"),
	}

	_, err := service.Create(r.Context(), equipment.CreateInput{
		InventoryNumber: form.InventoryNumber,
		Kind:            equipment.Kind(form.Kind),
	})
	if err == nil {
		http.Redirect(w, r, "/equipment", http.StatusSeeOther)
		return
	}

	switch {
	case errors.Is(err, equipment.ErrInventoryNumberRequired):
		renderEquipmentPage(
			logger,
			service,
			pageTemplates,
			w,
			r,
			http.StatusUnprocessableEntity,
			form,
			"Введите инвентарный номер.",
		)
	case errors.Is(err, equipment.ErrInvalidKind):
		renderEquipmentPage(
			logger,
			service,
			pageTemplates,
			w,
			r,
			http.StatusUnprocessableEntity,
			form,
			"Выберите тип оборудования.",
		)
	case errors.Is(err, equipment.ErrInventoryNumberExists):
		renderEquipmentPage(
			logger,
			service,
			pageTemplates,
			w,
			r,
			http.StatusConflict,
			form,
			"Оборудование с таким инвентарным номером уже существует.",
		)
	default:
		logger.Error(
			"create equipment",
			slog.Any("error", err),
		)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

func changeEquipmentStatus(
	logger *slog.Logger,
	service equipmentService,
	pageTemplates *template.Template,
	w http.ResponseWriter,
	r *http.Request,
) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	_, err = service.ChangeStatus(
		r.Context(),
		id,
		equipment.Status(r.PostForm.Get("status")),
	)
	if err == nil {
		http.Redirect(w, r, "/equipment", http.StatusSeeOther)
		return
	}

	switch {
	case errors.Is(err, equipment.ErrEquipmentNotFound):
		http.NotFound(w, r)
	case errors.Is(err, equipment.ErrInvalidStatus):
		renderEquipmentPage(
			logger,
			service,
			pageTemplates,
			w,
			r,
			http.StatusUnprocessableEntity,
			equipmentFormData{},
			"Выберите допустимое состояние оборудования.",
		)
	case errors.Is(err, equipment.ErrStatusTransitionNotAllowed):
		renderEquipmentPage(
			logger,
			service,
			pageTemplates,
			w,
			r,
			http.StatusUnprocessableEntity,
			equipmentFormData{},
			"Этот переход состояния сейчас недоступен.",
		)
	default:
		logger.Error(
			"change equipment status",
			slog.Any("error", err),
		)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

func renderEquipmentPage(
	logger *slog.Logger,
	service equipmentService,
	pageTemplates *template.Template,
	w http.ResponseWriter,
	r *http.Request,
	statusCode int,
	form equipmentFormData,
	errorMessage string,
) {
	items, err := service.List(r.Context())
	if err != nil {
		logger.Error(
			"list equipment",
			slog.Any("error", err),
		)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	data := equipmentPageData{
		Title: "Оборудование — SUP Rental",
		Items: equipmentItemsView(items),
		Kinds: equipmentKindOptions(),
		Form:  form,
		Error: errorMessage,
	}
	if r.URL.Query().Get("updated") == "1" {
		data.Success = "Оборудование обновлено."
	}

	var body bytes.Buffer
	if err := pageTemplates.ExecuteTemplate(&body, "equipment.html", data); err != nil {
		logger.Error(
			"render equipment page",
			slog.Any("error", err),
		)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(statusCode)

	if _, err := w.Write(body.Bytes()); err != nil {
		logger.Error(
			"write equipment response",
			slog.Any("error", err),
		)
	}
}

func equipmentItemsView(items []equipment.Item) []equipmentItemView {
	result := make([]equipmentItemView, 0, len(items))
	for _, item := range items {
		result = append(result, equipmentItemView{
			ID:              item.ID,
			InventoryNumber: item.InventoryNumber,
			Kind:            equipmentKindLabel(item.Kind),
			Status:          equipmentStatusLabel(item.Status),
			StatusOptions:   equipmentStatusOptions(item.Status),
			CanEdit:         item.Status.CanEditDetails(),
		})
	}

	return result
}

func equipmentStatusOptions(status equipment.Status) []equipmentStatusOption {
	switch status {
	case equipment.StatusAvailable:
		return []equipmentStatusOption{
			{Value: string(equipment.StatusMaintenance), Label: "На обслуживании"},
			{Value: string(equipment.StatusRetired), Label: "Списан"},
		}
	case equipment.StatusMaintenance:
		return []equipmentStatusOption{
			{Value: string(equipment.StatusAvailable), Label: "Доступен"},
			{Value: string(equipment.StatusRetired), Label: "Списан"},
		}
	default:
		return nil
	}
}

func equipmentKindOptions() []equipmentKindOption {
	return []equipmentKindOption{
		{Value: string(equipment.KindSUPBoard), Label: "SUP-доска"},
		{Value: string(equipment.KindPaddle), Label: "Весло"},
		{Value: string(equipment.KindLifeJacket), Label: "Спасательный жилет"},
	}
}

func equipmentKindLabel(kind equipment.Kind) string {
	switch kind {
	case equipment.KindSUPBoard:
		return "SUP-доска"
	case equipment.KindPaddle:
		return "Весло"
	case equipment.KindLifeJacket:
		return "Спасательный жилет"
	default:
		return string(kind)
	}
}

func equipmentStatusLabel(status equipment.Status) string {
	switch status {
	case equipment.StatusAvailable:
		return "Доступен"
	case equipment.StatusIssued:
		return "Выдан"
	case equipment.StatusMaintenance:
		return "На обслуживании"
	case equipment.StatusRetired:
		return "Списан"
	default:
		return string(status)
	}
}
