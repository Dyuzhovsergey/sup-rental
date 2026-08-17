package httpserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Dyuzhovsergey/sup-rental/internal/client"
	"github.com/Dyuzhovsergey/sup-rental/internal/equipment"
	"github.com/Dyuzhovsergey/sup-rental/internal/rental"
	"github.com/Dyuzhovsergey/sup-rental/internal/session"
	"github.com/Dyuzhovsergey/sup-rental/internal/user"
)

func TestRentalWizardSelectsExistingClient(t *testing.T) {
	models := []rental.AvailableModel{{
		ModelID: 4, Kind: equipment.KindSUPBoard, ModelCode: "TOURING",
		HourlyRateKopecks: 100_000, AvailableCount: 5,
	}}
	clients := &clientServiceStub{
		find: func(context.Context, string) (client.Client, error) {
			return rentalClientFixture(), nil
		},
	}
	handler := newRentalTestHandler(t, user.RoleOperator, &rentalServiceStub{
		available: func(context.Context, rental.Interval) ([]rental.AvailableModel, error) {
			return models, nil
		},
	}, clients)

	page := httptest.NewRecorder()
	handler.ServeHTTP(page, rentalRequest(
		http.MethodGet,
		"/rentals/new/client?start=2026-08-15T10%3A08&duration_hours=1&model_id=4&quantity=1",
		nil,
	))
	if page.Code != http.StatusOK {
		t.Fatalf("GET client step status = %d", page.Code)
	}
	for _, want := range []string{"Шаг 3. Клиент", "Телефон", "ФИО нового клиента", "wizard-steps", `name="model_id" value="4"`} {
		if !strings.Contains(page.Body.String(), want) {
			t.Errorf("client step does not contain %q", want)
		}
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, rentalRequest(http.MethodPost, "/rentals/new/client", url.Values{
		"csrf_token": {"csrf-token"}, "phone": {"+79991234567"}, "full_name": {"Не должен заменить клиента"},
		"start": {"2026-08-15T10:08"}, "duration_hours": {"1"},
		"model_id": {"4"}, "quantity": {"1"},
	}))
	location, err := url.Parse(response.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	if response.Code != http.StatusSeeOther || location.Path != "/rentals/new/review" ||
		location.Query().Get("client_id") != "18" || location.Query().Get("model_id") != "4" {
		t.Fatalf("response = %d Location %q", response.Code, response.Header().Get("Location"))
	}
}

func TestRentalWizardCreatesMissingClient(t *testing.T) {
	var createdActor user.User
	clients := &clientServiceStub{
		find: func(context.Context, string) (client.Client, error) {
			return client.Client{}, client.ErrClientNotFound
		},
		create: func(_ context.Context, actor user.User, fullName, phone string) (client.Client, error) {
			createdActor = actor
			if fullName != "Анна Петрова" || phone != "+7 999 123-45-67" {
				t.Errorf("Create() values = %q, %q", fullName, phone)
			}
			return rentalClientFixture(), nil
		},
	}
	response := httptest.NewRecorder()
	newRentalTestHandler(t, user.RoleOperator, &rentalServiceStub{
		available: func(context.Context, rental.Interval) ([]rental.AvailableModel, error) {
			return []rental.AvailableModel{{
				ModelID: 4, Kind: equipment.KindSUPBoard, ModelCode: "TOURING",
				HourlyRateKopecks: 100_000, AvailableCount: 5,
			}}, nil
		},
	}, clients).ServeHTTP(
		response,
		rentalRequest(http.MethodPost, "/rentals/new/client", url.Values{
			"csrf_token": {"csrf-token"}, "phone": {"+7 999 123-45-67"}, "full_name": {"Анна Петрова"},
			"start": {"2026-08-15T10:08"}, "duration_hours": {"1"},
			"model_id": {"4"}, "quantity": {"1"},
		}),
	)
	if response.Code != http.StatusSeeOther || createdActor.Role != user.RoleOperator ||
		!strings.HasPrefix(response.Header().Get("Location"), "/rentals/new/review?") {
		t.Fatalf("response = %d, actor = %+v", response.Code, createdActor)
	}
}

func TestRentalWizardShowsClientAndAvailableModels(t *testing.T) {
	clients := rentalClientsStub()
	rentals := &rentalServiceStub{
		available: func(_ context.Context, interval rental.Interval) ([]rental.AvailableModel, error) {
			if interval.SlotCount() != 3 {
				t.Errorf("SlotCount() = %d, want 3", interval.SlotCount())
			}
			return []rental.AvailableModel{
				{ModelID: 7, Kind: equipment.KindSUPBoard, ModelCode: "LOW-STOCK", HourlyRateKopecks: 80_000, AvailableCount: 2},
				{ModelID: 4, Kind: equipment.KindSUPBoard, ModelCode: "TOURING", HourlyRateKopecks: 100_000, AvailableCount: 5},
			}, nil
		},
	}
	handler := newRentalTestHandler(t, user.RoleOperator, rentals, clients)

	period := httptest.NewRecorder()
	handler.ServeHTTP(period, rentalRequest(http.MethodGet, "/rentals/new", nil))
	if period.Code != http.StatusOK || !strings.Contains(period.Body.String(), "Шаг 1. Срок аренды") ||
		!strings.Contains(period.Body.String(), `step="60"`) ||
		!strings.Contains(period.Body.String(), `data-limited-select`) ||
		!strings.Contains(period.Body.String(), `name="duration_days"`) ||
		!strings.Contains(period.Body.String(), `<option value="31">31</option>`) ||
		!strings.Contains(period.Body.String(), `<option value="23">23</option>`) ||
		!strings.Contains(period.Body.String(), `<option value="30">30</option>`) {
		t.Fatalf("period page = %d body %q", period.Code, period.Body.String())
	}

	equipmentResponse := httptest.NewRecorder()
	handler.ServeHTTP(equipmentResponse, rentalRequest(
		http.MethodGet,
		"/rentals/new/equipment?start=2026-08-15T10%3A08&duration_days=0&duration_hours=1&duration_minutes=30",
		nil,
	))
	if equipmentResponse.Code != http.StatusOK {
		t.Fatalf("equipment page status = %d body %q", equipmentResponse.Code, equipmentResponse.Body.String())
	}
	for _, want := range []string{
		"Шаг 2. Оборудование", "TOURING", "LOW-STOCK", "1000 ₽/час", "Доступно на период",
		`max="5"`, `data-slot-count="3"`, `action="/rentals/new/client"`,
		`data-rental-kind-count="sup_board"`, "Перейти к клиенту", ">4</span>Итог",
		`id="rental-kind-sup_board"`, `id="rental-kind-paddle"`, `id="rental-kind-life_jacket"`,
		`data-quantity-decrease`, `data-quantity-increase`, `aria-label="Увеличить количество модели TOURING"`,
	} {
		if !strings.Contains(equipmentResponse.Body.String(), want) {
			t.Errorf("equipment page does not contain %q", want)
		}
	}
	if strings.Index(equipmentResponse.Body.String(), "TOURING") > strings.Index(equipmentResponse.Body.String(), "LOW-STOCK") {
		t.Error("models are not sorted by available count descending")
	}
	for _, want := range []string{"15.08.2026 10:08 — 11:38", "1 ч 30 мин"} {
		if !strings.Contains(equipmentResponse.Body.String(), want) {
			t.Errorf("equipment page does not contain arbitrary-start result %q", want)
		}
	}
	for _, want := range []string{
		"duration_days=0", "duration_hours=1", "duration_minutes=30", "start=2026-08-15T10%3A08",
	} {
		if !strings.Contains(equipmentResponse.Body.String(), want) {
			t.Errorf("back link does not preserve %q", want)
		}
	}

	periodBack := httptest.NewRecorder()
	handler.ServeHTTP(periodBack, rentalRequest(
		http.MethodGet,
		"/rentals/new?start=2026-08-15T10%3A08&duration_days=0&duration_hours=1&duration_minutes=30",
		nil,
	))
	for _, want := range []string{`value="2026-08-15T10:08"`, `<option value="1" selected>1</option>`, `<option value="30" selected>30</option>`, "15.08.2026 11:38"} {
		if !strings.Contains(periodBack.Body.String(), want) {
			t.Errorf("restored period page does not contain %q", want)
		}
	}
}

func TestRentalWizardPreservesPreselectedClientAfterEquipment(t *testing.T) {
	rentals := &rentalServiceStub{
		available: func(context.Context, rental.Interval) ([]rental.AvailableModel, error) {
			return []rental.AvailableModel{{
				ModelID: 4, Kind: equipment.KindSUPBoard, ModelCode: "TOURING",
				HourlyRateKopecks: 100_000, AvailableCount: 5,
			}}, nil
		},
	}
	response := httptest.NewRecorder()
	newRentalTestHandler(t, user.RoleOperator, rentals, rentalClientsStub()).ServeHTTP(
		response,
		rentalRequest(
			http.MethodGet,
			"/rentals/new/client?client_id=18&start=2026-08-15T10%3A08&duration_hours=1&model_id=4&quantity=2",
			nil,
		),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("client step status = %d body %q", response.Code, response.Body.String())
	}
	for _, want := range []string{
		"Шаг 3. Клиент", "Предварительно выбран", "Анна Петрова", "&#43;7 (999) 123-45-67",
		"Выбрать другого клиента", "Перейти к итогу", "Выбрано оборудования", "2",
		"/rentals/new/review?", "client_id=18", "model_id=4", "quantity=2",
	} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("preselected client step does not contain %q", want)
		}
	}
}

