package httpserver

import (
	"bytes"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Dyuzhovsergey/sup-rental/internal/equipment"
)

type equipmentEditPageData struct {
	Authentication  *authenticationView
	Title           string
	ID              int64
	InventoryNumber string
	Kind            string
	ModelCode       string
	HourlyRate      string
	Statuses        []equipmentStatusOption
	Kinds           []equipmentKindOption
	Form            equipmentFormData
	ModelForm       equipmentModelFormData
	RateForm        string
	CanRetire       bool
	StatusError     string
	ModelKindError  string
	ModelCodeError  string
	ModelRateError  string
	RateError       string
	Success         string
}

type equipmentModelFormData struct {
	Kind             string
	ModelCode        string
	HourlyRateRubles string
}

type equipmentEditErrors struct {
	Status    string
	ModelKind string
	ModelCode string
	ModelRate string
	Rate      string
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
			Kind: string(item.Kind), ModelCode: item.ModelCode,
			HourlyRateRubles: strconv.FormatInt(item.HourlyRateKopecks/100, 10),
			Status:           string(item.Status),
		},
		equipmentModelFormData{
			Kind: string(item.Kind), ModelCode: item.ModelCode,
			HourlyRateRubles: strconv.FormatInt(item.HourlyRateKopecks/100, 10),
		},
		strconv.FormatInt(item.HourlyRateKopecks/100, 10),
		equipmentEditErrors{},
		item,
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
		Status: r.PostForm.Get("status"),
	}
	item, getErr := service.Get(r.Context(), id)
	if getErr != nil {
		handleEquipmentEditError(logger, w, r, "get equipment for update form", getErr)
		return
	}
	form.Kind = string(item.Kind)
	form.ModelCode = item.ModelCode
	form.HourlyRateRubles = strconv.FormatInt(item.HourlyRateKopecks/100, 10)

	updated, err := service.Update(r.Context(), currentUser(r), id, equipment.UpdateInput{
		Status: equipment.Status(form.Status),
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
	case errors.Is(err, equipment.ErrInvalidStatus):
		renderEquipmentEditPage(
			logger,
			pageTemplates,
			w,
			r,
			http.StatusUnprocessableEntity,
			id,
			form,
			equipmentModelFormData{Kind: string(item.Kind), ModelCode: item.ModelCode, HourlyRateRubles: form.HourlyRateRubles},
			form.HourlyRateRubles,
			equipmentEditErrors{Status: "Выберите допустимый статус оборудования."},
			item,
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
			equipmentModelFormData{Kind: string(item.Kind), ModelCode: item.ModelCode, HourlyRateRubles: form.HourlyRateRubles},
			form.HourlyRateRubles,
			equipmentEditErrors{Status: "Этот переход статуса недоступен в форме редактирования."},
			item,
		)
	default:
		handleEquipmentEditError(logger, w, r, "update equipment", err)
	}
}

func changeEquipmentModel(
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
	item, err := service.Get(r.Context(), id)
	if err != nil {
		handleEquipmentEditError(logger, w, r, "get equipment for model form", err)
		return
	}
	modelForm := normalizeEquipmentEditModelForm(equipmentModelFormData{
		Kind: r.PostForm.Get("kind"), ModelCode: r.PostForm.Get("model_code"),
		HourlyRateRubles: r.PostForm.Get("hourly_rate_rubles"),
	})
	rate, parseErr := strconv.ParseInt(modelForm.HourlyRateRubles, 10, 64)
	if parseErr != nil {
		renderEquipmentEditWithModelError(
			logger, pageTemplates, w, r, id, item, modelForm,
			http.StatusUnprocessableEntity,
			equipmentEditErrors{ModelRate: "Введите положительное целое число рублей."},
		)
		return
	}

	_, err = service.ChangeModel(r.Context(), currentUser(r), id, equipment.ModelChangeInput{
		Kind: equipment.Kind(modelForm.Kind), ModelCode: modelForm.ModelCode,
		HourlyRateRubles: rate,
	})
	if err == nil {
		http.Redirect(w, r, equipmentEditRedirectURL(id, "model_changed", 0), http.StatusSeeOther)
		return
	}

	formErrors := equipmentEditErrors{}
	statusCode := http.StatusUnprocessableEntity
	switch {
	case errors.Is(err, equipment.ErrInvalidKind):
		formErrors.ModelKind = "Выберите тип оборудования."
	case errors.Is(err, equipment.ErrModelCodeRequired):
		formErrors.ModelCode = "Введите код модели латинскими буквами."
	case errors.Is(err, equipment.ErrInvalidModelCode):
		formErrors.ModelCode = "Используйте только латинские буквы, цифры, пробелы, дефисы или _."
	case errors.Is(err, equipment.ErrInvalidHourlyRate):
		formErrors.ModelRate = "Введите положительное целое число рублей."
	case errors.Is(err, equipment.ErrModelRateConflict):
		statusCode = http.StatusConflict
		formErrors.ModelRate = "У существующей модели другой тариф. Укажите её текущий тариф или измените его отдельно."
	case errors.Is(err, equipment.ErrEquipmentModelUnchanged):
		formErrors.ModelCode = "Выберите другую модель оборудования."
	case errors.Is(err, equipment.ErrEquipmentNotFound):
		http.NotFound(w, r)
		return
	case errors.Is(err, equipment.ErrEquipmentUpdateNotAllowed):
		http.Error(w, "Редактирование оборудования в текущем состоянии недоступно.", http.StatusConflict)
		return
	default:
		handleEquipmentEditError(logger, w, r, "change equipment model", err)
		return
	}
	renderEquipmentEditWithModelError(logger, pageTemplates, w, r, id, item, modelForm, statusCode, formErrors)
}

