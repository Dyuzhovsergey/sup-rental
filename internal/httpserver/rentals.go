package httpserver

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Dyuzhovsergey/sup-rental/internal/client"
	"github.com/Dyuzhovsergey/sup-rental/internal/equipment"
	"github.com/Dyuzhovsergey/sup-rental/internal/rental"
	"github.com/Dyuzhovsergey/sup-rental/internal/user"
)

const rentalDateTimeLayout = "2006-01-02T15:04"

type rentalService interface {
	AvailableModels(context.Context, rental.Interval) ([]rental.AvailableModel, error)
	CreateConfirmed(context.Context, user.User, int64, rental.Interval, []rental.ModelSelection) (rental.Rental, error)
	Issue(context.Context, user.User, int64) (rental.Rental, error)
	IssueMany(context.Context, user.User, []int64) ([]rental.Rental, error)
	Cancel(context.Context, user.User, int64) (rental.Rental, error)
	CancelMany(context.Context, user.User, []int64) ([]rental.Rental, error)
	Get(context.Context, int64) (rental.Rental, error)
	ListPage(context.Context, []rental.Status, int, int) (rental.Page, error)
}

type rentalWizardPageData struct {
	Authentication   *authenticationView
	Title            string
	Step             string
	StepNumber       int
	Client           client.Client
	Phone            string
	FullName         string
	PhoneError       string
	FullNameError    string
	Start            string
	StartError       string
	DurationDays     string
	DurationHours    string
	DurationMinutes  string
	DurationError    string
	DayOptions       []rentalDurationOption
	HourOptions      []rentalDurationOption
	MinuteOptions    []rentalDurationOption
	EndLabel         string
	Models           []rentalModelView
	ModelGroups      []rentalModelGroupView
	SelectedModels   []rentalSelectedModelView
	SelectionError   string
	Duration         string
	Period           string
	SlotCount        int
	SUPBoardCount    int
	PaddleCount      int
	LifeJacketCount  int
	ItemCount        int
	PlannedTotal     string
	PeriodBackURL    string
	EquipmentBackURL string
	ClientBackURL    string
	ReviewURL        string
	ChangeClientURL  string
	CSRFToken        string
}

type rentalModelView struct {
	ModelID           int64
	Kind              string
	KindValue         string
	ModelCode         string
	HourlyRate        string
	HourlyRateKopecks int64
	AvailableCount    int
	Quantity          string
}

type rentalModelGroupView struct {
	KindValue      string
	Kind           string
	Models         []rentalModelView
	AvailableLabel string
}

type rentalSelectedModelView struct {
	ModelID    int64
	Kind       string
	ModelCode  string
	HourlyRate string
	Quantity   int
	Subtotal   string
}

type rentalSelectionSummary struct {
	Models          []rentalSelectedModelView
	SUPBoardCount   int
	PaddleCount     int
	LifeJacketCount int
	ItemCount       int
	TotalKopecks    int64
}

type rentalDurationOption struct {
	Value    int
	Label    string
	Selected bool
}

type rentalsPageData struct {
	Authentication  *authenticationView
	Title           string
	Success         string
	Confirmed       rentalSectionView
	Active          rentalSectionView
	History         rentalSectionView
	PageSize        int
	PageSizeOptions []pageSizeOption
	CanCreate       bool
}

type rentalSectionView struct {
	ID          string
	Heading     string
	Description string
	Rentals     []rentalSummaryView
	TotalLabel  string
	Pagination  paginationView
	EmptyTitle  string
	EmptyText   string
	ShowActions bool
	CanManage   bool
	BulkActions bool
}

type rentalPageNumbers struct {
	Confirmed int
	Active    int
	History   int
}

type rentalSummaryView struct {
	ID           int64
	ClientName   string
	Period       string
	ItemCount    string
	Status       string
	PlannedTotal string
}

type rentalDetailPageData struct {
	Authentication *authenticationView
	Title          string
	RentalID       int64
	Client         client.Client
	Period         string
	Duration       string
	Status         string
	Items          []rentalItemView
	ItemCount      string
	PlannedTotal   string
	IssuedAt       string
	CanIssue       bool
}

type rentalItemView struct {
	InventoryNumber string
	Kind            string
	ModelCode       string
	HourlyRate      string
}