func TestRentalWizardShowsReviewWithCompositionAndTotals(t *testing.T) {
	models := []rental.AvailableModel{
		{ModelID: 4, Kind: equipment.KindSUPBoard, ModelCode: "TOURING", HourlyRateKopecks: 100_000, AvailableCount: 5},
		{ModelID: 5, Kind: equipment.KindPaddle, ModelCode: "CARBON", HourlyRateKopecks: 40_000, AvailableCount: 6},
		{ModelID: 6, Kind: equipment.KindLifeJacket, ModelCode: "COMFORT", HourlyRateKopecks: 20_000, AvailableCount: 8},
	}
	rentalService := &rentalServiceStub{
		available: func(context.Context, rental.Interval) ([]rental.AvailableModel, error) {
			return models, nil
		},
	}
	handler := newRentalTestHandler(t, user.RoleOperator, rentalService, rentalClientsStub())

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, rentalRequest(
		http.MethodGet,
		"/rentals/new/review?client_id=18&start=2026-08-15T10%3A08&duration_days=0&duration_hours=1&duration_minutes=30&model_id=4&quantity=1&model_id=5&quantity=2&model_id=6&quantity=3&model_id=99&quantity=0",
		nil,
	))
	if response.Code != http.StatusOK {
		t.Fatalf("review status = %d body %q", response.Code, response.Body.String())
	}
	for _, want := range []string{
		"Шаг 4. Итог", "Анна Петрова", "15.08.2026 10:08 — 11:38", "1 ч 30 мин",
		"TOURING", "CARBON", "COMFORT", "SUP-доски</span><strong>1", "Вёсла</span><strong>2",
		"Жилеты</span><strong>3", "Всего единиц</span><strong>6", "3600 ₽",
		`name="csrf_token" value="csrf-token"`, "Создать аренду", "Назад к клиенту",
	} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("review page does not contain %q", want)
		}
	}
	for _, want := range []string{
		`name="model_id" value="4"`, `name="quantity" value="1"`,
		`model_id=4`, `quantity=3`,
	} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("review page does not preserve %q", want)
		}
	}
}

