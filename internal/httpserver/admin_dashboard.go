package httpserver

import (
	"context"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/Dyuzhovsergey/sup-rental/internal/dashboard"
)

type adminDashboardService interface {
	Snapshot(ctx context.Context, period dashboard.FinancialPeriod) (dashboard.Snapshot, error)
	PaymentOperations(ctx context.Context, period dashboard.FinancialPeriod, page, pageSize int) (dashboard.PaymentOperationsPage, error)
}

type adminDashboardPageData struct {
	Authentication *authenticationView
	Title          string
	Equipment      []adminMetricView
	Rentals        []adminMetricView
	Finance        []adminFinanceMetricView
	FinanceFilter  adminFinancePeriodView
	FinanceHeading string
	PaymentsURL    string
	PeriodValid    bool
}

type adminFinanceMetricView struct {
	Label       string
	Value       string
	Description string
	Tone        string
}

type adminMetricView struct {
	Label string
	Value int64
	Tone  string
}

func showAdminDashboard(
	logger *slog.Logger,
	service adminDashboardService,
	pageTemplates *template.Template,
	w http.ResponseWriter,
	r *http.Request,
) {
	selection := parseAdminFinancePeriod(r.URL.Query(), time.Now())
	data := adminDashboardPageData{
		Authentication: authenticationForPage(r),
		Title:          "Панель администратора — SUP Rental",
		FinanceFilter:  selection.view("/admin", 0),
		PeriodValid:    selection.valid(),
	}
	if !selection.valid() {
		renderPage(
			logger, pageTemplates, w, http.StatusUnprocessableEntity, "admin_dashboard.html", data,
			"render admin dashboard period error", "write admin dashboard period error response",
		)
		return
	}

	snapshot, err := service.Snapshot(r.Context(), selection.Period)
	if err != nil {
		logger.Error("load admin dashboard", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	data.Equipment = []adminMetricView{

		{Label: "Всего единиц", Value: snapshot.EquipmentTotal, Tone: "primary"},
		{Label: "Доступно", Value: snapshot.EquipmentAvailable, Tone: "success"},
		{Label: "На обслуживании", Value: snapshot.EquipmentMaintenance, Tone: "warning"},
		{Label: "Выдано", Value: snapshot.EquipmentIssued, Tone: "primary"},
		{Label: "Списано", Value: snapshot.EquipmentRetired, Tone: "neutral"},
	}
	data.Rentals = []adminMetricView{
		{Label: "Активные", Value: snapshot.RentalsActive, Tone: "success"},
		{Label: "Просроченные", Value: snapshot.RentalsOverdue, Tone: "danger"},
		{Label: "Начинаются сегодня", Value: snapshot.RentalsStartingToday, Tone: "primary"},
		{Label: "Завершаются сегодня", Value: snapshot.RentalsEndingToday, Tone: "warning"},
	}
	data.Finance = []adminFinanceMetricView{
		{
			Label: "Основные оплаты", Value: adminMoneyLabel(snapshot.PaymentsBaseKopecks),
			Description: "Получено при создании аренд", Tone: "primary",
		},
		{
			Label: "Доплаты за просрочку", Value: adminMoneyLabel(snapshot.PaymentsOverdueKopecks),
			Description: "Получено при завершении", Tone: "warning",
		},
		{
			Label: "Возвраты", Value: adminMoneyLabel(snapshot.PaymentsRefundKopecks),
			Description: "Возвращено при отмене", Tone: "neutral",
		},
		{
			Label: financeTotalLabel(selection), Value: adminMoneyLabel(snapshot.PaymentsNetKopecks),
			Description: "Оплаты + доплаты − возвраты", Tone: adminNetTone(snapshot.PaymentsNetKopecks),
		},
	}
	data.FinanceHeading = financeHeading(selection)
	data.PaymentsURL = financeNavigationURL("/admin/payments", selection)
	renderPage(
		logger, pageTemplates, w, http.StatusOK, "admin_dashboard.html", data,
		"render admin dashboard", "write admin dashboard response",
	)
}

func financeHeading(selection adminFinancePeriodSelection) string {
	if selection.Key == financePeriodToday {
		return "Финансы сегодня"
	}
	return "Финансы " + selection.Phrase
}

func financeTotalLabel(selection adminFinancePeriodSelection) string {
	switch selection.Key {
	case financePeriodToday:
		return "Итого за сегодня"
	case financePeriodYesterday:
		return "Итого за вчера"
	default:
		return "Итого за период"
	}
}

func adminNetTone(value int64) string {
	if value < 0 {
		return "danger"
	}
	return "success"
}

func adminMoneyLabel(kopecks int64) string {
	negative := kopecks < 0
	var absolute uint64
	if negative {
		absolute = uint64(-(kopecks + 1)) + 1
	} else {
		absolute = uint64(kopecks)
	}

	rubles := strconv.FormatUint(absolute/100, 10)
	for index := len(rubles) - 3; index > 0; index -= 3 {
		rubles = rubles[:index] + " " + rubles[index:]
	}
	if cents := absolute % 100; cents != 0 {
		rubles += "," + strconv.FormatUint(cents/10, 10) + strconv.FormatUint(cents%10, 10)
	}
	if negative {
		rubles = "−" + rubles
	}
	return rubles + " ₽"
}
