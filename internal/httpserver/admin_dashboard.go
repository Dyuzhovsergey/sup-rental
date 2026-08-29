package httpserver

import (
	"context"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/Dyuzhovsergey/sup-rental/internal/dashboard"
)

type adminDashboardService interface {
	Snapshot(ctx context.Context) (dashboard.Snapshot, error)
	PaymentOperations(ctx context.Context, page, pageSize int) (dashboard.PaymentOperationsPage, error)
}

type adminDashboardPageData struct {
	Authentication *authenticationView
	Title          string
	Equipment      []adminMetricView
	Rentals        []adminMetricView
	Finance        []adminFinanceMetricView
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
	snapshot, err := service.Snapshot(r.Context())
	if err != nil {
		logger.Error("load admin dashboard", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	data := adminDashboardPageData{
		Authentication: authenticationForPage(r),
		Title:          "Панель администратора — SUP Rental",
		Equipment: []adminMetricView{
			{Label: "Всего единиц", Value: snapshot.EquipmentTotal, Tone: "primary"},
			{Label: "Доступно", Value: snapshot.EquipmentAvailable, Tone: "success"},
			{Label: "На обслуживании", Value: snapshot.EquipmentMaintenance, Tone: "warning"},
			{Label: "Выдано", Value: snapshot.EquipmentIssued, Tone: "primary"},
			{Label: "Списано", Value: snapshot.EquipmentRetired, Tone: "neutral"},
		},
		Rentals: []adminMetricView{
			{Label: "Активные", Value: snapshot.RentalsActive, Tone: "success"},
			{Label: "Просроченные", Value: snapshot.RentalsOverdue, Tone: "danger"},
			{Label: "Начинаются сегодня", Value: snapshot.RentalsStartingToday, Tone: "primary"},
			{Label: "Завершаются сегодня", Value: snapshot.RentalsEndingToday, Tone: "warning"},
		},
		Finance: []adminFinanceMetricView{
			{
				Label: "Основные оплаты", Value: adminMoneyLabel(snapshot.PaymentsBaseTodayKopecks),
				Description: "Получено при создании аренд", Tone: "primary",
			},
			{
				Label: "Доплаты за просрочку", Value: adminMoneyLabel(snapshot.PaymentsOverdueTodayKopecks),
				Description: "Получено при завершении", Tone: "warning",
			},
			{
				Label: "Возвраты", Value: adminMoneyLabel(snapshot.PaymentsRefundTodayKopecks),
				Description: "Возвращено при отмене", Tone: "neutral",
			},
			{
				Label: "Итого за сегодня", Value: adminMoneyLabel(snapshot.PaymentsNetTodayKopecks),
				Description: "Оплаты + доплаты − возвраты", Tone: adminNetTone(snapshot.PaymentsNetTodayKopecks),
			},
		},
	}
	renderPage(
		logger, pageTemplates, w, http.StatusOK, "admin_dashboard.html", data,
		"render admin dashboard", "write admin dashboard response",
	)
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