func TestRentalWizardReviewRejectsUnavailableQuantity(t *testing.T) {
	rentalService := &rentalServiceStub{
		available: func(context.Context, rental.Interval) ([]rental.AvailableModel, error) {
			return []rental.AvailableModel{{
				ModelID: 4, Kind: equipment.KindSUPBoard, ModelCode: "TOURING",
				HourlyRateKopecks: 100_000, AvailableCount: 1,
			}}, nil
		},
	}
	response := httptest.NewRecorder()
	newRentalTestHandler(t, user.RoleOperator, rentalService, rentalClientsStub()).ServeHTTP(
		response,
		rentalRequest(
			http.MethodGet,
			"/rentals/new/review?client_id=18&start=2026-08-15T10%3A08&duration_hours=1&model_id=4&quantity=2",
			nil,
		),
	)
	if response.Code != http.StatusConflict ||
		!strings.Contains(response.Body.String(), "Доступное количество изменилось") ||
		!strings.Contains(response.Body.String(), "Шаг 2. Оборудование") {
		t.Fatalf("response = %d body %q", response.Code, response.Body.String())
	}
}

func TestRentalWizardRejectsInvalidPeriod(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		wantText string
	}{
		{name: "zero", query: "", wantText: "не менее 30 минут"},
		{name: "days", query: "&duration_days=32", wantText: "корректную продолжительность"},
		{name: "hours", query: "&duration_hours=24", wantText: "корректную продолжительность"},
		{name: "minutes", query: "&duration_minutes=15", wantText: "корректную продолжительность"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			newRentalTestHandler(t, user.RoleOperator, &rentalServiceStub{}, rentalClientsStub()).ServeHTTP(
				response,
				rentalRequest(
					http.MethodGet,
					"/rentals/new/equipment?client_id=18&start=2026-08-15T10%3A08"+tt.query,
					nil,
				),
			)
			if response.Code != http.StatusUnprocessableEntity ||
				!strings.Contains(response.Body.String(), tt.wantText) ||
				!strings.Contains(response.Body.String(), `aria-invalid="true"`) {
				t.Fatalf("response = %d body %q", response.Code, response.Body.String())
			}
		})
	}
}

func TestParseRentalIntervalCombinesDurationComponents(t *testing.T) {
	tests := []struct {
		name                 string
		days, hours, minutes string
		wantSlots            int
		wantEnd              string
	}{
		{name: "hours only", hours: "6", wantSlots: 12, wantEnd: "2026-08-15T16:08"},
		{name: "one day", days: "1", wantSlots: 48, wantEnd: "2026-08-16T10:08"},
		{name: "mixed", days: "2", hours: "3", minutes: "30", wantSlots: 103, wantEnd: "2026-08-17T13:38"},
		{name: "maximum", days: "31", hours: "23", minutes: "30", wantSlots: 1535, wantEnd: "2026-09-16T09:38"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			interval, startError, durationError := parseRentalInterval(
				"2026-08-15T10:08", tt.days, tt.hours, tt.minutes,
			)
			if startError != "" || durationError != "" {
				t.Fatalf("errors = %q, %q", startError, durationError)
			}
			if interval.SlotCount() != tt.wantSlots {
				t.Errorf("SlotCount() = %d, want %d", interval.SlotCount(), tt.wantSlots)
			}
			if got := interval.End().In(moscowTimeZone).Format(rentalDateTimeLayout); got != tt.wantEnd {
				t.Errorf("End() = %q, want %q", got, tt.wantEnd)
			}
		})
	}
}

func TestCreateConfirmedRentalRedirectsToList(t *testing.T) {
	var gotActor user.User
	var gotSelections []rental.ModelSelection
	rentals := &rentalServiceStub{
		create: func(_ context.Context, actor user.User, clientID int64, interval rental.Interval, selections []rental.ModelSelection) (rental.Rental, error) {
			gotActor = actor
			gotSelections = append([]rental.ModelSelection(nil), selections...)
			return rental.Restore(24, clientID, interval, rental.StatusConfirmed, nil, []rental.Item{{
				EquipmentID: 94, InventoryNumber: "SUP-TOURING-1", Kind: equipment.KindSUPBoard,
				ModelCode: "TOURING", HourlyRateKopecks: 100_000,
			}})
		},
	}
	response := httptest.NewRecorder()
	newRentalTestHandler(t, user.RoleOperator, rentals, rentalClientsStub()).ServeHTTP(
		response,
		rentalRequest(http.MethodPost, "/rentals", url.Values{
			"csrf_token": {"csrf-token"}, "client_id": {"18"},
			"start": {"2026-08-15T10:08"}, "duration_days": {"0"},
			"duration_hours": {"1"}, "duration_minutes": {"30"},
			"model_id": {"4", "8"}, "quantity": {"1", "2"},
		}),
	)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/rentals?created=24" {
		t.Fatalf("response = %d Location %q", response.Code, response.Header().Get("Location"))
	}
	if gotActor.Role != user.RoleOperator || gotActor.Login != "operator" || len(gotSelections) != 2 || gotSelections[1].Quantity != 2 {
		t.Errorf("actor = %+v, selections = %+v", gotActor, gotSelections)
	}
}