func showRentalClientStep(
	logger *slog.Logger,
	rentals rentalService,
	clients clientService,
	pageTemplates *template.Template,
	w http.ResponseWriter,
	r *http.Request,
) {
	customer, ok := optionalRentalClientFromRequest(logger, clients, w, r)
	if !ok {
		return
	}
	startValue := strings.TrimSpace(r.URL.Query().Get("start"))
	daysValue := strings.TrimSpace(r.URL.Query().Get("duration_days"))
	hoursValue := strings.TrimSpace(r.URL.Query().Get("duration_hours"))
	minutesValue := strings.TrimSpace(r.URL.Query().Get("duration_minutes"))
	interval, startError, durationError := parseRentalInterval(
		startValue, daysValue, hoursValue, minutesValue,
	)
	if startError != "" || durationError != "" {
		renderRentalPeriodStep(
			logger, pageTemplates, w, http.StatusUnprocessableEntity, r, customer,
			startValue, daysValue, hoursValue, minutesValue, startError, durationError,
		)
		return
	}

	selections, selected, parseOK := rentalSelections(
		r.URL.Query()["model_id"], r.URL.Query()["quantity"],
	)
	if !parseOK {
		renderRentalEquipmentError(
			logger, rentals, pageTemplates, w, r, customer, interval,
			startValue, daysValue, hoursValue, minutesValue, nil,
			"Проверьте выбранные модели и количество.", http.StatusUnprocessableEntity,
		)
		return
	}
	models, err := rentals.AvailableModels(r.Context(), interval)
	if err != nil {
		logger.Error("load rental equipment before client step", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	summary, err := buildRentalSelectionSummary(models, selections, interval)
	if err != nil {
		status, message := rentalSelectionHTTPError(err)
		if status == http.StatusInternalServerError {
			logger.Error("calculate rental selection before client step", slog.Any("error", err))
			http.Error(w, "Internal Server Error", status)
			return
		}
		renderRentalEquipmentError(
			logger, rentals, pageTemplates, w, r, customer, interval,
			startValue, daysValue, hoursValue, minutesValue, selected, message, status,
		)
		return
	}

	renderRentalClientSelectionStep(
		logger, pageTemplates, w, http.StatusOK, r, customer, interval,
		startValue, daysValue, hoursValue, minutesValue, selections, summary,
		"", "", "", "",
	)
}

func selectRentalClient(
	logger *slog.Logger,
	rentals rentalService,
	clients clientService,
	pageTemplates *template.Template,
	w http.ResponseWriter,
	r *http.Request,
) {
	startValue := strings.TrimSpace(r.PostForm.Get("start"))
	daysValue := strings.TrimSpace(r.PostForm.Get("duration_days"))
	hoursValue := strings.TrimSpace(r.PostForm.Get("duration_hours"))
	minutesValue := strings.TrimSpace(r.PostForm.Get("duration_minutes"))
	interval, startError, durationError := parseRentalInterval(
		startValue, daysValue, hoursValue, minutesValue,
	)
	if startError != "" || durationError != "" {
		renderRentalPeriodStep(
			logger, pageTemplates, w, http.StatusUnprocessableEntity, r, client.Client{},
			startValue, daysValue, hoursValue, minutesValue, startError, durationError,
		)
		return
	}
	selections, selected, parseOK := rentalSelections(r.PostForm["model_id"], r.PostForm["quantity"])
	if !parseOK {
		renderRentalEquipmentError(
			logger, rentals, pageTemplates, w, r, client.Client{}, interval,
			startValue, daysValue, hoursValue, minutesValue, nil,
			"Проверьте выбранные модели и количество.", http.StatusUnprocessableEntity,
		)
		return
	}
	models, err := rentals.AvailableModels(r.Context(), interval)
	if err != nil {
		logger.Error("load rental equipment before client selection", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	summary, err := buildRentalSelectionSummary(models, selections, interval)
	if err != nil {
		status, message := rentalSelectionHTTPError(err)
		if status == http.StatusInternalServerError {
			logger.Error("calculate rental selection before client selection", slog.Any("error", err))
			http.Error(w, "Internal Server Error", status)
			return
		}
		renderRentalEquipmentError(
			logger, rentals, pageTemplates, w, r, client.Client{}, interval,
			startValue, daysValue, hoursValue, minutesValue, selected, message, status,
		)
		return
	}

	phone := strings.TrimSpace(r.PostForm.Get("phone"))
	fullName := strings.TrimSpace(r.PostForm.Get("full_name"))

	found, err := clients.FindByPhone(r.Context(), phone)
	if err == nil {
		redirectToRentalReview(
			w, r, found.ID, startValue, daysValue, hoursValue, minutesValue, selections,
		)
		return
	}
	if errors.Is(err, client.ErrPhoneRequired) || errors.Is(err, client.ErrInvalidPhone) {
		renderRentalClientSelectionStep(
			logger, pageTemplates, w, http.StatusUnprocessableEntity, r, client.Client{}, interval,
			startValue, daysValue, hoursValue, minutesValue, selections, summary,
			clientPhoneInputLabel(phone), fullName, "Введите корректный номер телефона.", "",
		)
		return
	}
	if !errors.Is(err, client.ErrClientNotFound) {
		logger.Error("find rental client", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	created, err := clients.Create(r.Context(), currentUser(r), fullName, phone)
	if err == nil {
		redirectToRentalReview(
			w, r, created.ID, startValue, daysValue, hoursValue, minutesValue, selections,
		)
		return
	}
	phoneError := ""
	fullNameError := ""
	switch {
	case errors.Is(err, client.ErrFullNameRequired):
		fullNameError = "Клиент не найден. Укажите ФИО для создания карточки."
	case errors.Is(err, client.ErrFullNameTooLong):
		fullNameError = "ФИО должно содержать не более 200 символов."
	case errors.Is(err, client.ErrInvalidFullName):
		fullNameError = "ФИО содержит недопустимые символы."
	case errors.Is(err, client.ErrPhoneRequired), errors.Is(err, client.ErrInvalidPhone):
		phoneError = "Введите корректный номер телефона."
	case errors.Is(err, client.ErrPhoneExists):
		phoneError = "Клиент с таким телефоном уже существует. Повторите поиск."
	default:
		logger.Error("create rental client", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	renderRentalClientSelectionStep(
		logger, pageTemplates, w, http.StatusUnprocessableEntity, r, client.Client{}, interval,
		startValue, daysValue, hoursValue, minutesValue, selections, summary,
		clientPhoneInputLabel(phone), fullName, phoneError, fullNameError,
	)
}

func redirectToRentalReview(
	w http.ResponseWriter,
	r *http.Request,
	clientID int64,
	start, days, hours, minutes string,
	selections []rental.ModelSelection,
) {
	http.Redirect(
		w,
		r,
		rentalReviewURL(clientID, start, days, hours, minutes, selections),
		http.StatusSeeOther,
	)
}

func showRentalPeriodStep(
	logger *slog.Logger,
	clients clientService,
	pageTemplates *template.Template,
	w http.ResponseWriter,
	r *http.Request,
) {
	customer, ok := optionalRentalClientFromRequest(logger, clients, w, r)
	if !ok {
		return
	}
	startValue := strings.TrimSpace(r.URL.Query().Get("start"))
	daysValue := strings.TrimSpace(r.URL.Query().Get("duration_days"))
	hoursValue := strings.TrimSpace(r.URL.Query().Get("duration_hours"))
	minutesValue := strings.TrimSpace(r.URL.Query().Get("duration_minutes"))
	renderRentalPeriodStep(
		logger, pageTemplates, w, http.StatusOK, r, customer,
		startValue, daysValue, hoursValue, minutesValue, "", "",
	)
}

func showRentalEquipmentStep(
	logger *slog.Logger,
	rentals rentalService,
	clients clientService,
	pageTemplates *template.Template,
	w http.ResponseWriter,
	r *http.Request,
) {
	customer, ok := optionalRentalClientFromRequest(logger, clients, w, r)
	if !ok {
		return
	}
	startValue := strings.TrimSpace(r.URL.Query().Get("start"))
	daysValue := strings.TrimSpace(r.URL.Query().Get("duration_days"))
	hoursValue := strings.TrimSpace(r.URL.Query().Get("duration_hours"))
	minutesValue := strings.TrimSpace(r.URL.Query().Get("duration_minutes"))
	interval, startError, durationError := parseRentalInterval(
		startValue, daysValue, hoursValue, minutesValue,
	)
	if startError != "" || durationError != "" {
		renderRentalPeriodStep(
			logger, pageTemplates, w, http.StatusUnprocessableEntity, r, customer,
			startValue, daysValue, hoursValue, minutesValue, startError, durationError,
		)
		return
	}

	models, err := rentals.AvailableModels(r.Context(), interval)
	if err != nil {
		logger.Error("load rental equipment", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	selections, selected, parseOK := rentalSelections(
		r.URL.Query()["model_id"], r.URL.Query()["quantity"],
	)
	selectionError := ""
	status := http.StatusOK
	if !parseOK {
		selectionError = "Проверьте выбранные модели и количество."
		status = http.StatusUnprocessableEntity
	}
	summary := rentalSelectionSummary{}
	if parseOK {
		summary, err = buildRentalSelectionSummary(models, selections, interval)
		switch {
		case errors.Is(err, rental.ErrInsufficientEquipment):
			selectionError = "Доступное количество изменилось. Проверьте состав."
			status = http.StatusConflict
		case errors.Is(err, rental.ErrInvalidModelSelection):
			selectionError = "Выбрана неизвестная модель или некорректное количество."
			status = http.StatusUnprocessableEntity
		case err != nil:
			logger.Error("calculate rental equipment summary", slog.Any("error", err))
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
	}
	renderRentalWizard(logger, pageTemplates, w, status, r, rentalWizardPageData{
		Title: "Новая аренда — SUP Rental", Step: "equipment", StepNumber: 2,
		Client: customer, Start: startValue,
		DurationDays: daysValue, DurationHours: hoursValue, DurationMinutes: minutesValue,
		Models: rentalModelViews(models, selected), ModelGroups: rentalModelGroups(models, selected), SelectionError: selectionError,
		Duration: rentalDurationLabel(interval),
		Period:   rentalPeriodLabel(interval), EndLabel: rentalEndLabel(interval),
		SlotCount:     interval.SlotCount(),
		SUPBoardCount: summary.SUPBoardCount, PaddleCount: summary.PaddleCount,
		LifeJacketCount: summary.LifeJacketCount, ItemCount: summary.ItemCount,
		PlannedTotal:  rentalMoneyLabel(summary.TotalKopecks),
		PeriodBackURL: rentalPeriodURL(customer.ID, startValue, daysValue, hoursValue, minutesValue),
	})
}

func showRentalReviewStep(
	logger *slog.Logger,
	rentals rentalService,
	clients clientService,
	pageTemplates *template.Template,
	w http.ResponseWriter,
	r *http.Request,
) {
	customer, ok := rentalClientFromRequest(logger, clients, w, r)
	if !ok {
		return
	}
	startValue := strings.TrimSpace(r.URL.Query().Get("start"))
	daysValue := strings.TrimSpace(r.URL.Query().Get("duration_days"))
	hoursValue := strings.TrimSpace(r.URL.Query().Get("duration_hours"))
	minutesValue := strings.TrimSpace(r.URL.Query().Get("duration_minutes"))
	interval, startError, durationError := parseRentalInterval(
		startValue, daysValue, hoursValue, minutesValue,
	)
	if startError != "" || durationError != "" {
		renderRentalPeriodStep(
			logger, pageTemplates, w, http.StatusUnprocessableEntity, r, customer,
			startValue, daysValue, hoursValue, minutesValue, startError, durationError,
		)
		return
	}

	selections, selected, parseOK := rentalSelections(
		r.URL.Query()["model_id"], r.URL.Query()["quantity"],
	)
	if !parseOK {
		renderRentalEquipmentError(
			logger, rentals, pageTemplates, w, r, customer, interval,
			startValue, daysValue, hoursValue, minutesValue, nil,
			"Проверьте выбранные модели и количество.", http.StatusUnprocessableEntity,
		)
		return
	}
	models, err := rentals.AvailableModels(r.Context(), interval)
	if err != nil {
		logger.Error("load rental equipment for review", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	summary, err := buildRentalSelectionSummary(models, selections, interval)
	if err != nil {
		status := http.StatusUnprocessableEntity
		message := "Выбрана неизвестная модель или некорректное количество."
		if errors.Is(err, rental.ErrInsufficientEquipment) {
			status = http.StatusConflict
			message = "Доступное количество изменилось. Проверьте состав."
		} else if !errors.Is(err, rental.ErrInvalidModelSelection) {
			logger.Error("calculate rental review", slog.Any("error", err))
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		renderRentalEquipmentError(
			logger, rentals, pageTemplates, w, r, customer, interval,
			startValue, daysValue, hoursValue, minutesValue, selected, message, status,
		)
		return
	}

	renderRentalWizard(logger, pageTemplates, w, http.StatusOK, r, rentalWizardPageData{
		Title: "Новая аренда — SUP Rental", Step: "review", StepNumber: 4,
		Client: customer, Start: startValue,
		DurationDays: daysValue, DurationHours: hoursValue, DurationMinutes: minutesValue,
		SelectedModels: summary.Models, Duration: rentalDurationLabel(interval),
		Period: rentalPeriodLabel(interval), EndLabel: rentalEndLabel(interval),
		SUPBoardCount: summary.SUPBoardCount, PaddleCount: summary.PaddleCount,
		LifeJacketCount: summary.LifeJacketCount, ItemCount: summary.ItemCount,
		PlannedTotal: rentalMoneyLabel(summary.TotalKopecks),
		EquipmentBackURL: rentalEquipmentURL(
			customer.ID, startValue, daysValue, hoursValue, minutesValue, selections,
		),
		ClientBackURL: rentalClientURL(
			customer.ID, startValue, daysValue, hoursValue, minutesValue, selections,
		),
	})
}

func createConfirmedRental(
	logger *slog.Logger,
	rentals rentalService,
	clients clientService,
	pageTemplates *template.Template,
	w http.ResponseWriter,
	r *http.Request,
) {
	clientID, err := strconv.ParseInt(r.PostForm.Get("client_id"), 10, 64)
	if err != nil || clientID <= 0 {
		http.NotFound(w, r)
		return
	}
	customer, err := clients.Get(r.Context(), clientID)
	if errors.Is(err, client.ErrClientNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		logger.Error("load rental client before create", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	startValue := r.PostForm.Get("start")
	daysValue := r.PostForm.Get("duration_days")
	hoursValue := r.PostForm.Get("duration_hours")
	minutesValue := r.PostForm.Get("duration_minutes")
	interval, startError, durationError := parseRentalInterval(
		startValue, daysValue, hoursValue, minutesValue,
	)
	if startError != "" || durationError != "" {
		renderRentalPeriodStep(
			logger, pageTemplates, w, http.StatusUnprocessableEntity, r, customer,
			startValue, daysValue, hoursValue, minutesValue, startError, durationError,
		)
		return
	}

	selections, selected, parseOK := rentalSelections(r.PostForm["model_id"], r.PostForm["quantity"])
	if !parseOK {
		renderRentalEquipmentError(
			logger, rentals, pageTemplates, w, r, customer, interval,
			startValue, daysValue, hoursValue, minutesValue, nil,
			"Проверьте выбранные модели и количество.",
			http.StatusUnprocessableEntity,
		)
		return
	}

	created, err := rentals.CreateConfirmed(
		r.Context(), currentUser(r), clientID, interval, selections,
	)
	if err == nil {
		http.Redirect(
			w, r, "/rentals?created="+strconv.FormatInt(created.ID, 10),
			http.StatusSeeOther,
		)
		return
	}
	status := http.StatusUnprocessableEntity
	message := "Проверьте выбранные модели и количество."
	switch {
	case errors.Is(err, rental.ErrInsufficientEquipment):
		status = http.StatusConflict
		message = "Доступное количество изменилось. Проверьте состав и повторите создание."
	case errors.Is(err, rental.ErrRentalItemsRequired):
		message = "Выберите хотя бы одну единицу оборудования."
	case errors.Is(err, rental.ErrInvalidModelSelection):
		message = "Выбрана неизвестная модель или некорректное количество."
	case errors.Is(err, user.ErrAccessDenied):
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	case errors.Is(err, client.ErrClientNotFound):
		http.NotFound(w, r)
		return
	default:
		logger.Error("create confirmed rental", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	renderRentalEquipmentError(
		logger, rentals, pageTemplates, w, r, customer, interval,
		startValue, daysValue, hoursValue, minutesValue, selected, message, status,
	)
}

func renderRentalEquipmentError(
	logger *slog.Logger,
	rentals rentalService,
	pageTemplates *template.Template,
	w http.ResponseWriter,
	r *http.Request,
	customer client.Client,
	interval rental.Interval,
	startValue, daysValue, hoursValue, minutesValue string,
	selected map[int64]string,
	message string,
	status int,
) {
	models, err := rentals.AvailableModels(r.Context(), interval)
	if err != nil {
		logger.Error("reload rental equipment after error", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	selections := make([]rental.ModelSelection, 0, len(selected))
	for modelID, quantityValue := range selected {
		quantity, parseErr := strconv.Atoi(quantityValue)
		if parseErr == nil && quantity >= 0 {
			selections = append(selections, rental.ModelSelection{ModelID: modelID, Quantity: quantity})
		}
	}
	summary, summaryErr := buildRentalSelectionSummary(models, selections, interval)
	if summaryErr != nil {
		summary = rentalSelectionSummary{}
	}
	renderRentalWizard(logger, pageTemplates, w, status, r, rentalWizardPageData{
		Title: "Новая аренда — SUP Rental", Step: "equipment", StepNumber: 2,
		Client: customer, Start: startValue,
		DurationDays: daysValue, DurationHours: hoursValue, DurationMinutes: minutesValue,
		Models: rentalModelViews(models, selected), ModelGroups: rentalModelGroups(models, selected), SelectionError: message,
		Duration: rentalDurationLabel(interval), Period: rentalPeriodLabel(interval), EndLabel: rentalEndLabel(interval),
		SlotCount:     interval.SlotCount(),
		SUPBoardCount: summary.SUPBoardCount, PaddleCount: summary.PaddleCount,
		LifeJacketCount: summary.LifeJacketCount, ItemCount: summary.ItemCount,
		PlannedTotal:  rentalMoneyLabel(summary.TotalKopecks),
		PeriodBackURL: rentalPeriodURL(customer.ID, startValue, daysValue, hoursValue, minutesValue),
	})
}

func showRentalsPage(
	logger *slog.Logger,
	rentals rentalService,
	pageTemplates *template.Template,
	w http.ResponseWriter,
	r *http.Request,
) {
	pages, pageSize, ok := rentalPagination(r.URL.Query())
	if !ok {
		http.NotFound(w, r)
		return
	}
	confirmed, err := rentals.ListPage(
		r.Context(), []rental.Status{rental.StatusConfirmed}, pages.Confirmed, pageSize,
	)
	if !writeRentalListError(logger, w, r, err, "list confirmed rentals") {
		return
	}
	active, err := rentals.ListPage(
		r.Context(), []rental.Status{rental.StatusActive}, pages.Active, pageSize,
	)
	if !writeRentalListError(logger, w, r, err, "list active rentals") {
		return
	}
	history, err := rentals.ListPage(
		r.Context(), []rental.Status{rental.StatusCompleted, rental.StatusCancelled}, pages.History, pageSize,
	)
	if !writeRentalListError(logger, w, r, err, "list rental history") {
		return
	}
	if pages.Confirmed > pageCount(confirmed.Total, pageSize) ||
		pages.Active > pageCount(active.Total, pageSize) ||
		pages.History > pageCount(history.Total, pageSize) {
		http.NotFound(w, r)
		return
	}
	authentication := authenticationForPage(r)
	canManage := authentication != nil && authentication.IsOperator
	data := rentalsPageData{
		Authentication: authentication,
		Title:          "Аренды — SUP Rental",
		Confirmed: rentalSectionView{
			ID:      "confirmed-rentals-heading",
			Heading: "Подтверждённые аренды", Description: "Ожидают фактической выдачи оборудования",
			Rentals: rentalSummaryViews(confirmed.Rentals), TotalLabel: rentalCountLabel(confirmed.Total),
			Pagination: rentalSectionPagination("confirmed_page", pages.Confirmed, confirmed.Total, pages, pageSize),
			EmptyTitle: "Подтверждённых аренд нет", EmptyText: "Новые аренды появятся здесь до выдачи.",
			ShowActions: true, CanManage: canManage, BulkActions: canManage,
		},
		Active: rentalSectionView{
			ID:      "active-rentals-heading",
			Heading: "Активные аренды", Description: "Оборудование выдано клиентам",
			Rentals: rentalSummaryViews(active.Rentals), TotalLabel: rentalCountLabel(active.Total),
			Pagination: rentalSectionPagination("active_page", pages.Active, active.Total, pages, pageSize),
			EmptyTitle: "Активных аренд нет", EmptyText: "Выданные аренды появятся здесь.",
		},
		History: rentalSectionView{
			ID:      "rental-history-heading",
			Heading: "История аренд", Description: "Отменённые и завершённые аренды",
			Rentals: rentalSummaryViews(history.Rentals), TotalLabel: rentalCountLabel(history.Total),
			Pagination: rentalSectionPagination("history_page", pages.History, history.Total, pages, pageSize),
			EmptyTitle: "История пока пуста", EmptyText: "Отменённые и завершённые аренды появятся здесь.",
		},
		PageSize: pageSize, PageSizeOptions: rentalPageSizeOptions(pageSize),
		CanCreate: canManage,
	}
	if createdID, parseErr := positiveOptionalID(r.URL.Query().Get("created")); parseErr == nil && createdID > 0 {
		if _, getErr := rentals.Get(r.Context(), createdID); getErr == nil {
			data.Success = "Аренда №" + strconv.FormatInt(createdID, 10) + " создана и подтверждена."
		} else if !errors.Is(getErr, rental.ErrRentalNotFound) {
			logger.Error("load created rental notice", slog.Any("error", getErr))
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
	}
	if issuedID, parseErr := positiveOptionalID(r.URL.Query().Get("issued")); parseErr == nil && issuedID > 0 {
		issued, getErr := rentals.Get(r.Context(), issuedID)
		if getErr == nil && issued.Status == rental.StatusActive {
			data.Success = "Аренда №" + strconv.FormatInt(issuedID, 10) + " переведена в работу. Оборудование выдано."
		} else if getErr != nil && !errors.Is(getErr, rental.ErrRentalNotFound) {
			logger.Error("load issued rental notice", slog.Any("error", getErr))
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
	}
	if cancelledID, parseErr := positiveOptionalID(r.URL.Query().Get("cancelled")); parseErr == nil && cancelledID > 0 {
		cancelled, getErr := rentals.Get(r.Context(), cancelledID)
		if getErr == nil && cancelled.Status == rental.StatusCancelled {
			data.Success = "Аренда №" + strconv.FormatInt(cancelledID, 10) + " отменена."
		} else if getErr != nil && !errors.Is(getErr, rental.ErrRentalNotFound) {
			logger.Error("load cancelled rental notice", slog.Any("error", getErr))
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
	}
	if count, parseErr := positiveOptionalID(r.URL.Query().Get("bulk_issued")); parseErr == nil && count > 0 {
		data.Success = rentalBulkSuccessLabel(int(count), "Выдана", "Выданы", "Выдано")
	}
	if count, parseErr := positiveOptionalID(r.URL.Query().Get("bulk_cancelled")); parseErr == nil && count > 0 {
		data.Success = rentalBulkSuccessLabel(int(count), "Отменена", "Отменены", "Отменено")
	}
	renderPage(logger, pageTemplates, w, http.StatusOK, "rentals.html", data, "render rentals page", "write rentals response")
}

func rentalBulkSuccessLabel(count int, one, few, many string) string {
	word := many
	if count%10 == 1 && count%100 != 11 {
		word = one
	} else if count%10 >= 2 && count%10 <= 4 && (count%100 < 12 || count%100 > 14) {
		word = few
	}
	return fmt.Sprintf("%s %d %s.", word, count, russianRentalWord(count))
}

func writeRentalListError(logger *slog.Logger, w http.ResponseWriter, r *http.Request, err error, message string) bool {
	if err == nil {
		return true
	}
	if errors.Is(err, rental.ErrInvalidPage) {
		http.NotFound(w, r)
		return false
	}
	logger.Error(message, slog.Any("error", err))
	http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	return false
}

func showRentalDetailPage(
	logger *slog.Logger,
	rentals rentalService,
	clients clientService,
	pageTemplates *template.Template,
	w http.ResponseWriter,
	r *http.Request,
) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}
	value, err := rentals.Get(r.Context(), id)
	if errors.Is(err, rental.ErrRentalNotFound) || errors.Is(err, rental.ErrInvalidRentalID) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		logger.Error("get rental", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	customer, err := clients.Get(r.Context(), value.ClientID)
	if err != nil {
		logger.Error("get rental client", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	total, err := value.PlannedTotalKopecks()
	if err != nil {
		logger.Error("calculate rental detail total", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	authentication := authenticationForPage(r)
	data := rentalDetailPageData{
		Authentication: authentication, Title: fmt.Sprintf("Аренда №%d — SUP Rental", id),
		RentalID: id, Client: customer, Period: rentalPeriodLabel(value.Interval),
		Duration: rentalDurationLabel(value.Interval), Status: rentalStatusLabel(value.Status),
		Items: rentalItemViews(value.Items()), ItemCount: rentalItemCountLabel(value.ItemCount()),
		PlannedTotal: rentalMoneyLabel(total),
		CanIssue:     authentication != nil && authentication.IsOperator && value.Status == rental.StatusConfirmed,
	}
	if issuedAt, ok := value.IssuedAt(); ok {
		data.IssuedAt = rentalDateTimeLabel(issuedAt)
	}
	renderPage(logger, pageTemplates, w, http.StatusOK, "rental_detail.html", data,
		"render rental detail", "write rental detail response")
}

func renderRentalPeriodStep(
	logger *slog.Logger,
	pageTemplates *template.Template,
	w http.ResponseWriter,
	status int,
	r *http.Request,
	customer client.Client,
	startValue, daysValue, hoursValue, minutesValue, startError, durationError string,
) {
	data := rentalWizardPageData{
		Title: "Новая аренда — SUP Rental", Step: "period", StepNumber: 1,
		Client: customer, Start: startValue,
		DurationDays: daysValue, DurationHours: hoursValue, DurationMinutes: minutesValue,
		StartError: startError, DurationError: durationError,
		DayOptions:    rentalDurationOptions(rental.MaxDurationDays, daysValue),
		HourOptions:   rentalDurationOptions(23, hoursValue),
		MinuteOptions: rentalMinuteOptions(minutesValue),
	}
	if interval, periodStartError, periodDurationError := parseRentalInterval(
		startValue, daysValue, hoursValue, minutesValue,
	); periodStartError == "" && periodDurationError == "" {
		data.EndLabel = rentalEndLabel(interval)
	}
	renderRentalWizard(logger, pageTemplates, w, status, r, data)
}

func renderRentalClientSelectionStep(
	logger *slog.Logger,
	pageTemplates *template.Template,
	w http.ResponseWriter,
	status int,
	r *http.Request,
	customer client.Client,
	interval rental.Interval,
	startValue, daysValue, hoursValue, minutesValue string,
	selections []rental.ModelSelection,
	summary rentalSelectionSummary,
	phone, fullName, phoneError, fullNameError string,
) {
	if customer.ID > 0 {
		if phone == "" {
			phone = clientPhoneInputLabel(string(customer.Phone))
		}
		if fullName == "" {
			fullName = customer.FullName
		}
	}
	data := rentalWizardPageData{
		Title: "Новая аренда — SUP Rental", Step: "client", StepNumber: 3,
		Client: customer, Phone: phone, FullName: fullName,
		PhoneError: phoneError, FullNameError: fullNameError,
		Start: startValue, DurationDays: daysValue, DurationHours: hoursValue, DurationMinutes: minutesValue,
		SelectedModels: summary.Models, Duration: rentalDurationLabel(interval), Period: rentalPeriodLabel(interval),
		SUPBoardCount: summary.SUPBoardCount, PaddleCount: summary.PaddleCount,
		LifeJacketCount: summary.LifeJacketCount, ItemCount: summary.ItemCount,
		PlannedTotal: rentalMoneyLabel(summary.TotalKopecks),
		EquipmentBackURL: rentalEquipmentURL(
			customer.ID, startValue, daysValue, hoursValue, minutesValue, selections,
		),
		ChangeClientURL: rentalClientURL(
			0, startValue, daysValue, hoursValue, minutesValue, selections,
		),
	}
	if customer.ID > 0 {
		data.ReviewURL = rentalReviewURL(
			customer.ID, startValue, daysValue, hoursValue, minutesValue, selections,
		)
	}
	renderRentalWizard(logger, pageTemplates, w, status, r, data)
}

func rentalSelectionHTTPError(err error) (int, string) {
	switch {
	case errors.Is(err, rental.ErrInsufficientEquipment):
		return http.StatusConflict, "Доступное количество изменилось. Проверьте состав."
	case errors.Is(err, rental.ErrInvalidModelSelection):
		return http.StatusUnprocessableEntity, "Выбрана неизвестная модель или некорректное количество."
	default:
		return http.StatusInternalServerError, "Internal Server Error"
	}
}

func optionalRentalClientFromRequest(
	logger *slog.Logger,
	clients clientService,
	w http.ResponseWriter,
	r *http.Request,
) (client.Client, bool) {
	rawID := strings.TrimSpace(r.URL.Query().Get("client_id"))
	if rawID == "" {
		return client.Client{}, true
	}
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return client.Client{}, false
	}
	customer, err := clients.Get(r.Context(), id)
	if errors.Is(err, client.ErrClientNotFound) {
		http.NotFound(w, r)
		return client.Client{}, false
	}
	if err != nil {
		logger.Error("load optional rental client", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return client.Client{}, false
	}
	return customer, true
}

func rentalClientFromRequest(
	logger *slog.Logger,
	clients clientService,
	w http.ResponseWriter,
	r *http.Request,
) (client.Client, bool) {
	id, err := strconv.ParseInt(r.URL.Query().Get("client_id"), 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return client.Client{}, false
	}
	customer, err := clients.Get(r.Context(), id)
	if errors.Is(err, client.ErrClientNotFound) {
		http.NotFound(w, r)
		return client.Client{}, false
	}
	if err != nil {
		logger.Error("load rental client", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return client.Client{}, false
	}
	return customer, true
}

func renderRentalWizard(
	logger *slog.Logger,
	pageTemplates *template.Template,
	w http.ResponseWriter,
	status int,
	r *http.Request,
	data rentalWizardPageData,
) {
	data.Authentication = authenticationForPage(r)
	if data.Authentication != nil {
		data.CSRFToken = data.Authentication.CSRFToken
	}
	renderPage(logger, pageTemplates, w, status, "rental_new.html", data, "render rental wizard", "write rental wizard response")
}

func parseRentalInterval(
	startValue, daysValue, hoursValue, minutesValue string,
) (rental.Interval, string, string) {
	start, err := time.ParseInLocation(rentalDateTimeLayout, startValue, moscowTimeZone)
	if err != nil {
		return rental.Interval{}, "Укажите дату и время начала.", ""
	}
	days, daysOK := parseRentalDurationComponent(daysValue, rental.MaxDurationDays)
	hours, hoursOK := parseRentalDurationComponent(hoursValue, 23)
	minutes, minutesOK := parseRentalMinutes(minutesValue)
	if !daysOK || !hoursOK || !minutesOK {
		return rental.Interval{}, "", "Выберите корректную продолжительность аренды."
	}
	durationSlots := days*48 + hours*2 + minutes/30
	if durationSlots == 0 {
		return rental.Interval{}, "", "Укажите продолжительность не менее 30 минут."
	}
	end := start.Add(time.Duration(durationSlots) * rental.SlotDuration)
	interval, err := rental.NewInterval(start, end)
	if err == nil {
		return interval, "", ""
	}
	switch {
	case errors.Is(err, rental.ErrStartNotMinuteAligned):
		return rental.Interval{}, "Укажите время начала с точностью до минуты.", ""
	case errors.Is(err, rental.ErrEndNotAfterStart), errors.Is(err, rental.ErrIntervalTooShort),
		errors.Is(err, rental.ErrIntervalTooLong), errors.Is(err, rental.ErrDurationNotAligned):
		return rental.Interval{}, "", "Выберите корректную продолжительность аренды."
	default:
		return rental.Interval{}, "", "Проверьте период аренды."
	}
}

func parseRentalDurationComponent(value string, maximum int) (int, bool) {
	if value == "" {
		return 0, true
	}
	parsed, err := strconv.Atoi(value)
	return parsed, err == nil && parsed >= 0 && parsed <= maximum
}

func parseRentalMinutes(value string) (int, bool) {
	if value == "" || value == "0" {
		return 0, true
	}
	if value == "30" {
		return 30, true
	}
	return 0, false
}

func rentalDurationOptions(maximum int, selected string) []rentalDurationOption {
	selectedValue, selectedOK := parseRentalDurationComponent(selected, maximum)
	if !selectedOK {
		selectedValue = 0
	}
	options := make([]rentalDurationOption, 0, maximum+1)
	for value := 0; value <= maximum; value++ {
		options = append(options, rentalDurationOption{
			Value: value, Label: strconv.Itoa(value), Selected: value == selectedValue,
		})
	}
	return options
}

func rentalMinuteOptions(selected string) []rentalDurationOption {
	minutes, ok := parseRentalMinutes(selected)
	if !ok {
		minutes = 0
	}
	return []rentalDurationOption{
		{Value: 0, Label: "00", Selected: minutes == 0},
		{Value: 30, Label: "30", Selected: minutes == 30},
	}
}

func rentalSelections(modelIDs, quantities []string) ([]rental.ModelSelection, map[int64]string, bool) {
	if len(modelIDs) != len(quantities) {
		return nil, nil, false
	}
	selections := make([]rental.ModelSelection, 0, len(modelIDs))
	selected := make(map[int64]string, len(modelIDs))
	seen := make(map[int64]struct{}, len(modelIDs))
	for index, rawID := range modelIDs {
		modelID, err := strconv.ParseInt(rawID, 10, 64)
		if err != nil || modelID <= 0 {
			return nil, nil, false
		}
		if _, exists := seen[modelID]; exists {
			return nil, nil, false
		}
		quantity, err := strconv.Atoi(quantities[index])
		if err != nil || quantity < 0 {
			return nil, nil, false
		}
		seen[modelID] = struct{}{}
		selections = append(selections, rental.ModelSelection{ModelID: modelID, Quantity: quantity})
		selected[modelID] = quantities[index]
	}
	return selections, selected, true
}

func buildRentalSelectionSummary(
	models []rental.AvailableModel,
	selections []rental.ModelSelection,
	interval rental.Interval,
) (rentalSelectionSummary, error) {
	available := make(map[int64]rental.AvailableModel, len(models))
	for _, model := range models {
		available[model.ModelID] = model
	}

	summary := rentalSelectionSummary{}
	for _, selection := range selections {
		if selection.ModelID <= 0 || selection.Quantity < 0 {
			return rentalSelectionSummary{}, rental.ErrInvalidModelSelection
		}
		if selection.Quantity == 0 {
			continue
		}
		model, exists := available[selection.ModelID]
		if !exists || selection.Quantity > model.AvailableCount {
			return rentalSelectionSummary{}, rental.ErrInsufficientEquipment
		}

		subtotal, err := rentalSelectionSubtotal(
			model.HourlyRateKopecks, selection.Quantity, interval.SlotCount(),
		)
		if err != nil || summary.TotalKopecks > math.MaxInt64-subtotal {
			return rentalSelectionSummary{}, rental.ErrPriceOverflow
		}
		summary.TotalKopecks += subtotal
		summary.ItemCount += selection.Quantity
		switch model.Kind {
		case equipment.KindSUPBoard:
			summary.SUPBoardCount += selection.Quantity
		case equipment.KindPaddle:
			summary.PaddleCount += selection.Quantity
		case equipment.KindLifeJacket:
			summary.LifeJacketCount += selection.Quantity
		default:
			return rentalSelectionSummary{}, rental.ErrInvalidModelSelection
		}
		summary.Models = append(summary.Models, rentalSelectedModelView{
			ModelID: model.ModelID, Kind: equipmentKindLabel(model.Kind),
			ModelCode: model.ModelCode, HourlyRate: equipmentHourlyRateLabel(model.HourlyRateKopecks),
			Quantity: selection.Quantity, Subtotal: rentalMoneyLabel(subtotal),
		})
	}
	return summary, nil
}

func rentalSelectionSubtotal(hourlyRateKopecks int64, quantity, slots int) (int64, error) {
	if hourlyRateKopecks <= 0 || hourlyRateKopecks%2 != 0 || quantity <= 0 || slots <= 0 {
		return 0, rental.ErrPriceOverflow
	}
	halfHourlyRate := hourlyRateKopecks / 2
	if int64(quantity) > math.MaxInt64/halfHourlyRate {
		return 0, rental.ErrPriceOverflow
	}
	quantityTotal := int64(quantity) * halfHourlyRate
	if int64(slots) > math.MaxInt64/quantityTotal {
		return 0, rental.ErrPriceOverflow
	}
	return quantityTotal * int64(slots), nil
}

func rentalEquipmentURL(
	clientID int64,
	start, days, hours, minutes string,
	selections []rental.ModelSelection,
) string {
	return "/rentals/new/equipment?" + rentalWizardQuery(
		clientID, start, days, hours, minutes, selections,
	).Encode()
}

func rentalClientURL(
	clientID int64,
	start, days, hours, minutes string,
	selections []rental.ModelSelection,
) string {
	return "/rentals/new/client?" + rentalWizardQuery(
		clientID, start, days, hours, minutes, selections,
	).Encode()
}

func rentalReviewURL(
	clientID int64,
	start, days, hours, minutes string,
	selections []rental.ModelSelection,
) string {
	return "/rentals/new/review?" + rentalWizardQuery(
		clientID, start, days, hours, minutes, selections,
	).Encode()
}

func rentalPeriodURL(clientID int64, start, days, hours, minutes string) string {
	return "/rentals/new?" + rentalWizardQuery(
		clientID, start, days, hours, minutes, nil,
	).Encode()
}

func rentalWizardQuery(
	clientID int64,
	start, days, hours, minutes string,
	selections []rental.ModelSelection,
) url.Values {
	query := url.Values{
		"start":            {start},
		"duration_days":    {days},
		"duration_hours":   {hours},
		"duration_minutes": {minutes},
	}
	if clientID > 0 {
		query.Set("client_id", strconv.FormatInt(clientID, 10))
	}
	for _, selection := range selections {
		query.Add("model_id", strconv.FormatInt(selection.ModelID, 10))
		query.Add("quantity", strconv.Itoa(selection.Quantity))
	}
	return query
}

func rentalModelViews(models []rental.AvailableModel, selected map[int64]string) []rentalModelView {
	views := make([]rentalModelView, 0, len(models))
	for _, model := range models {
		quantity := "0"
		if selected != nil && selected[model.ModelID] != "" {
			quantity = selected[model.ModelID]
		}
		views = append(views, rentalModelView{
			ModelID: model.ModelID, Kind: equipmentKindLabel(model.Kind), KindValue: string(model.Kind), ModelCode: model.ModelCode,
			HourlyRate:        equipmentHourlyRateLabel(model.HourlyRateKopecks),
			HourlyRateKopecks: model.HourlyRateKopecks,
			AvailableCount:    model.AvailableCount, Quantity: quantity,
		})
	}
	return views
}

func rentalModelGroups(
	models []rental.AvailableModel,
	selected map[int64]string,
) []rentalModelGroupView {
	viewsByKind := make(map[equipment.Kind][]rentalModelView, 3)
	for _, view := range rentalModelViews(models, selected) {
		kind := equipment.Kind(view.KindValue)
		viewsByKind[kind] = append(viewsByKind[kind], view)
	}

	kinds := []equipment.Kind{
		equipment.KindSUPBoard,
		equipment.KindPaddle,
		equipment.KindLifeJacket,
	}
	groups := make([]rentalModelGroupView, 0, len(kinds))
	for _, kind := range kinds {
		kindViews := viewsByKind[kind]
		sort.SliceStable(kindViews, func(i, j int) bool {
			if kindViews[i].AvailableCount != kindViews[j].AvailableCount {
				return kindViews[i].AvailableCount > kindViews[j].AvailableCount
			}
			return kindViews[i].ModelCode < kindViews[j].ModelCode
		})
		availableCount := 0
		for _, view := range kindViews {
			availableCount += view.AvailableCount
		}
		groups = append(groups, rentalModelGroupView{
			KindValue:      string(kind),
			Kind:           equipmentKindLabel(kind),
			Models:         kindViews,
			AvailableLabel: fmt.Sprintf("Доступно: %d", availableCount),
		})
	}
	return groups
}

func rentalSummaryViews(values []rental.Summary) []rentalSummaryView {
	views := make([]rentalSummaryView, 0, len(values))
	for _, value := range values {
		views = append(views, rentalSummaryView{
			ID: value.ID, ClientName: value.ClientName,
			Period: rentalPeriodLabel(value.Interval), ItemCount: rentalItemCountLabel(value.ItemCount),
			Status: rentalStatusLabel(value.Status), PlannedTotal: rentalMoneyLabel(value.PlannedTotalKopecks),
		})
	}
	return views
}

func rentalItemViews(items []rental.Item) []rentalItemView {
	views := make([]rentalItemView, 0, len(items))
	for _, item := range items {
		views = append(views, rentalItemView{
			InventoryNumber: item.InventoryNumber, Kind: equipmentKindLabel(item.Kind),
			ModelCode: item.ModelCode, HourlyRate: equipmentHourlyRateLabel(item.HourlyRateKopecks),
		})
	}
	return views
}

func rentalPagination(query url.Values) (rentalPageNumbers, int, bool) {
	confirmedPage, confirmedOK := positiveQueryPage(query.Get("confirmed_page"))
	activePage, activeOK := positiveQueryPage(query.Get("active_page"))
	historyPage, historyOK := positiveQueryPage(query.Get("history_page"))
	if !confirmedOK || !activeOK || !historyOK {
		return rentalPageNumbers{}, 0, false
	}
	pageSize := rental.DefaultPageSize
	if raw := query.Get("page_size"); raw != "" {
		var err error
		pageSize, err = strconv.Atoi(raw)
		if err != nil {
			return rentalPageNumbers{}, 0, false
		}
	}
	for _, allowed := range rental.AllowedPageSizes() {
		if pageSize == allowed {
			return rentalPageNumbers{Confirmed: confirmedPage, Active: activePage, History: historyPage}, pageSize, true
		}
	}
	return rentalPageNumbers{}, 0, false
}

func rentalPageSizeOptions(selected int) []pageSizeOption {
	options := make([]pageSizeOption, 0, len(rental.AllowedPageSizes()))
	for _, size := range rental.AllowedPageSizes() {
		options = append(options, pageSizeOption{Value: size, Selected: size == selected})
	}
	return options
}

func rentalSectionPageURL(pageKey string, page int, pages rentalPageNumbers, pageSize int) string {
	query := url.Values{"page_size": {strconv.Itoa(pageSize)}}
	pageValues := map[string]int{
		"confirmed_page": pages.Confirmed,
		"active_page":    pages.Active,
		"history_page":   pages.History,
	}
	pageValues[pageKey] = page
	for key, value := range pageValues {
		if value > 1 {
			query.Set(key, strconv.Itoa(value))
		}
	}
	return "/rentals?" + query.Encode()
}

func rentalSectionPagination(pageKey string, page, total int, pages rentalPageNumbers, pageSize int) paginationView {
	totalPages := pageCount(total, pageSize)
	return paginationView{
		HasPrevious: page > 1,
		HasNext:     page < totalPages,
		PreviousURL: rentalSectionPageURL(pageKey, page-1, pages, pageSize),
		NextURL:     rentalSectionPageURL(pageKey, page+1, pages, pageSize),
		PageLabel:   pageLabel(page, totalPages),
	}
}

func rentalPeriodLabel(interval rental.Interval) string {
	start := interval.Start().In(moscowTimeZone)
	end := interval.End().In(moscowTimeZone)
	if start.YearDay() == end.YearDay() && start.Year() == end.Year() {
		return start.Format("02.01.2006 15:04") + " — " + end.Format("15:04")
	}
	return start.Format("02.01.2006 15:04") + " — " + end.Format("02.01.2006 15:04")
}

func rentalDateTimeLabel(value time.Time) string {
	return value.In(moscowTimeZone).Format("02.01.2006 15:04")
}

func rentalDurationLabel(interval rental.Interval) string {
	return rentalSlotsLabel(interval.SlotCount())
}

func rentalSlotsLabel(slots int) string {
	minutes := slots * int(rental.SlotDuration/time.Minute)
	hours := minutes / 60
	remaining := minutes % 60
	if hours == 0 {
		return fmt.Sprintf("%d мин", remaining)
	}
	if remaining == 0 {
		return fmt.Sprintf("%d ч", hours)
	}
	return fmt.Sprintf("%d ч %d мин", hours, remaining)
}

func rentalEndLabel(interval rental.Interval) string {
	return interval.End().In(moscowTimeZone).Format("02.01.2006 15:04")
}

func rentalStatusLabel(status rental.Status) string {
	switch status {
	case rental.StatusConfirmed:
		return "Подтверждена"
	case rental.StatusActive:
		return "Активна"
	case rental.StatusCompleted:
		return "Завершена"
	case rental.StatusCancelled:
		return "Отменена"
	default:
		return string(status)
	}
}

func rentalMoneyLabel(kopecks int64) string {
	return fmt.Sprintf("%d ₽", kopecks/100)
}

func rentalCountLabel(count int) string {
	return strconv.Itoa(count) + " " + russianRentalWord(count)
}

func russianRentalWord(count int) string {
	if count%10 == 1 && count%100 != 11 {
		return "аренда"
	}
	if count%10 >= 2 && count%10 <= 4 && (count%100 < 12 || count%100 > 14) {
		return "аренды"
	}
	return "аренд"
}

func rentalItemCountLabel(count int) string {
	return equipmentCountLabel(count)
}
