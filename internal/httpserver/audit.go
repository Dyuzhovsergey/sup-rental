package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Dyuzhovsergey/sup-rental/internal/audit"
	"github.com/Dyuzhovsergey/sup-rental/internal/user"
)

type auditService interface {
	List(ctx context.Context, actor user.User, filter audit.Filter) (audit.Page, error)
}

type auditFilterView struct {
	Category string
	Result   string
	Actor    string
	Target   string
	From     string
	To       string
}

type auditEventView struct {
	OccurredAt string
	Action     string
	ActionCode string
	Actor      string
	Target     string
	Result     string
	Successful bool
	Summary    string
}

type auditPageData struct {
	Authentication *authenticationView
	Title          string
	Events         []auditEventView
	Filter         auditFilterView
	TotalLabel     string
	HasPrevious    bool
	HasNext        bool
	PreviousURL    string
	NextURL        string
	PageLabel      string
	FromError      string
	ToError        string
	PeriodError    string
}

func showAuditPage(
	logger *slog.Logger,
	service auditService,
	pageTemplates *template.Template,
	w http.ResponseWriter,
	r *http.Request,
) {
	view := auditFilterView{
		Category: r.URL.Query().Get("category"), Result: r.URL.Query().Get("result"),
		Actor: r.URL.Query().Get("actor"), Target: r.URL.Query().Get("target"),
		From: r.URL.Query().Get("from"), To: r.URL.Query().Get("to"),
	}
	pageNumber, err := positivePage(r.URL.Query().Get("page"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	from, fromError := parseMoscowDate(view.From, false)
	to, toError := parseMoscowDate(view.To, true)
	if fromError != "" || toError != "" {
		renderAuditPage(logger, pageTemplates, w, r, http.StatusUnprocessableEntity, audit.Page{Page: pageNumber}, view, fromError, toError, "")
		return
	}
	filter, err := audit.NewFilter(view.Category, view.Result, view.Actor, view.Target, from, to, pageNumber)
	if errors.Is(err, audit.ErrInvalidFilter) {
		periodError := "Выберите допустимые значения фильтров."
		if from != nil && to != nil && from.After(*to) {
			periodError = "Дата начала не может быть позже даты окончания."
		}
		renderAuditPage(logger, pageTemplates, w, r, http.StatusUnprocessableEntity, audit.Page{Page: pageNumber}, view, "", "", periodError)
		return
	}
	if err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	result, err := service.List(r.Context(), currentUser(r), filter)
	if err != nil {
		logger.Error("list audit events", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	if result.Total > 0 && len(result.Events) == 0 {
		http.NotFound(w, r)
		return
	}
	renderAuditPage(logger, pageTemplates, w, r, http.StatusOK, result, view, "", "", "")
}

func renderAuditPage(
	logger *slog.Logger,
	pageTemplates *template.Template,
	w http.ResponseWriter,
	r *http.Request,
	statusCode int,
	result audit.Page,
	filter auditFilterView,
	fromError, toError, periodError string,
) {
	totalPages := pageCount(result.Total, audit.PageSize)
	data := auditPageData{
		Authentication: authenticationForPage(r), Title: "Журнал действий — SUP Rental",
		Events: auditEventViews(result.Events), Filter: filter,
		TotalLabel: auditCountLabel(result.Total), PageLabel: pageLabel(result.Page, totalPages),
		FromError: fromError, ToError: toError, PeriodError: periodError,
	}
	data.HasPrevious = result.Page > 1
	data.HasNext = result.Page < totalPages
	data.PreviousURL = auditPageURL(filter, result.Page-1)
	data.NextURL = auditPageURL(filter, result.Page+1)

	var body bytes.Buffer
	if err := pageTemplates.ExecuteTemplate(&body, "audit.html", data); err != nil {
		logger.Error("render audit page", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(statusCode)
	if _, err := w.Write(body.Bytes()); err != nil {
		logger.Error("write audit response", slog.Any("error", err))
	}
}

func parseMoscowDate(value string, endExclusive bool) (*time.Time, string) {
	if value == "" {
		return nil, ""
	}
	parsed, err := time.ParseInLocation("2006-01-02", value, moscowTimeZone)
	if err != nil {
		return nil, "Введите корректную дату."
	}
	if endExclusive {
		parsed = parsed.AddDate(0, 0, 1)
	}
	return &parsed, ""
}

func positivePage(value string) (int, error) {
	if value == "" {
		return 1, nil
	}
	page, err := strconv.Atoi(value)
	if err != nil || page <= 0 {
		return 0, audit.ErrInvalidPage
	}
	return page, nil
}

func auditEventViews(events []audit.Event) []auditEventView {
	views := make([]auditEventView, 0, len(events))
	for _, event := range events {
		actor := "Система"
		if event.ActorLogin != nil && *event.ActorLogin != "" {
			actor = *event.ActorLogin
		}
		views = append(views, auditEventView{
			OccurredAt: event.OccurredAt.In(moscowTimeZone).Format("02.01.2006 15:04:05"),
			Action:     auditActionLabel(event.Action), ActionCode: event.Action,
			Actor: actor, Target: event.TargetLabel,
			Result: auditResultLabel(event.Result), Successful: event.Result == audit.ResultSuccess,
			Summary: safeAuditSummary(event),
		})
	}
	return views
}

func safeAuditSummary(event audit.Event) string {
	if strings.HasPrefix(event.Action, "equipment.") {
		var details struct {
			Batch *struct {
				Kind                 string `json:"kind"`
				ModelCode            string `json:"model_code"`
				HourlyRateKopecks    int64  `json:"hourly_rate_kopecks"`
				Quantity             int    `json:"quantity"`
				FirstInventoryNumber string `json:"first_inventory_number"`
				LastInventoryNumber  string `json:"last_inventory_number"`
			} `json:"batch"`
			Before *struct {
				InventoryNumber string `json:"inventory_number"`
				Kind            string `json:"kind"`
				ModelCode       string `json:"model_code"`
				HourlyRate      int64  `json:"hourly_rate_kopecks"`
				Status          string `json:"status"`
			} `json:"before"`
			After *struct {
				InventoryNumber string `json:"inventory_number"`
				Kind            string `json:"kind"`
				ModelCode       string `json:"model_code"`
				HourlyRate      int64  `json:"hourly_rate_kopecks"`
				Status          string `json:"status"`
			} `json:"after"`
			ModelRate *struct {
				Kind          string `json:"kind"`
				ModelCode     string `json:"model_code"`
				BeforeKopecks int64  `json:"before_kopecks"`
				AfterKopecks  int64  `json:"after_kopecks"`
				AffectedItems int    `json:"affected_items"`
			} `json:"model_rate"`
		}
		if json.Unmarshal(event.Details, &details) != nil {
			return ""
		}
		if details.Batch != nil {
			return "Тип: " + auditEquipmentKind(details.Batch.Kind) +
				"; модель: " + details.Batch.ModelCode +
				"; тариф: " + equipmentHourlyRateLabel(details.Batch.HourlyRateKopecks) +
				"; количество: " + strconv.Itoa(details.Batch.Quantity) +
				"; номера: " + details.Batch.FirstInventoryNumber + " — " + details.Batch.LastInventoryNumber
		}
		if details.ModelRate != nil {
			return "Тип: " + auditEquipmentKind(details.ModelRate.Kind) +
				"; модель: " + details.ModelRate.ModelCode +
				"; тариф: " + equipmentHourlyRateLabel(details.ModelRate.BeforeKopecks) +
				" → " + equipmentHourlyRateLabel(details.ModelRate.AfterKopecks) +
				"; затронуто: " + equipmentUnitsLabel(details.ModelRate.AffectedItems)
		}
		if details.Before != nil && details.After != nil {
			changes := make([]string, 0, 5)
			if details.Before.InventoryNumber != details.After.InventoryNumber {
				changes = append(changes, "Номер: "+details.Before.InventoryNumber+" → "+details.After.InventoryNumber)
			}
			if details.Before.Kind != details.After.Kind {
				changes = append(changes, "Тип: "+auditEquipmentKind(details.Before.Kind)+" → "+auditEquipmentKind(details.After.Kind))
			}
			if details.Before.ModelCode != details.After.ModelCode {
				changes = append(changes, "Модель: "+details.Before.ModelCode+" → "+details.After.ModelCode)
			}
			if details.Before.HourlyRate != details.After.HourlyRate {
				changes = append(changes, "Тариф: "+equipmentHourlyRateLabel(details.Before.HourlyRate)+" → "+equipmentHourlyRateLabel(details.After.HourlyRate))
			}
			if details.Before.Status != details.After.Status {
				changes = append(changes, "Статус: "+auditEquipmentStatus(details.Before.Status)+" → "+auditEquipmentStatus(details.After.Status))
			}
			return strings.Join(changes, "; ")
		}
	}
	if event.Action == "client.updated" {
		var details struct {
			BeforeFullName string `json:"before_full_name"`
			AfterFullName  string `json:"after_full_name"`
			PhoneChanged   bool   `json:"phone_changed"`
		}
		if json.Unmarshal(event.Details, &details) != nil {
			return ""
		}
		changes := make([]string, 0, 2)
		if details.BeforeFullName != details.AfterFullName {
			changes = append(changes, "ФИО: "+details.BeforeFullName+" → "+details.AfterFullName)
		}
		if details.PhoneChanged {
			changes = append(changes, "Телефон изменён")
		}
		return strings.Join(changes, "; ")
	}
	if event.Action == "rental.confirmed" {
		var details struct {
			ClientID       int64     `json:"client_id"`
			PlannedStart   time.Time `json:"planned_start"`
			PlannedEnd     time.Time `json:"planned_end"`
			EquipmentCount int       `json:"equipment_count"`
		}
		if json.Unmarshal(event.Details, &details) != nil {
			return ""
		}
		return "Клиент ID: " + strconv.FormatInt(details.ClientID, 10) +
			"; период: " + details.PlannedStart.In(moscowTimeZone).Format("02.01.2006 15:04") +
			" — " + details.PlannedEnd.In(moscowTimeZone).Format("02.01.2006 15:04") +
			"; оборудование: " + strconv.Itoa(details.EquipmentCount)
	}
	if strings.HasPrefix(event.Action, "auth.") {
		var details struct {
			RemoteIP string `json:"remote_ip"`
		}
		if json.Unmarshal(event.Details, &details) == nil && details.RemoteIP != "" {
			return "IP: " + details.RemoteIP
		}
	}
	return ""
}

func auditActionLabel(action string) string {
	labels := map[string]string{
		"auth.login_succeeded": "Успешный вход", "auth.login_failed": "Неуспешный вход",
		"auth.login_throttled": "Вход временно заблокирован", "auth.logout": "Выход из системы",
		"admin.created": "Администратор создан", "admin.password_changed": "Пароль администратора изменён",
		"operator.created": "Оператор создан", "operator.disabled": "Оператор отключён",
		"operator.activated": "Оператор активирован", "operator.password_changed": "Пароль оператора изменён",
		"equipment.created": "Оборудование добавлено", "equipment.updated": "Оборудование изменено",
		"equipment.batch_created":      "Партия оборудования добавлена",
		"equipment.model_changed":      "Модель оборудования изменена",
		"equipment.model_rate_changed": "Тариф модели оборудования изменён",
		"equipment.status_changed":     "Состояние оборудования изменено", "equipment.retired": "Оборудование списано",
		"equipment.deleted": "Оборудование удалено",
		"client.created":    "Клиент создан", "client.updated": "Данные клиента изменены",
		"rental.confirmed": "Аренда создана и подтверждена",
	}
	if label := labels[action]; label != "" {
		return label
	}
	return action
}

func auditResultLabel(result string) string {
	if result == audit.ResultSuccess {
		return "Успешно"
	}
	return "Ошибка"
}
func auditEquipmentKind(value string) string { return equipmentKindLabelFromString(value) }
func equipmentKindLabelFromString(value string) string {
	switch value {
	case "sup_board":
		return "SUP-доска"
	case "paddle":
		return "Весло"
	case "life_jacket":
		return "Спасательный жилет"
	}
	return value
}
func auditEquipmentStatus(value string) string {
	switch value {
	case "available":
		return "Доступен"
	case "maintenance":
		return "На обслуживании"
	case "issued":
		return "Выдан"
	case "retired":
		return "Списан"
	}
	return value
}
func pageCount(total, size int) int {
	if total == 0 {
		return 1
	}
	return (total + size - 1) / size
}
func pageLabel(page, total int) string {
	return "Страница " + strconv.Itoa(page) + " из " + strconv.Itoa(total)
}
func auditCountLabel(count int) string { return strconv.Itoa(count) + " " + russianAuditWord(count) }
func russianAuditWord(count int) string {
	if count%10 == 1 && count%100 != 11 {
		return "событие"
	}
	if count%10 >= 2 && count%10 <= 4 && (count%100 < 12 || count%100 > 14) {
		return "события"
	}
	return "событий"
}

func auditPageURL(filter auditFilterView, page int) string {
	query := url.Values{}
	for key, value := range map[string]string{"category": filter.Category, "result": filter.Result, "actor": filter.Actor, "target": filter.Target, "from": filter.From, "to": filter.To} {
		if value != "" {
			query.Set(key, value)
		}
	}
	if page > 1 {
		query.Set("page", strconv.Itoa(page))
	}
	if len(query) == 0 {
		return "/admin/audit"
	}
	return "/admin/audit?" + query.Encode()
}