func TestCreateConfirmedRentalHandlesAvailabilityConflictAndHidesError(t *testing.T) {
	tests := []struct {
		name       string
		serviceErr error
		wantStatus int
		wantText   string
	}{
		{name: "availability changed", serviceErr: rental.ErrInsufficientEquipment, wantStatus: http.StatusConflict, wantText: "Доступное количество изменилось"},
		{name: "empty composition", serviceErr: rental.ErrRentalItemsRequired, wantStatus: http.StatusUnprocessableEntity, wantText: "Выберите хотя бы одну единицу"},
		{name: "internal", serviceErr: errors.New("database secret detail"), wantStatus: http.StatusInternalServerError, wantText: "Internal Server Error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rentals := &rentalServiceStub{
				create: func(context.Context, user.User, int64, rental.Interval, []rental.ModelSelection) (rental.Rental, error) {
					return rental.Rental{}, tt.serviceErr
				},
				available: func(context.Context, rental.Interval) ([]rental.AvailableModel, error) {
					return []rental.AvailableModel{{ModelID: 4, Kind: equipment.KindSUPBoard, ModelCode: "TOURING", HourlyRateKopecks: 100_000, AvailableCount: 1}}, nil
				},
			}
			response := httptest.NewRecorder()
			newRentalTestHandler(t, user.RoleOperator, rentals, rentalClientsStub()).ServeHTTP(
				response,
				rentalRequest(http.MethodPost, "/rentals", url.Values{
					"csrf_token": {"csrf-token"}, "client_id": {"18"},
					"start": {"2026-08-15T10:08"}, "duration_days": {"0"},
					"duration_hours": {"1"}, "duration_minutes": {"30"},
					"model_id": {"4"}, "quantity": {"1"},
				}),
			)
			if response.Code != tt.wantStatus || !strings.Contains(response.Body.String(), tt.wantText) ||
				strings.Contains(response.Body.String(), "database secret detail") {
				t.Fatalf("response = %d body %q", response.Code, response.Body.String())
			}
		})
	}
}

func TestRentalsListAndDetail(t *testing.T) {
	interval := rentalHTTPInterval(t)
	item := rental.Item{
		EquipmentID: 94, InventoryNumber: "SUP-TOURING-1", Kind: equipment.KindSUPBoard,
		ModelCode: "TOURING", HourlyRateKopecks: 100_000,
	}
	stored, err := rental.Restore(24, 18, interval, rental.StatusConfirmed, nil, []rental.Item{item})
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	rentals := &rentalServiceStub{
		list: func(_ context.Context, statuses []rental.Status, page, pageSize int) (rental.Page, error) {
			if len(statuses) != 1 || statuses[0] != rental.StatusConfirmed {
				return rental.Page{Page: page, PageSize: pageSize}, nil
			}
			return rental.Page{Rentals: []rental.Summary{{
				ID: 24, ClientID: 18, ClientName: "Анна Петрова", Interval: interval,
				Status: rental.StatusConfirmed, ItemCount: 1, PlannedTotalKopecks: 150_000,
			}}, Total: 1, Page: 1, PageSize: 5}, nil
		},
		get: func(context.Context, int64) (rental.Rental, error) { return stored, nil },
	}
	handler := newRentalTestHandler(t, user.RoleAdmin, rentals, rentalClientsStub())

	list := httptest.NewRecorder()
	handler.ServeHTTP(list, rentalRequest(http.MethodGet, "/rentals?created=24", nil))
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d", list.Code)
	}
	for _, want := range []string{"Аренда №24 создана и подтверждена", "Подтверждена", "Анна Петрова", "1 позиция", "1500 ₽", `href="/rentals/24"`} {
		if !strings.Contains(list.Body.String(), want) {
			t.Errorf("list does not contain %q", want)
		}
	}
	if strings.Contains(list.Body.String(), `href="/rentals/new"`) {
		t.Error("admin list contains create action")
	}

	detail := httptest.NewRecorder()
	handler.ServeHTTP(detail, rentalRequest(http.MethodGet, "/rentals/24", nil))
	if detail.Code != http.StatusOK {
		t.Fatalf("detail status = %d", detail.Code)
	}
	for _, want := range []string{"Аренда №24", "SUP-TOURING-1", "TOURING", "1000 ₽/час", "1 ч 30 мин", "&#43;7 (999) 123-45-67"} {
		if !strings.Contains(detail.Body.String(), want) {
			t.Errorf("detail does not contain %q", want)
		}
	}
}

func TestRentalMutationsRequireOperatorAndCSRF(t *testing.T) {
	admin := newRentalTestHandler(t, user.RoleAdmin, &rentalServiceStub{}, rentalClientsStub())
	for _, request := range []*http.Request{
		rentalRequest(http.MethodGet, "/rentals/new", nil),
		rentalRequest(http.MethodGet, "/rentals/new/client", nil),
		rentalRequest(http.MethodGet, "/rentals/new/review", nil),
		rentalRequest(http.MethodPost, "/rentals", url.Values{"csrf_token": {"csrf-token"}}),
		rentalRequest(http.MethodGet, "/rentals/24/issue", nil),
		rentalRequest(http.MethodPost, "/rentals/24/issue", url.Values{"csrf_token": {"csrf-token"}}),
		rentalRequest(http.MethodGet, "/rentals/24/cancel", nil),
		rentalRequest(http.MethodPost, "/rentals/24/cancel", url.Values{"csrf_token": {"csrf-token"}}),
		rentalRequest(http.MethodGet, "/rentals/bulk/issue?rental_id=24", nil),
		rentalRequest(http.MethodPost, "/rentals/bulk/issue", url.Values{"csrf_token": {"csrf-token"}, "rental_id": {"24"}}),
		rentalRequest(http.MethodGet, "/rentals/bulk/cancel?rental_id=24", nil),
		rentalRequest(http.MethodPost, "/rentals/bulk/cancel", url.Values{"csrf_token": {"csrf-token"}, "rental_id": {"24"}}),
	} {
		response := httptest.NewRecorder()
		admin.ServeHTTP(response, request)
		if response.Code != http.StatusForbidden {
			t.Errorf("%s %s status = %d, want 403", request.Method, request.URL.Path, response.Code)
		}
	}

	operator := newRentalTestHandler(t, user.RoleOperator, &rentalServiceStub{}, rentalClientsStub())
	for _, token := range []string{"", "wrong"} {
		for _, path := range []string{"/rentals", "/rentals/24/cancel", "/rentals/bulk/issue", "/rentals/bulk/cancel"} {
			response := httptest.NewRecorder()
			operator.ServeHTTP(response, rentalRequest(http.MethodPost, path, url.Values{"csrf_token": {token}}))
			if response.Code != http.StatusForbidden {
				t.Errorf("POST %s token %q status = %d, want 403", path, token, response.Code)
			}
		}
	}
}

