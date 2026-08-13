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
	"strconv"
	"strings"

	"github.com/Dyuzhovsergey/sup-rental/internal/equipment"
	"github.com/Dyuzhovsergey/sup-rental/internal/user"
)

type equipmentService interface {
	CreateBatch(ctx context.Context, actor user.User, input equipment.BatchCreateInput) (equipment.Batch, error)
	List(ctx context.Context) ([]equipment.Item, error)
	ListPage(ctx context.Context, input equipment.ListPageInput) (equipment.ListPage, error)
	Get(ctx context.Context, id int64) (equipment.Item, error)
	Update(ctx context.Context, actor user.User, id int64, input equipment.UpdateInput) (equipment.Item, error)
	ChangeModel(ctx context.Context, actor user.User, id int64, input equipment.ModelChangeInput) (equipment.Item, error)
	ChangeModelRate(ctx context.Context, actor user.User, id int64, hourlyRateRubles int64) (equipment.ModelRateChange, error)
	ChangeStatus(ctx context.Context, actor user.User, id int64, target equipment.Status) (equipment.Item, error)
	Delete(ctx context.Context, actor user.User, id int64) (equipment.Item, error)
}

type equipmentPageData struct {
	Authentication     *authenticationView
	CanManageEquipment bool
	Title              string
	ActiveItems        []equipmentItemView
	RetiredItems       []equipmentItemView
	CountLabel         string
	ActiveCountLabel   string
	RetiredCountLabel  string
	Kinds              []equipmentKindOption
	Form               equipmentFormData
	KindError          string
	ModelCodeError     string
	HourlyRateError    string
	QuantityError      string
	Success            string
	PageSize           int
	PageSizeOptions    []pageSizeOption
	ActivePagination   paginationView
	RetiredPagination  paginationView
	HasRetiredItems    bool
}

type pageSizeOption struct {
	Value    int
	Selected bool
}

type paginationView struct {
	HasPrevious bool
	HasNext     bool
	PreviousURL string
	NextURL     string
	PageLabel   string
}

type equipmentItemView struct {
	ID              int64
	InventoryNumber string
	Kind            string
	ModelCode       string
	HourlyRate      string
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
	Kind             string
	ModelCode        string
	HourlyRateRubles string
	Quantity         string
	Status           string
}

