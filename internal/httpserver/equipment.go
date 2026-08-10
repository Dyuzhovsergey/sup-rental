package httpserver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/Dyuzhovsergey/sup-rental/internal/equipment"
)

type equipmentService interface {
	Create(ctx context.Context, input equipment.CreateInput) (equipment.Item, error)
	List(ctx context.Context) ([]equipment.Item, error)
	Get(ctx context.Context, id int64) (equipment.Item, error)
	Update(ctx context.Context, id int64, input equipment.UpdateInput) (equipment.Item, error)
	ChangeStatus(ctx context.Context, id int64, target equipment.Status) (equipment.Item, error)
	Delete(ctx context.Context, id int64) (equipment.Item, error)
}

type equipmentPageData struct {
	Title                string
	ActiveItems          []equipmentItemView
	RetiredItems         []equipmentItemView
	CountLabel           string
	ActiveCountLabel     string
	RetiredCountLabel    string
	Kinds                []equipmentKindOption
	Form                 equipmentFormData
	InventoryNumberError string
	KindError            string
	Success              string
}

type equipmentItemView struct {
	ID              int64
	InventoryNumber string
	Kind            string
	Status          string
	CanEdit         bool
}

type equipmentKindOption struct {
	Value string
	Label string
}

type equipmentStatusOption struct {
	Value    string
	Label    string
	Selected bool
}

type equipmentFormData struct {
	InventoryNumber string
	Kind            string
	Status          string
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
			"",
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
			"",
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
			"",
		)
	default:
		logger.Error(
			"create equipment",
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
	inventoryNumberError string,
	kindError string,
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

	activeItems, retiredItems := equipmentItemsByLifecycle(items)
	data := equipmentPageData{
		Title:                "Оборудование — SUP Rental",
		ActiveItems:          activeItems,
		RetiredItems:         retiredItems,
		CountLabel:           equipmentCountLabel(len(items)),
		ActiveCountLabel:     equipmentCountLabel(len(activeItems)),
		RetiredCountLabel:    equipmentCountLabel(len(retiredItems)),
		Kinds:                equipmentKindOptions(),
		Form:                 form,
		InventoryNumberError: inventoryNumberError,
		KindError:            kindError,
	}
	data.Success = equipmentSuccessMessage(r.URL.Query())

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

func equipmentItemsByLifecycle(
	items []equipment.Item,
) ([]equipmentItemView, []equipmentItemView) {
	active := make([]equipmentItemView, 0, len(items))
	retired := make([]equipmentItemView, 0)

	for _, item := range items {
		view := equipmentItemView{
			ID:              item.ID,
			InventoryNumber: item.InventoryNumber,
			Kind:            equipmentKindLabel(item.Kind),
			Status:          equipmentStatusLabel(item.Status),
			CanEdit:         item.Status.CanEditDetails(),
		}
		if item.Status == equipment.StatusRetired {
			retired = append(retired, view)
			continue
		}

		active = append(active, view)
	}

	return active, retired
}

func equipmentEditableStatusOptions(status equipment.Status) []equipmentStatusOption {
	return []equipmentStatusOption{
		{
			Value:    string(equipment.StatusAvailable),
			Label:    "Доступен",
			Selected: status == equipment.StatusAvailable,
		},
		{
			Value:    string(equipment.StatusMaintenance),
			Label:    "На обслуживании",
			Selected: status == equipment.StatusMaintenance,
		},
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

func equipmentCountLabel(count int) string {
	lastTwoDigits := count % 100
	if lastTwoDigits >= 11 && lastTwoDigits <= 14 {
		return fmt.Sprintf("%d позиций", count)
	}

	switch count % 10 {
	case 1:
		return fmt.Sprintf("%d позиция", count)
	case 2, 3, 4:
		return fmt.Sprintf("%d позиции", count)
	default:
		return fmt.Sprintf("%d позиций", count)
	}
}

const (
	equipmentNoticeUpdated = "updated"
	equipmentNoticeRetired = "retired"
	equipmentNoticeDeleted = "deleted"
)

func equipmentRedirectURL(notice string, item equipment.Item) string {
	query := url.Values{}
	query.Set("notice", notice)
	query.Set("kind", string(item.Kind))
	query.Set("inventory_number", item.InventoryNumber)

	return "/equipment?" + query.Encode()
}

func equipmentSuccessMessage(query url.Values) string {
	if len(query["notice"]) != 1 ||
		len(query["kind"]) != 1 ||
		len(query["inventory_number"]) != 1 {
		return ""
	}

	kind := equipment.Kind(query.Get("kind"))
	inventoryNumber := query.Get("inventory_number")
	if !kind.Valid() || !validNoticeInventoryNumber(inventoryNumber) {
		return ""
	}

	var action string
	switch query.Get("notice") {
	case equipmentNoticeUpdated:
		action = "обновлено"
	case equipmentNoticeRetired:
		action = "списано"
	case equipmentNoticeDeleted:
		action = "удалено"
	default:
		return ""
	}

	return fmt.Sprintf(
		"Оборудование %s %s %s.",
		equipmentKindLabel(kind),
		inventoryNumber,
		action,
	)
}

func validNoticeInventoryNumber(inventoryNumber string) bool {
	return inventoryNumber != "" && strings.TrimSpace(inventoryNumber) == inventoryNumber
}