func changeEquipmentModelRate(
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
	item, err := service.Get(r.Context(), id)
	if err != nil {
		handleEquipmentEditError(logger, w, r, "get equipment for rate form", err)
		return
	}
	rateForm := strings.TrimSpace(r.PostForm.Get("hourly_rate_rubles"))
	rate, parseErr := strconv.ParseInt(rateForm, 10, 64)
	if parseErr != nil {
		renderEquipmentEditWithRateError(logger, pageTemplates, w, r, id, item, rateForm,
			http.StatusUnprocessableEntity, "Введите положительное целое число рублей.")
		return
	}

	changed, err := service.ChangeModelRate(r.Context(), currentUser(r), id, rate)
	if err == nil {
		http.Redirect(w, r, equipmentEditRedirectURL(id, "rate_changed", changed.AffectedItems), http.StatusSeeOther)
		return
	}
	switch {
	case errors.Is(err, equipment.ErrInvalidHourlyRate):
		renderEquipmentEditWithRateError(logger, pageTemplates, w, r, id, item, rateForm,
			http.StatusUnprocessableEntity, "Введите положительное целое число рублей.")
	case errors.Is(err, equipment.ErrModelRateUnchanged):
		renderEquipmentEditWithRateError(logger, pageTemplates, w, r, id, item, rateForm,
			http.StatusUnprocessableEntity, "Укажите новый тариф, отличный от текущего.")
	case errors.Is(err, equipment.ErrEquipmentNotFound):
		http.NotFound(w, r)
	case errors.Is(err, equipment.ErrEquipmentUpdateNotAllowed):
		http.Error(w, "Редактирование оборудования в текущем состоянии недоступно.", http.StatusConflict)
	default:
		handleEquipmentEditError(logger, w, r, "change equipment model rate", err)
	}
}

func renderEquipmentEditWithModelError(
	logger *slog.Logger, templates *template.Template, w http.ResponseWriter, r *http.Request,
	id int64, item equipment.Item, modelForm equipmentModelFormData, statusCode int, formErrors equipmentEditErrors,
) {
	renderEquipmentEditPage(logger, templates, w, r, statusCode, id,
		equipmentFormData{Status: string(item.Status)}, modelForm,
		strconv.FormatInt(item.HourlyRateKopecks/100, 10), formErrors, item)
}

func renderEquipmentEditWithRateError(
	logger *slog.Logger, templates *template.Template, w http.ResponseWriter, r *http.Request,
	id int64, item equipment.Item, rateForm string, statusCode int, message string,
) {
	renderEquipmentEditPage(logger, templates, w, r, statusCode, id,
		equipmentFormData{Status: string(item.Status)},
		equipmentModelFormData{Kind: string(item.Kind), ModelCode: item.ModelCode, HourlyRateRubles: strconv.FormatInt(item.HourlyRateKopecks/100, 10)},
		rateForm, equipmentEditErrors{Rate: message}, item)
}

func equipmentEditRedirectURL(id int64, notice string, affectedItems int) string {
	query := url.Values{"notice": {notice}}
	if affectedItems > 0 {
		query.Set("affected", strconv.Itoa(affectedItems))
	}
	return "/equipment/" + strconv.FormatInt(id, 10) + "/edit?" + query.Encode()
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
	modelForm equipmentModelFormData,
	rateForm string,
	formErrors equipmentEditErrors,
	item equipment.Item,
) {
	data := equipmentEditPageData{
		Authentication: authenticationForPage(r),
		Title:          "Редактирование оборудования — SUP Rental", ID: id,
		InventoryNumber: item.InventoryNumber, Kind: equipmentKindLabel(item.Kind),
		ModelCode: item.ModelCode, HourlyRate: equipmentHourlyRateLabel(item.HourlyRateKopecks),
		Statuses: equipmentEditableStatusOptions(equipment.Status(form.Status)), Form: form,
		Kinds: equipmentKindOptions(), ModelForm: modelForm, RateForm: rateForm,
		CanRetire:   item.Status.CanTransitionTo(equipment.StatusRetired),
		StatusError: formErrors.Status, ModelKindError: formErrors.ModelKind,
		ModelCodeError: formErrors.ModelCode, ModelRateError: formErrors.ModelRate,
		RateError: formErrors.Rate, Success: equipmentEditSuccessMessage(r.URL.Query(), item),
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

func equipmentEditSuccessMessage(query url.Values, item equipment.Item) string {
	if len(query["notice"]) != 1 {
		return ""
	}
	switch query.Get("notice") {
	case "model_changed":
		if len(query) != 1 {
			return ""
		}
		return "Модель оборудования изменена. Новый инвентарный номер: " + item.InventoryNumber + "."
	case "rate_changed":
		if len(query) != 2 || len(query["affected"]) != 1 {
			return ""
		}
		affected, err := strconv.Atoi(query.Get("affected"))
		if err != nil || affected <= 0 {
			return ""
		}
		return "Тариф модели " + item.ModelCode + " обновлён. Затронуто: " + equipmentUnitsLabel(affected) + "."
	default:
		return ""
	}
}

func normalizeEquipmentEditModelForm(form equipmentModelFormData) equipmentModelFormData {
	form.Kind = strings.TrimSpace(form.Kind)
	form.ModelCode = strings.TrimSpace(form.ModelCode)
	form.HourlyRateRubles = strings.TrimSpace(form.HourlyRateRubles)
	return form
}