func TestRentalsListSeparatesStatusesAndShowsConfirmedActions(t *testing.T) {
	interval := rentalHTTPInterval(t)
	rentalByStatus := map[rental.Status]rental.Summary{
		rental.StatusConfirmed: {ID: 24, ClientName: "Подтверждённый клиент", Interval: interval, Status: rental.StatusConfirmed, ItemCount: 1, PlannedTotalKopecks: 100_000},
		rental.StatusActive:    {ID: 25, ClientName: "Активный клиент", Interval: interval, Status: rental.StatusActive, ItemCount: 2, PlannedTotalKopecks: 200_000},
		rental.StatusCancelled: {ID: 26, ClientName: "Отменённый клиент", Interval: interval, Status: rental.StatusCancelled, ItemCount: 3, PlannedTotalKopecks: 300_000},
	}
	rentals := &rentalServiceStub{list: func(_ context.Context, statuses []rental.Status, page, pageSize int) (rental.Page, error) {
		status := statuses[0]
		if len(statuses) == 2 {
			status = rental.StatusCancelled
		}
		summary := rentalByStatus[status]
		total := 1
		if status == rental.StatusConfirmed {
			total = 6
		}
		return rental.Page{Rentals: []rental.Summary{summary}, Total: total, Page: page, PageSize: pageSize}, nil
	}}
	response := httptest.NewRecorder()
	newRentalTestHandler(t, user.RoleOperator, rentals, rentalClientsStub()).ServeHTTP(
		response, rentalRequest(http.MethodGet, "/rentals", nil),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("list status = %d body %q", response.Code, response.Body.String())
	}
	body := response.Body.String()
	confirmedIndex := strings.Index(body, "Подтверждённые аренды")
	activeIndex := strings.Index(body, "Активные аренды")
	historyIndex := strings.Index(body, "История аренд")
	if confirmedIndex < 0 || activeIndex <= confirmedIndex || historyIndex <= activeIndex {
		t.Fatalf("section order is wrong: confirmed=%d active=%d history=%d", confirmedIndex, activeIndex, historyIndex)
	}
	for _, want := range []string{
		"Подтверждённый клиент", "Активный клиент", "Отменённый клиент",
		`href="/rentals/24/issue"`, `href="/rentals/24/cancel"`, "Выдать оборудование",
		`confirmed_page=2&amp;page_size=5`, `data-rental-bulk-form`,
		`name="rental_id" value="24"`, `formaction="/rentals/bulk/issue"`,
		`formaction="/rentals/bulk/cancel"`, "Выбрать все на странице",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("list does not contain %q", want)
		}
	}
	for _, unwanted := range []string{`href="/rentals/25/issue"`, `href="/rentals/26/cancel"`} {
		if strings.Contains(body, unwanted) {
			t.Errorf("list contains unexpected action %q", unwanted)
		}
	}

	adminResponse := httptest.NewRecorder()
	newRentalTestHandler(t, user.RoleAdmin, rentals, rentalClientsStub()).ServeHTTP(
		adminResponse, rentalRequest(http.MethodGet, "/rentals", nil),
	)
	if adminResponse.Code != http.StatusOK {
		t.Fatalf("admin list status = %d", adminResponse.Code)
	}
	for _, unwanted := range []string{"data-rental-bulk-form", `name="rental_id"`, "Выдать выбранные", "Отменить выбранные"} {
		if strings.Contains(adminResponse.Body.String(), unwanted) {
			t.Errorf("admin list contains %q", unwanted)
		}
	}
}

func TestRentalBulkConfirmationAndRedirects(t *testing.T) {
	interval := rentalHTTPInterval(t)
	confirmedForID := func(id int64) rental.Rental {
		value, err := rental.Restore(id, 18, interval, rental.StatusConfirmed, nil, []rental.Item{{
			EquipmentID: id + 100, InventoryNumber: fmt.Sprintf("SUP-TOURING-%d", id), Kind: equipment.KindSUPBoard,
			ModelCode: "TOURING", HourlyRateKopecks: 100_000,
		}})
		if err != nil {
			t.Fatalf("Restore(%d) error = %v", id, err)
		}
		return value
	}
	var issuedIDs, cancelledIDs []int64
	rentals := &rentalServiceStub{
		get: func(_ context.Context, id int64) (rental.Rental, error) { return confirmedForID(id), nil },
		issueMany: func(_ context.Context, actor user.User, ids []int64) ([]rental.Rental, error) {
			if actor.Role != user.RoleOperator {
				t.Errorf("IssueMany() actor = %+v", actor)
			}
			issuedIDs = append([]int64(nil), ids...)
			return []rental.Rental{{ID: ids[0]}, {ID: ids[1]}}, nil
		},
		cancelMany: func(_ context.Context, actor user.User, ids []int64) ([]rental.Rental, error) {
			if actor.Role != user.RoleOperator {
				t.Errorf("CancelMany() actor = %+v", actor)
			}
			cancelledIDs = append([]int64(nil), ids...)
			return []rental.Rental{{ID: ids[0]}, {ID: ids[1]}}, nil
		},
	}
	handler := newRentalTestHandler(t, user.RoleOperator, rentals, rentalClientsStub())

	for _, path := range []string{
		"/rentals/bulk/issue?rental_id=24&rental_id=25",
		"/rentals/bulk/cancel?rental_id=24&rental_id=25",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, rentalRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d body %q", path, response.Code, response.Body.String())
		}
		for _, want := range []string{"Выбранные аренды", "№24", "№25", "Анна Петрова", `name="csrf_token" value="csrf-token"`} {
			if !strings.Contains(response.Body.String(), want) {
				t.Errorf("GET %s body does not contain %q", path, want)
			}
		}
	}

	form := url.Values{"csrf_token": {"csrf-token"}, "rental_id": {"24", "25"}}
	issueResponse := httptest.NewRecorder()
	handler.ServeHTTP(issueResponse, rentalRequest(http.MethodPost, "/rentals/bulk/issue", form))
	if issueResponse.Code != http.StatusSeeOther || issueResponse.Header().Get("Location") != "/rentals?bulk_issued=2" {
		t.Fatalf("bulk issue = %d Location %q", issueResponse.Code, issueResponse.Header().Get("Location"))
	}
	cancelResponse := httptest.NewRecorder()
	handler.ServeHTTP(cancelResponse, rentalRequest(http.MethodPost, "/rentals/bulk/cancel", form))
	if cancelResponse.Code != http.StatusSeeOther || cancelResponse.Header().Get("Location") != "/rentals?bulk_cancelled=2" {
		t.Fatalf("bulk cancel = %d Location %q", cancelResponse.Code, cancelResponse.Header().Get("Location"))
	}
	if fmt.Sprint(issuedIDs) != "[24 25]" || fmt.Sprint(cancelledIDs) != "[24 25]" {
		t.Fatalf("issued = %v cancelled = %v", issuedIDs, cancelledIDs)
	}
}

