package httpserver

import (
	"context"
	"html/template"
	"log/slog"
	"net/http"

	"github.com/Dyuzhovsergey/sup-rental/internal/dashboard"
)

type adminDashboardService interface {
	Snapshot(ctx context.Context) (dashboard.Snapshot, error)
}

type adminDashboardPageData struct {
	Authentication *authenticationView
	Title          string
	Equipment      []adminMetricView
	Rentals        []adminMetricView
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
	}
	renderPage(
		logger, pageTemplates, w, http.StatusOK, "admin_dashboard.html", data,
		"render admin dashboard", "write admin dashboard response",
	)
}