type equipmentFormErrors struct {
	Kind       string
	ModelCode  string
	HourlyRate string
	Quantity   string
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
			equipmentFormErrors{},
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
		Kind:             r.PostForm.Get("kind"),
		ModelCode:        r.PostForm.Get("model_code"),
		HourlyRateRubles: r.PostForm.Get("hourly_rate_rubles"),
		Quantity:         r.PostForm.Get("quantity"),
	}
	hourlyRateRubles, rateErr := strconv.ParseInt(form.HourlyRateRubles, 10, 64)
	quantity, quantityErr := strconv.Atoi(form.Quantity)
	formErrors := equipmentFormErrors{}
	if rateErr != nil {
		formErrors.HourlyRate = "Введите положительное целое число рублей."
	}
	if quantityErr != nil {
		formErrors.Quantity = "Введите целое количество от 1 до 100."
	}
	if rateErr != nil || quantityErr != nil {
		renderEquipmentPage(logger, service, pageTemplates, w, r,
			http.StatusUnprocessableEntity, form, formErrors)
		return
	}

	batch, err := service.CreateBatch(r.Context(), currentUser(r), equipment.BatchCreateInput{
		Kind: equipment.Kind(form.Kind), ModelCode: form.ModelCode,
		HourlyRateRubles: hourlyRateRubles, Quantity: quantity,
	})
	if err == nil {
		http.Redirect(w, r, equipmentBatchRedirectURL(batch), http.StatusSeeOther)
		return
	}

	switch {
	case errors.Is(err, equipment.ErrInvalidKind):
		formErrors.Kind = "Выберите тип оборудования."
		renderEquipmentPage(logger, service, pageTemplates, w, r,
			http.StatusUnprocessableEntity, form, formErrors)
	case errors.Is(err, equipment.ErrModelCodeRequired):
		formErrors.ModelCode = "Введите код модели латинскими буквами."
		renderEquipmentPage(logger, service, pageTemplates, w, r,
			http.StatusUnprocessableEntity, form, formErrors)
	case errors.Is(err, equipment.ErrInvalidModelCode):
		formErrors.ModelCode = "Используйте только латинские буквы, цифры, пробелы, дефисы или _."
		renderEquipmentPage(logger, service, pageTemplates, w, r,
			http.StatusUnprocessableEntity, form, formErrors)
	case errors.Is(err, equipment.ErrInvalidHourlyRate):
		formErrors.HourlyRate = "Введите положительное целое число рублей."
		renderEquipmentPage(logger, service, pageTemplates, w, r,
			http.StatusUnprocessableEntity, form, formErrors)
	case errors.Is(err, equipment.ErrInvalidBatchQuantity):
		formErrors.Quantity = "Количество одной партии — от 1 до 100."
		renderEquipmentPage(logger, service, pageTemplates, w, r,
			http.StatusUnprocessableEntity, form, formErrors)
	case errors.Is(err, equipment.ErrModelRateConflict):
		formErrors.HourlyRate = "Эта модель уже существует с другим часовым тарифом."
		renderEquipmentPage(logger, service, pageTemplates, w, r,
			http.StatusConflict, form, formErrors)
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
	formErrors equipmentFormErrors,
) {
	activePageNumber, retiredPageNumber, pageSize, err := equipmentPagination(r.URL.Query())
	if err != nil {
		http.NotFound(w, r)
		return
	}

	activePage, err := service.ListPage(r.Context(), equipment.ListPageInput{
		Scope: equipment.ListScopeActive, Page: activePageNumber, PageSize: pageSize,
	})
	if err != nil {
		logger.Error(
			"list equipment",
			slog.Any("error", err),
		)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	retiredPage, err := service.ListPage(r.Context(), equipment.ListPageInput{
		Scope: equipment.ListScopeRetired, Page: retiredPageNumber, PageSize: pageSize,
	})
	if err != nil {
		logger.Error("list retired equipment", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	if pageOutOfRange(activePage) || pageOutOfRange(retiredPage) {
		http.NotFound(w, r)
		return
	}

	authentication := authenticationForPage(r)
	data := equipmentPageData{
		Authentication:     authentication,
		CanManageEquipment: authentication != nil && authentication.CanManageEquipment,
		Title:              "Оборудование — SUP Rental",
		ActiveItems:        equipmentItemViews(activePage.Items),
		RetiredItems:       equipmentItemViews(retiredPage.Items),
		CountLabel:         equipmentCountLabel(activePage.Total + retiredPage.Total),
		ActiveCountLabel:   equipmentCountLabel(activePage.Total),
		RetiredCountLabel:  equipmentCountLabel(retiredPage.Total),
		Kinds:              equipmentKindOptions(),
		Form:               form,
		KindError:          formErrors.Kind,
		ModelCodeError:     formErrors.ModelCode,
		HourlyRateError:    formErrors.HourlyRate,
		QuantityError:      formErrors.Quantity,
		PageSize:           pageSize,
		PageSizeOptions:    equipmentPageSizeOptions(pageSize),
		ActivePagination:   equipmentPaginationView(activePage, retiredPageNumber),
		RetiredPagination:  equipmentPaginationView(retiredPage, activePageNumber),
		HasRetiredItems:    retiredPage.Total > 0,
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

func equipmentItemViews(items []equipment.Item) []equipmentItemView {
	views := make([]equipmentItemView, 0, len(items))
	for _, item := range items {
		views = append(views, equipmentItemView{
			ID: item.ID, InventoryNumber: item.InventoryNumber,
			Kind: equipmentKindLabel(item.Kind), ModelCode: item.ModelCode,
			HourlyRate: equipmentHourlyRateLabel(item.HourlyRateKopecks),
			Status:     equipmentStatusLabel(item.Status),
			CanEdit:    item.Status.CanEditDetails(),
		})
	}
	return views
}

func equipmentPagination(query url.Values) (int, int, int, error) {
	activePage, err := positivePage(query.Get("active_page"))
	if err != nil {
		return 0, 0, 0, err
	}
	retiredPage, err := positivePage(query.Get("retired_page"))
	if err != nil {
		return 0, 0, 0, err
	}
	pageSize := 5
	if value := query.Get("page_size"); value != "" {
		pageSize, err = strconv.Atoi(value)
		if err != nil {
			return 0, 0, 0, err
		}
	}
	valid := false
	for _, size := range equipment.AllowedPageSizes() {
		if pageSize == size {
			valid = true
		}
	}
	if !valid {
		return 0, 0, 0, equipment.ErrInvalidListPage
	}
	return activePage, retiredPage, pageSize, nil
}

func equipmentPageSizeOptions(selected int) []pageSizeOption {
	allowedSizes := equipment.AllowedPageSizes()
	options := make([]pageSizeOption, 0, len(allowedSizes))
	for _, size := range allowedSizes {
		options = append(options, pageSizeOption{Value: size, Selected: size == selected})
	}
	return options
}

func pageOutOfRange(page equipment.ListPage) bool {
	return page.Total > 0 && len(page.Items) == 0
}

func equipmentPaginationView(page equipment.ListPage, otherPage int) paginationView {
	totalPages := pageCount(page.Total, page.PageSize)
	view := paginationView{PageLabel: pageLabel(page.Page, totalPages)}
	view.HasPrevious = page.Page > 1
	view.HasNext = page.Page < totalPages
	view.PreviousURL = equipmentPageURL(page, page.Page-1, otherPage)
	view.NextURL = equipmentPageURL(page, page.Page+1, otherPage)
	return view
}

func equipmentPageURL(page equipment.ListPage, targetPage, otherPage int) string {
	query := url.Values{"page_size": {strconv.Itoa(page.PageSize)}}
	if page.Scope == equipment.ListScopeRetired {
		query.Set("retired_page", strconv.Itoa(targetPage))
		query.Set("active_page", strconv.Itoa(otherPage))
	} else {
		query.Set("active_page", strconv.Itoa(targetPage))
		query.Set("retired_page", strconv.Itoa(otherPage))
	}
	return "/equipment?" + query.Encode()
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
	equipmentNoticeBatchCreated = "batch_created"
	equipmentNoticeUpdated      = "updated"
	equipmentNoticeRetired      = "retired"
	equipmentNoticeDeleted      = "deleted"
)

func equipmentBatchRedirectURL(batch equipment.Batch) string {
	query := url.Values{}
	query.Set("notice", equipmentNoticeBatchCreated)
	query.Set("count", strconv.Itoa(len(batch.Items)))
	query.Set("first", batch.FirstInventoryNumber)
	query.Set("last", batch.LastInventoryNumber)
	return "/equipment?" + query.Encode()
}

func equipmentRedirectURL(notice string, item equipment.Item) string {
	query := url.Values{}
	query.Set("notice", notice)
	query.Set("kind", string(item.Kind))
	query.Set("inventory_number", item.InventoryNumber)

	return "/equipment?" + query.Encode()
}

func equipmentSuccessMessage(query url.Values) string {
	if query.Get("notice") == equipmentNoticeBatchCreated {
		count, err := strconv.Atoi(query.Get("count"))
		first, last := query.Get("first"), query.Get("last")
		if err != nil || count < 1 || count > 100 ||
			!validNoticeInventoryNumber(first) || !validNoticeInventoryNumber(last) {
			return ""
		}
		if count == 1 {
			return fmt.Sprintf("Добавлено оборудование %s.", first)
		}
		return fmt.Sprintf("Добавлено %s оборудования: %s — %s.", equipmentUnitsLabel(count), first, last)
	}
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

func equipmentUnitsLabel(count int) string {
	lastTwoDigits := count % 100
	if lastTwoDigits >= 11 && lastTwoDigits <= 14 {
		return fmt.Sprintf("%d единиц", count)
	}
	switch count % 10 {
	case 1:
		return fmt.Sprintf("%d единица", count)
	case 2, 3, 4:
		return fmt.Sprintf("%d единицы", count)
	default:
		return fmt.Sprintf("%d единиц", count)
	}
}

func equipmentHourlyRateLabel(kopecks int64) string {
	return fmt.Sprintf("%d ₽/час", kopecks/100)
}

func validNoticeInventoryNumber(inventoryNumber string) bool {
	return inventoryNumber != "" && strings.TrimSpace(inventoryNumber) == inventoryNumber
}