func TestRentalBulkErrorsAreMappedSafely(t *testing.T) {
	handler := newRentalTestHandler(t, user.RoleOperator, &rentalServiceStub{}, rentalClientsStub())
	invalid := httptest.NewRecorder()
	handler.ServeHTTP(invalid, rentalRequest(http.MethodGet, "/rentals/bulk/issue", nil))
	if invalid.Code != http.StatusUnprocessableEntity || !strings.Contains(invalid.Body.String(), "Выберите от 1 до 15") {
		t.Fatalf("invalid selection = %d body %q", invalid.Code, invalid.Body.String())
	}

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantText   string
	}{
		{name: "invalid", err: rental.ErrInvalidBulkSelection, wantStatus: http.StatusUnprocessableEntity, wantText: "Выберите от 1 до 15"},
		{name: "missing", err: rental.ErrRentalNotFound, wantStatus: http.StatusNotFound, wantText: "404 page not found"},
		{name: "stale", err: rental.ErrStatusTransitionNotAllowed, wantStatus: http.StatusConflict, wantText: "Вернитесь к списку"},
		{name: "equipment", err: rental.ErrEquipmentUnavailable, wantStatus: http.StatusConflict, wantText: "Вернитесь к списку"},
		{name: "internal", err: errors.New("database secret detail"), wantStatus: http.StatusInternalServerError, wantText: "Internal Server Error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rentals := &rentalServiceStub{issueMany: func(context.Context, user.User, []int64) ([]rental.Rental, error) {
				return nil, tt.err
			}}
			response := httptest.NewRecorder()
			newRentalTestHandler(t, user.RoleOperator, rentals, rentalClientsStub()).ServeHTTP(
				response,
				rentalRequest(http.MethodPost, "/rentals/bulk/issue", url.Values{"csrf_token": {"csrf-token"}, "rental_id": {"24"}}),
			)
			if response.Code != tt.wantStatus || !strings.Contains(response.Body.String(), tt.wantText) || strings.Contains(response.Body.String(), "database secret detail") {
				t.Fatalf("response = %d body %q", response.Code, response.Body.String())
			}
		})
	}
}

func TestRentalsListShowsBulkSuccessNotices(t *testing.T) {
	for query, want := range map[string]string{
		"bulk_issued=2":    "Выданы 2 аренды.",
		"bulk_cancelled=5": "Отменено 5 аренд.",
	} {
		response := httptest.NewRecorder()
		newRentalTestHandler(t, user.RoleOperator, &rentalServiceStub{}, rentalClientsStub()).ServeHTTP(
			response, rentalRequest(http.MethodGet, "/rentals?"+query, nil),
		)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), want) {
			t.Fatalf("GET %s = %d body %q", query, response.Code, response.Body.String())
		}
	}
}

func TestRentalIssueConfirmationAndSuccess(t *testing.T) {
	interval := rentalHTTPInterval(t)
	confirmed, err := rental.Restore(24, 18, interval, rental.StatusConfirmed, nil, []rental.Item{{
		EquipmentID: 94, InventoryNumber: "SUP-TOURING-1", Kind: equipment.KindSUPBoard,
		ModelCode: "TOURING", HourlyRateKopecks: 100_000,
	}})
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	issuedAt := time.Date(2026, time.August, 15, 10, 8, 0, 0, moscowTimeZone)
	active := confirmed
	if err := active.Issue(issuedAt); err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	var gotActor user.User
	rentals := &rentalServiceStub{
		get: func(context.Context, int64) (rental.Rental, error) { return confirmed, nil },
		issue: func(_ context.Context, actor user.User, id int64) (rental.Rental, error) {
			gotActor = actor
			if id != 24 {
				t.Errorf("Issue() id = %d", id)
			}
			return active, nil
		},
	}
	handler := newRentalTestHandler(t, user.RoleOperator, rentals, rentalClientsStub())

	page := httptest.NewRecorder()
	handler.ServeHTTP(page, rentalRequest(http.MethodGet, "/rentals/24/issue", nil))
	if page.Code != http.StatusOK {
		t.Fatalf("issue page status = %d body %q", page.Code, page.Body.String())
	}
	for _, want := range []string{"Подтверждение выдачи", "Анна Петрова", "SUP-TOURING-1", `name="csrf_token" value="csrf-token"`, "Подтвердить выдачу"} {
		if !strings.Contains(page.Body.String(), want) {
			t.Errorf("issue page does not contain %q", want)
		}
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, rentalRequest(http.MethodPost, "/rentals/24/issue", url.Values{"csrf_token": {"csrf-token"}}))
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/rentals?issued=24" {
		t.Fatalf("issue response = %d Location %q", response.Code, response.Header().Get("Location"))
	}
	if gotActor.Role != user.RoleOperator || gotActor.Login != "operator" {
		t.Fatalf("Issue() actor = %+v", gotActor)
	}
}

