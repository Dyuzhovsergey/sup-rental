package httpserver

import (
	"fmt"
	"html/template"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/Dyuzhovsergey/sup-rental/internal/rental"
)

type operatorDashboardPageData struct {
	Authentication *authenticationView
	Title          string
	TodayLabel     string
	Metrics        []operatorMetricView
	Confirmed      operatorMonitoringSectionView
	Active         operatorMonitoringSectionView
}

type operatorMetricView struct {
	Label string
	Value int
	Tone  string
}

type operatorMonitoringSectionView struct {
	Heading     string
	Description string
	CountLabel  string
	Rentals     []operatorMonitoringRentalView
	EmptyTitle  string
	EmptyText   string
	Limited     bool
}

type operatorMonitoringRentalView struct {
	ID            int64
	ClientName    string
	Period        string
	End           string
	ItemCount     string
	TimingLabel   string
	TimingTone    string
	Progress      int
	ProgressLabel string
}

func showOperatorDashboard(
	logger *slog.Logger,
	rentals rentalService,
	pageTemplates *template.Template,
	w http.ResponseWriter,
	r *http.Request,
) {
	snapshot, err := rentals.Monitoring(r.Context())
	if err != nil {
		logger.Error("load operator rental monitoring", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	data := operatorDashboardPageData{
		Authentication: authenticationForPage(r),
		Title:          "Рабочее место оператора — SUP Rental",
		TodayLabel:     russianDateLabel(snapshot.GeneratedAt.In(moscowTimeZone)),
		Metrics: []operatorMetricView{
			{Label: "Аренды сегодня", Value: snapshot.TodayTotal, Tone: "primary"},
			{Label: "Ожидают выдачи", Value: snapshot.ConfirmedTotal, Tone: "warning"},
			{Label: "Активные", Value: snapshot.ActiveTotal, Tone: "success"},
			{Label: "Просрочены", Value: snapshot.OverdueTotal, Tone: "danger"},
		},
		Confirmed: operatorMonitoringSectionView{
			Heading: "Ожидают выдачи сегодня", Description: "Подтверждённые аренды текущего дня",
			CountLabel: rentalCountLabel(snapshot.ConfirmedTotal), Rentals: operatorMonitoringViews(snapshot.Confirmed),
			EmptyTitle: "Выдач на сегодня нет", EmptyText: "Новые подтверждённые аренды появятся здесь.",
			Limited: snapshot.ConfirmedTotal > len(snapshot.Confirmed),
		},
		Active: operatorMonitoringSectionView{
			Heading: "Активные аренды", Description: "Оборудование находится у клиентов",
			CountLabel: rentalCountLabel(snapshot.ActiveTotal), Rentals: operatorMonitoringViews(snapshot.Active),
			EmptyTitle: "Активных аренд нет", EmptyText: "После выдачи аренды появятся здесь.",
			Limited: snapshot.ActiveTotal > len(snapshot.Active),
		},
	}
	renderPage(
		logger, pageTemplates, w, http.StatusOK, "operator.html", data,
		"render operator dashboard", "write operator dashboard response",
	)
}

func operatorMonitoringViews(entries []rental.MonitoringEntry) []operatorMonitoringRentalView {
	views := make([]operatorMonitoringRentalView, 0, len(entries))
	for _, entry := range entries {
		label, tone := operatorTimingLabel(entry.Timing)
		views = append(views, operatorMonitoringRentalView{
			ID: entry.Summary.ID, ClientName: entry.Summary.ClientName,
			Period:      rentalPeriodLabel(entry.Summary.Interval),
			End:         entry.Summary.Interval.End().In(moscowTimeZone).Format("02.01 15:04"),
			ItemCount:   rentalItemCountLabel(entry.Summary.ItemCount),
			TimingLabel: label, TimingTone: tone, Progress: entry.Timing.Percent,
			ProgressLabel: fmt.Sprintf("Плановый период аренды №%d: %d%%", entry.Summary.ID, entry.Timing.Percent),
		})
	}
	return views
}

func operatorTimingLabel(timing rental.MonitoringTiming) (string, string) {
	delta := monitoringDurationLabel(timing.Delta)
	switch timing.State {
	case rental.MonitoringUpcoming:
		return "До начала " + delta, "neutral"
	case rental.MonitoringIssueDelayed:
		return "Выдача задерживается на " + delta, "warning"
	case rental.MonitoringActive:
		return "Осталось " + delta, "success"
	case rental.MonitoringDue:
		return "Плановое время завершения наступило", "warning"
	case rental.MonitoringOverdue:
		return "Просрочена на " + delta, "danger"
	default:
		return "Время не определено", "neutral"
	}
}

func monitoringDurationLabel(value time.Duration) string {
	if value < 0 {
		value = -value
	}
	minutes := int(math.Ceil(value.Minutes()))
	if minutes <= 0 {
		return "менее минуты"
	}
	days := minutes / (24 * 60)
	hours := minutes % (24 * 60) / 60
	remainingMinutes := minutes % 60
	parts := make([]string, 0, 3)
	if days > 0 {
		parts = append(parts, strconv.Itoa(days)+" "+russianDayWord(days))
	}
	if hours > 0 {
		parts = append(parts, strconv.Itoa(hours)+" "+russianHourWord(hours))
	}
	if remainingMinutes > 0 {
		parts = append(parts, strconv.Itoa(remainingMinutes)+" мин")
	}
	result := parts[0]
	for _, part := range parts[1:] {
		result += " " + part
	}
	return result
}

func russianDayWord(days int) string {
	if days%10 == 1 && days%100 != 11 {
		return "день"
	}
	if days%10 >= 2 && days%10 <= 4 && (days%100 < 12 || days%100 > 14) {
		return "дня"
	}
	return "дней"
}

func russianDateLabel(value time.Time) string {
	months := [...]string{
		"", "января", "февраля", "марта", "апреля", "мая", "июня",
		"июля", "августа", "сентября", "октября", "ноября", "декабря",
	}
	return fmt.Sprintf("%d %s %d", value.Day(), months[value.Month()], value.Year())
}