func TestRentalsListShowsIssuedNotice(t *testing.T) {
	interval := rentalHTTPInterval(t)
	issuedAt := time.Date(2026, time.August, 15, 10, 8, 0, 0, moscowTimeZone)
	active, err := rental.Restore(24, 18, interval, rental.StatusActive, &issuedAt, []rental.Item{{
		EquipmentID: 94, InventoryNumber: "SUP-TOURING-1", Kind: equipment.KindSUPBoard,
		ModelCode: "TOURING", HourlyRateKopecks: 100_000,
	}})
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	rentals := &rentalServiceStub{
		list: func(_ context.Context, statuses []rental.Status, page, pageSize int) (rental.Page, error) {
			if len(statuses) != 1 || statuses[0] != rental.StatusActive {
				return rental.Page{Page: page, PageSize: pageSize}, nil
			}
			return rental.Page{Rentals: []rental.Summary{{
				ID: 24, ClientID: 18, ClientName: "Анна Петрова", Interval: interval,
				Status: rental.StatusActive, ItemCount: 1, PlannedTotalKopecks: 150_000,
			}}, Total: 1, Page: 1, PageSize: 5}, nil
		},
		get: func(context.Context, int64) (rental.Rental, error) { return active, nil },
	}
	response := httptest.NewRecorder()
	newRentalTestHandler(t, user.RoleOperator, rentals, rentalClientsStub()).ServeHTTP(
		response, rentalRequest(http.MethodGet, "/rentals?issued=24", nil),
	)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Аренда №24 переведена в работу. Оборудование выдано.") {
		t.Fatalf("list response = %d body %q", response.Code, response.Body.String())
	}
}

func TestRentalIssueErrorsAreMappedSafely(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantText   string
	}{
		{name: "missing", err: rental.ErrRentalNotFound, wantStatus: http.StatusNotFound, wantText: "404 page not found"},
		{name: "wrong status", err: rental.ErrStatusTransitionNotAllowed, wantStatus: http.StatusConflict, wantText: "только подтверждённую"},
		{name: "equipment", err: rental.ErrEquipmentUnavailable, wantStatus: http.StatusConflict, wantText: "недоступно для выдачи"},
		{name: "internal", err: errors.New("database secret detail"), wantStatus: http.StatusInternalServerError, wantText: "Internal Server Error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rentals := &rentalServiceStub{issue: func(context.Context, user.User, int64) (rental.Rental, error) {
				return rental.Rental{}, tt.err
			}}
			response := httptest.NewRecorder()
			newRentalTestHandler(t, user.RoleOperator, rentals, rentalClientsStub()).ServeHTTP(
				response, rentalRequest(http.MethodPost, "/rentals/24/issue", url.Values{"csrf_token": {"csrf-token"}}),
			)
			if response.Code != tt.wantStatus || !strings.Contains(response.Body.String(), tt.wantText) || strings.Contains(response.Body.String(), "database secret detail") {
				t.Fatalf("response = %d body %q", response.Code, response.Body.String())
			}
		})
	}
}

func TestRentalCancellationConfirmationAndRedirect(t *testing.T) {
	interval := rentalHTTPInterval(t)
	confirmed, err := rental.Restore(24, 18, interval, rental.StatusConfirmed, nil, []rental.Item{{
		EquipmentID: 94, InventoryNumber: "SUP-TOURING-1", Kind: equipment.KindSUPBoard,
		ModelCode: "TOURING", HourlyRateKopecks: 100_000,
	}})
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	cancelled := confirmed
	if err := cancelled.Cancel(); err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	var gotActor user.User
	rentals := &rentalServiceStub{
		get: func(context.Context, int64) (rental.Rental, error) { return confirmed, nil },
		cancel: func(_ context.Context, actor user.User, id int64) (rental.Rental, error) {
			gotActor = actor
			if id != 24 {
				t.Errorf("Cancel() id = %d", id)
			}
			return cancelled, nil
		},
	}
	handler := newRentalTestHandler(t, user.RoleOperator, rentals, rentalClientsStub())
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, rentalRequest(http.MethodGet, "/rentals/24/cancel", nil))
	if page.Code != http.StatusOK {
		t.Fatalf("cancel page status = %d body %q", page.Code, page.Body.String())
	}
	for _, want := range []string{"Подтверждение отмены", "Анна Петрова", "SUP-TOURING-1", `name="csrf_token" value="csrf-token"`, "Подтвердить отмену", "останутся в истории"} {
		if !strings.Contains(page.Body.String(), want) {
			t.Errorf("cancel page does not contain %q", want)
		}
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, rentalRequest(http.MethodPost, "/rentals/24/cancel", url.Values{"csrf_token": {"csrf-token"}}))
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/rentals?cancelled=24" {
		t.Fatalf("cancel response = %d Location %q", response.Code, response.Header().Get("Location"))
	}
	if gotActor.Role != user.RoleOperator || gotActor.Login != "operator" {
		t.Fatalf("Cancel() actor = %+v", gotActor)
	}
}

func TestRentalCancellationPageErrorsAreMappedSafely(t *testing.T) {
	interval := rentalHTTPInterval(t)
	cancelled, err := rental.Restore(24, 18, interval, rental.StatusCancelled, nil, []rental.Item{{
		EquipmentID: 94, InventoryNumber: "SUP-TOURING-1", Kind: equipment.KindSUPBoard,
		ModelCode: "TOURING", HourlyRateKopecks: 100_000,
	}})
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	tests := []struct {
		name       string
		path       string
		get        func(context.Context, int64) (rental.Rental, error)
		wantStatus int
		wantText   string
	}{
		{name: "invalid id", path: "/rentals/not-a-number/cancel", wantStatus: http.StatusNotFound, wantText: "404 page not found"},
		{name: "missing", path: "/rentals/24/cancel", get: func(context.Context, int64) (rental.Rental, error) {
			return rental.Rental{}, rental.ErrRentalNotFound
		}, wantStatus: http.StatusNotFound, wantText: "404 page not found"},
		{name: "wrong status", path: "/rentals/24/cancel", get: func(context.Context, int64) (rental.Rental, error) {
			return cancelled, nil
		}, wantStatus: http.StatusConflict, wantText: "только подтверждённую"},
		{name: "internal", path: "/rentals/24/cancel", get: func(context.Context, int64) (rental.Rental, error) {
			return rental.Rental{}, errors.New("database secret detail")
		}, wantStatus: http.StatusInternalServerError, wantText: "Internal Server Error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			newRentalTestHandler(t, user.RoleOperator, &rentalServiceStub{get: tt.get}, rentalClientsStub()).ServeHTTP(
				response, rentalRequest(http.MethodGet, tt.path, nil),
			)
			if response.Code != tt.wantStatus || !strings.Contains(response.Body.String(), tt.wantText) || strings.Contains(response.Body.String(), "database secret detail") {
				t.Fatalf("response = %d body %q", response.Code, response.Body.String())
			}
		})
	}
}

func TestRentalCancellationErrorsAreMappedSafely(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantText   string
	}{
		{name: "missing", err: rental.ErrRentalNotFound, wantStatus: http.StatusNotFound, wantText: "404 page not found"},
		{name: "wrong status", err: rental.ErrStatusTransitionNotAllowed, wantStatus: http.StatusConflict, wantText: "только подтверждённую"},
		{name: "internal", err: errors.New("database secret detail"), wantStatus: http.StatusInternalServerError, wantText: "Internal Server Error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rentals := &rentalServiceStub{cancel: func(context.Context, user.User, int64) (rental.Rental, error) {
				return rental.Rental{}, tt.err
			}}
			response := httptest.NewRecorder()
			newRentalTestHandler(t, user.RoleOperator, rentals, rentalClientsStub()).ServeHTTP(
				response, rentalRequest(http.MethodPost, "/rentals/24/cancel", url.Values{"csrf_token": {"csrf-token"}}),
			)
			if response.Code != tt.wantStatus || !strings.Contains(response.Body.String(), tt.wantText) || strings.Contains(response.Body.String(), "database secret detail") {
				t.Fatalf("response = %d body %q", response.Code, response.Body.String())
			}
		})
	}
}

func TestRentalsListShowsCancelledNotice(t *testing.T) {
	interval := rentalHTTPInterval(t)
	cancelled, err := rental.Restore(24, 18, interval, rental.StatusCancelled, nil, []rental.Item{{
		EquipmentID: 94, InventoryNumber: "SUP-TOURING-1", Kind: equipment.KindSUPBoard,
		ModelCode: "TOURING", HourlyRateKopecks: 100_000,
	}})
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	rentals := &rentalServiceStub{
		list: func(_ context.Context, statuses []rental.Status, page, pageSize int) (rental.Page, error) {
			if len(statuses) != 2 {
				return rental.Page{Page: page, PageSize: pageSize}, nil
			}
			return rental.Page{Rentals: []rental.Summary{{
				ID: 24, ClientID: 18, ClientName: "Анна Петрова", Interval: interval,
				Status: rental.StatusCancelled, ItemCount: 1, PlannedTotalKopecks: 150_000,
			}}, Total: 1, Page: page, PageSize: pageSize}, nil
		},
		get: func(context.Context, int64) (rental.Rental, error) { return cancelled, nil },
	}
	response := httptest.NewRecorder()
	newRentalTestHandler(t, user.RoleOperator, rentals, rentalClientsStub()).ServeHTTP(
		response, rentalRequest(http.MethodGet, "/rentals?cancelled=24", nil),
	)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Аренда №24 отменена.") {
		t.Fatalf("list response = %d body %q", response.Code, response.Body.String())
	}
}

func newRentalTestHandler(t *testing.T, role user.Role, rentals rentalService, clients clientService) http.Handler {
	t.Helper()
	authenticated := authenticatedFixture()
	authenticated.User.Role = role
	if role == user.RoleOperator {
		authenticated.User.Login = "operator"
	}
	resolver := &sessionResolverStub{resolve: func(context.Context, string) (session.AuthenticatedSession, error) {
		return authenticated, nil
	}}
	handler, err := NewHandler(
		slog.New(slog.NewTextHandler(io.Discard, nil)), &equipmentServiceStub{},
		&authServiceStub{}, resolver, &operatorServiceStub{}, &auditServiceStub{}, clients,
		rentals, CookieSettings{},
	)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	return handler
}

func rentalRequest(method, target string, values url.Values) *http.Request {
	var body io.Reader
	if values != nil {
		body = strings.NewReader(values.Encode())
	}
	request := httptest.NewRequest(method, target, body)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "raw-session-token"})
	if values != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	return request
}

func rentalClientsStub() *clientServiceStub {
	return &clientServiceStub{
		get: func(_ context.Context, id int64) (client.Client, error) {
			if id != 18 {
				return client.Client{}, client.ErrClientNotFound
			}
			return rentalClientFixture(), nil
		},
	}
}

func rentalClientFixture() client.Client {
	customer, _ := client.New("Анна Петрова", "+79991234567")
	customer.ID = 18
	return customer
}

func rentalHTTPInterval(t *testing.T) rental.Interval {
	t.Helper()
	interval, err := rental.NewInterval(
		time.Date(2026, time.August, 15, 10, 0, 0, 0, moscowTimeZone),
		time.Date(2026, time.August, 15, 11, 30, 0, 0, moscowTimeZone),
	)
	if err != nil {
		t.Fatalf("NewInterval() error = %v", err)
	}
	return interval
}
