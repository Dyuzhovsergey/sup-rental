package httpserver

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Dyuzhovsergey/sup-rental/internal/equipment"
)

func TestEquipmentPageListsItems(t *testing.T) {
	service := &equipmentServiceStub{
		list: func(_ context.Context) ([]equipment.Item, error) {
			return []equipment.Item{{
				ID:              1,
				InventoryNumber: "SUP-001",
				Kind:            equipment.KindSUPBoard,
				Status:          equipment.StatusAvailable,
			}}, nil
		},
	}
	request := httptest.NewRequest(http.MethodGet, "/equipment", nil)
	response := httptest.NewRecorder()

	newTestHandler(t, discardLogger(), service).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Errorf("status code = %d, want %d", response.Code, http.StatusOK)
	}

	for _, want := range []string{
		`action="/equipment"`,
		`name="inventory_number"`,
		`value="sup_board"`,
		`class="equipment-layout"`,
		`class="panel equipment-create-panel"`,
		`class="panel equipment-list-panel"`,
		`class="main-content main-content--equipment"`,
		`class="count-chip count-chip--total">Всего 1 позиция</span>`,
		`class="count-chip">1 позиция</span>`,
		`href="/equipment/1">SUP-001</a>`,
		"SUP-001",
		"SUP-доска",
		"Доступен",
	} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("body = %q, want it to contain %q", response.Body.String(), want)
		}
	}
}

func TestEquipmentBusinessPagesUseSharedApplicationShell(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		status equipment.Status
	}{
		{name: "list", path: "/equipment", status: equipment.StatusAvailable},
		{name: "detail", path: "/equipment/17", status: equipment.StatusAvailable},
		{name: "edit", path: "/equipment/17/edit", status: equipment.StatusAvailable},
		{name: "retirement", path: "/equipment/17/retire", status: equipment.StatusAvailable},
		{name: "deletion", path: "/equipment/17/delete", status: equipment.StatusRetired},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := equipment.Item{
				ID:              17,
				InventoryNumber: "SUP-017",
				Kind:            equipment.KindSUPBoard,
				Status:          tt.status,
			}
			service := &equipmentServiceStub{
				list: func(_ context.Context) ([]equipment.Item, error) {
					return []equipment.Item{item}, nil
				},
				get: func(_ context.Context, _ int64) (equipment.Item, error) {
					return item, nil
				},
			}
			request := httptest.NewRequest(http.MethodGet, tt.path, nil)
			response := httptest.NewRecorder()

			newTestHandler(t, discardLogger(), service).ServeHTTP(response, request)

			if response.Code != http.StatusOK {
				t.Fatalf("status code = %d, want %d", response.Code, http.StatusOK)
			}

			body := response.Body.String()
			for _, want := range []string{
				`<link rel="stylesheet" href="/static/app.css">`,
				`class="skip-link"`,
				`class="app-shell"`,
				`class="sidebar"`,
				`id="main-content"`,
			} {
				if !strings.Contains(body, want) {
					t.Errorf("body = %q, want it to contain %q", body, want)
				}
			}
			if strings.Contains(body, "<style") {
				t.Errorf("body = %q, want styles loaded only from shared stylesheet", body)
			}
		})
	}
}

func TestEquipmentPageShowsStatusesWithoutControls(t *testing.T) {
	service := &equipmentServiceStub{
		list: func(_ context.Context) ([]equipment.Item, error) {
			return []equipment.Item{
				{ID: 1, InventoryNumber: "SUP-001", Status: equipment.StatusAvailable},
				{ID: 2, InventoryNumber: "PADDLE-001", Status: equipment.StatusMaintenance},
				{ID: 3, InventoryNumber: "JACKET-001", Status: equipment.StatusIssued},
				{ID: 4, InventoryNumber: "SUP-002", Status: equipment.StatusRetired},
			}, nil
		},
	}
	request := httptest.NewRequest(http.MethodGet, "/equipment", nil)
	response := httptest.NewRecorder()

	newTestHandler(t, discardLogger(), service).ServeHTTP(response, request)

	body := response.Body.String()
	for _, want := range []string{
		`<th scope="col">Статус</th>`,
		"Доступен",
		"На обслуживании",
		"Выдан",
		"Списан",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body = %q, want it to contain %q", body, want)
		}
	}

	for _, unwanted := range []string{
		`action="/equipment/1/status"`,
		`action="/equipment/2/status"`,
		`name="status"`,
		`onchange=`,
		`<th scope="col">Действие</th>`,
		`<th scope="col">Состояние</th>`,
	} {
		if strings.Contains(body, unwanted) {
			t.Errorf("body = %q, want it not to contain %q", body, unwanted)
		}
	}
}

func TestEquipmentPageShowsAvailableActions(t *testing.T) {
	service := &equipmentServiceStub{
		list: func(_ context.Context) ([]equipment.Item, error) {
			return []equipment.Item{
				{ID: 1, InventoryNumber: "SUP-001", Status: equipment.StatusAvailable},
				{ID: 2, InventoryNumber: "PADDLE-001", Status: equipment.StatusMaintenance},
				{ID: 3, InventoryNumber: "JACKET-001", Status: equipment.StatusIssued},
				{ID: 4, InventoryNumber: "SUP-002", Status: equipment.StatusRetired},
			}, nil
		},
	}
	request := httptest.NewRequest(http.MethodGet, "/equipment", nil)
	response := httptest.NewRecorder()

	newTestHandler(t, discardLogger(), service).ServeHTTP(response, request)

	body := response.Body.String()
	for _, want := range []string{
		`<th scope="col">Действия</th>`,
		`role="group"`,
		`button--edit action-button edit-action`,
		`href="/equipment/1/edit"`,
		`href="/equipment/2/edit"`,
		`retire-action`,
		`href="/equipment/1/retire"`,
		`href="/equipment/2/retire"`,
		`delete-action`,
		`href="/equipment/3/delete"`,
		`href="/equipment/4/delete"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body = %q, want it to contain %q", body, want)
		}
	}

	for _, unwanted := range []string{
		`href="/equipment/3/edit"`,
		`href="/equipment/4/edit"`,
		`href="/equipment/1/delete"`,
		`href="/equipment/2/delete"`,
		`href="/equipment/3/retire"`,
		`href="/equipment/4/retire"`,
	} {
		if strings.Contains(body, unwanted) {
			t.Errorf("body = %q, want it not to contain %q", body, unwanted)
		}
	}
}

func TestEquipmentPageSeparatesRetiredItems(t *testing.T) {
	service := &equipmentServiceStub{
		list: func(_ context.Context) ([]equipment.Item, error) {
			return []equipment.Item{
				{
					ID:              4,
					InventoryNumber: "RETIRED-001",
					Kind:            equipment.KindPaddle,
					Status:          equipment.StatusRetired,
				},
				{
					ID:              1,
					InventoryNumber: "ACTIVE-001",
					Kind:            equipment.KindSUPBoard,
					Status:          equipment.StatusAvailable,
				},
			}, nil
		},
	}
	request := httptest.NewRequest(http.MethodGet, "/equipment", nil)
	response := httptest.NewRecorder()

	newTestHandler(t, discardLogger(), service).ServeHTTP(response, request)

	body := response.Body.String()
	for _, want := range []string{
		`class="count-chip count-chip--total">Всего 2 позиции</span>`,
		`id="equipment-active-heading">Список оборудования</h2>`,
		`id="equipment-retired-heading">Списанное оборудование</h2>`,
		`class="panel equipment-list-panel retired-equipment-panel"`,
		`href="/equipment/1">ACTIVE-001</a>`,
		`href="/equipment/4">RETIRED-001</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body = %q, want it to contain %q", body, want)
		}
	}

	activePosition := strings.Index(body, "ACTIVE-001")
	retiredHeadingPosition := strings.Index(body, "Списанное оборудование")
	retiredPosition := strings.Index(body, "RETIRED-001")
	if activePosition < 0 || retiredHeadingPosition < 0 || retiredPosition < 0 {
		t.Fatalf("body does not contain all equipment sections")
	}
	if activePosition > retiredHeadingPosition || retiredHeadingPosition > retiredPosition {
		t.Errorf(
			"section order = active %d, retired heading %d, retired %d; want active section first",
			activePosition,
			retiredHeadingPosition,
			retiredPosition,
		)
	}
}

func TestEquipmentCountLabel(t *testing.T) {
	tests := []struct {
		count int
		want  string
	}{
		{count: 0, want: "0 позиций"},
		{count: 1, want: "1 позиция"},
		{count: 2, want: "2 позиции"},
		{count: 4, want: "4 позиции"},
		{count: 5, want: "5 позиций"},
		{count: 11, want: "11 позиций"},
		{count: 14, want: "14 позиций"},
		{count: 21, want: "21 позиция"},
		{count: 22, want: "22 позиции"},
		{count: 25, want: "25 позиций"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := equipmentCountLabel(tt.count); got != tt.want {
				t.Errorf("equipmentCountLabel(%d) = %q, want %q", tt.count, got, tt.want)
			}
		})
	}
}

func TestEquipmentDetailPageShowsItem(t *testing.T) {
	var gotID int64
	service := &equipmentServiceStub{
		get: func(_ context.Context, id int64) (equipment.Item, error) {
			gotID = id
			return equipment.Item{
				ID:              id,
				InventoryNumber: "SUP-017",
				Kind:            equipment.KindSUPBoard,
				Status:          equipment.StatusAvailable,
			}, nil
		},
	}
	request := httptest.NewRequest(http.MethodGet, "/equipment/17", nil)
	response := httptest.NewRecorder()

	newTestHandler(t, discardLogger(), service).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Errorf("status code = %d, want %d", response.Code, http.StatusOK)
	}
	if gotID != 17 {
		t.Errorf("Get() ID = %d, want 17", gotID)
	}
	if got := response.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want %q", got, "text/html; charset=utf-8")
	}

	for _, want := range []string{
		"<dl",
		"<h1>SUP-017</h1>",
		"Инвентарный номер",
		"SUP-017",
		"SUP-доска",
		"Доступен",
		"Внутренний ID",
		">17</dd>",
		`href="/equipment/17/edit"`,
		`href="/equipment">Назад к списку</a>`,
		`role="group"`,
	} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("body = %q, want it to contain %q", response.Body.String(), want)
		}
	}
	if got := strings.Count(
		response.Body.String(),
		`<span class="status-chip status-chip--available">`,
	); got != 1 {
		t.Errorf("available status chips = %d, want 1", got)
	}
	if strings.Contains(response.Body.String(), `href="/equipment/17/retire"`) {
		t.Errorf("body = %q, want retirement available only from edit page", response.Body.String())
	}
}

func TestEquipmentDetailPageShowsOnlyAllowedActions(t *testing.T) {
	tests := []struct {
		name           string
		status         equipment.Status
		wantEditLink   bool
		wantDeleteLink bool
	}{
		{
			name:         "available equipment",
			status:       equipment.StatusAvailable,
			wantEditLink: true,
		},
		{
			name:         "equipment under maintenance",
			status:       equipment.StatusMaintenance,
			wantEditLink: true,
		},
		{
			name:   "issued equipment",
			status: equipment.StatusIssued,
		},
		{
			name:           "retired equipment",
			status:         equipment.StatusRetired,
			wantDeleteLink: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &equipmentServiceStub{
				get: func(_ context.Context, id int64) (equipment.Item, error) {
					return equipment.Item{
						ID:              id,
						InventoryNumber: "SUP-017",
						Kind:            equipment.KindSUPBoard,
						Status:          tt.status,
					}, nil
				},
			}
			request := httptest.NewRequest(http.MethodGet, "/equipment/17", nil)
			response := httptest.NewRecorder()

			newTestHandler(t, discardLogger(), service).ServeHTTP(response, request)

			if response.Code != http.StatusOK {
				t.Errorf("status code = %d, want %d", response.Code, http.StatusOK)
			}
			hasEditLink := strings.Contains(
				response.Body.String(),
				`href="/equipment/17/edit"`,
			)
			if hasEditLink != tt.wantEditLink {
				t.Errorf("edit link presence = %t, want %t", hasEditLink, tt.wantEditLink)
			}
			if strings.Contains(response.Body.String(), `href="/equipment/17/retire"`) {
				t.Errorf(
					"body = %q, want retirement available only from edit page",
					response.Body.String(),
				)
			}
			hasDeleteLink := strings.Contains(
				response.Body.String(),
				`href="/equipment/17/delete"`,
			)
			if hasDeleteLink != tt.wantDeleteLink {
				t.Errorf(
					"delete link presence = %t, want %t",
					hasDeleteLink,
					tt.wantDeleteLink,
				)
			}
		})
	}
}

func TestEquipmentPageShowsRetirementSuccess(t *testing.T) {
	service := &equipmentServiceStub{
		list: func(_ context.Context) ([]equipment.Item, error) {
			return []equipment.Item{{
				ID:              17,
				InventoryNumber: "SUP-017",
				Kind:            equipment.KindSUPBoard,
				Status:          equipment.StatusRetired,
			}}, nil
		},
	}
	request := httptest.NewRequest(
		http.MethodGet,
		"/equipment?inventory_number=SUP-017&kind=sup_board&notice=retired",
		nil,
	)
	response := httptest.NewRecorder()

	newTestHandler(t, discardLogger(), service).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Errorf("status code = %d, want %d", response.Code, http.StatusOK)
	}
	for _, want := range []string{
		`role="status"`,
		`aria-live="polite"`,
		"Оборудование SUP-доска SUP-017 списано.",
		"Списан",
	} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("body = %q, want it to contain %q", response.Body.String(), want)
		}
	}
	if !strings.Contains(response.Body.String(), "Списан") {
		t.Errorf("body = %q, want retired equipment status", response.Body.String())
	}
}

func TestEquipmentDetailPageErrors(t *testing.T) {
	const internalErrorText = "database password leaked"
	tests := []struct {
		name       string
		path       string
		serviceErr error
		wantStatus int
		wantText   string
	}{
		{
			name:       "invalid equipment ID",
			path:       "/equipment/not-a-number",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "zero equipment ID",
			path:       "/equipment/0",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "negative equipment ID",
			path:       "/equipment/-1",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "equipment not found",
			path:       "/equipment/17",
			serviceErr: equipment.ErrEquipmentNotFound,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "internal error is hidden",
			path:       "/equipment/17",
			serviceErr: errors.New(internalErrorText),
			wantStatus: http.StatusInternalServerError,
			wantText:   "Internal Server Error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &equipmentServiceStub{
				get: func(_ context.Context, _ int64) (equipment.Item, error) {
					return equipment.Item{}, tt.serviceErr
				},
			}
			request := httptest.NewRequest(http.MethodGet, tt.path, nil)
			response := httptest.NewRecorder()

			newTestHandler(t, discardLogger(), service).ServeHTTP(response, request)

			if response.Code != tt.wantStatus {
				t.Errorf("status code = %d, want %d", response.Code, tt.wantStatus)
			}
			if tt.wantText != "" && !strings.Contains(response.Body.String(), tt.wantText) {
				t.Errorf("body = %q, want it to contain %q", response.Body.String(), tt.wantText)
			}
			if strings.Contains(response.Body.String(), internalErrorText) {
				t.Errorf("body = %q, want internal error hidden", response.Body.String())
			}
		})
	}
}

func TestEquipmentDetailPageRejectsUnsupportedMethod(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/equipment/17", nil)
	response := httptest.NewRecorder()

	newTestHandler(t, discardLogger()).ServeHTTP(response, request)

	if response.Code != http.StatusMethodNotAllowed {
		t.Errorf("status code = %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}
	if got := response.Header().Get("Allow"); got != http.MethodGet {
		t.Errorf("Allow = %q, want %q", got, http.MethodGet)
	}
}

func TestEquipmentPageShowsUpdateSuccess(t *testing.T) {
	request := httptest.NewRequest(
		http.MethodGet,
		"/equipment?inventory_number=SUP-017-UPDATED&kind=life_jacket&notice=updated",
		nil,
	)
	response := httptest.NewRecorder()

	newTestHandler(t, discardLogger()).ServeHTTP(response, request)

	for _, want := range []string{
		`role="status"`,
		`aria-live="polite"`,
		"Оборудование Спасательный жилет SUP-017-UPDATED обновлено.",
	} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("body = %q, want it to contain %q", response.Body.String(), want)
		}
	}
}

func TestEquipmentPageShowsDeletionSuccess(t *testing.T) {
	request := httptest.NewRequest(
		http.MethodGet,
		"/equipment?inventory_number=SUP-017&kind=sup_board&notice=deleted",
		nil,
	)
	response := httptest.NewRecorder()

	newTestHandler(t, discardLogger()).ServeHTTP(response, request)

	for _, want := range []string{
		`role="status"`,
		`aria-live="polite"`,
		"Оборудование SUP-доска SUP-017 удалено.",
	} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("body = %q, want it to contain %q", response.Body.String(), want)
		}
	}
}

func TestCreateEquipmentRedirectsToList(t *testing.T) {
	var gotInput equipment.CreateInput
	service := &equipmentServiceStub{
		create: func(_ context.Context, input equipment.CreateInput) (equipment.Item, error) {
			gotInput = input
			return equipment.Item{ID: 1}, nil
		},
	}
	form := url.Values{
		"inventory_number": {"SUP-001"},
		"kind":             {string(equipment.KindSUPBoard)},
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/equipment",
		strings.NewReader(form.Encode()),
	)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()

	newTestHandler(t, discardLogger(), service).ServeHTTP(response, request)

	if response.Code != http.StatusSeeOther {
		t.Errorf("status code = %d, want %d", response.Code, http.StatusSeeOther)
	}
	if got := response.Header().Get("Location"); got != "/equipment" {
		t.Errorf("Location = %q, want %q", got, "/equipment")
	}

	wantInput := equipment.CreateInput{
		InventoryNumber: "SUP-001",
		Kind:            equipment.KindSUPBoard,
	}
	if gotInput != wantInput {
		t.Errorf("Create() input = %+v, want %+v", gotInput, wantInput)
	}
}

func TestCreateEquipmentShowsValidationErrors(t *testing.T) {
	tests := []struct {
		name       string
		serviceErr error
		wantStatus int
		wantText   string
		wantAria   string
	}{
		{
			name:       "empty inventory number",
			serviceErr: equipment.ErrInventoryNumberRequired,
			wantStatus: http.StatusUnprocessableEntity,
			wantText:   "Введите инвентарный номер.",
			wantAria:   `aria-invalid="true" aria-describedby="create-inventory-number-error"`,
		},
		{
			name:       "invalid kind",
			serviceErr: equipment.ErrInvalidKind,
			wantStatus: http.StatusUnprocessableEntity,
			wantText:   "Выберите тип оборудования.",
			wantAria:   `aria-invalid="true" aria-describedby="create-kind-error"`,
		},
		{
			name:       "duplicate inventory number",
			serviceErr: equipment.ErrInventoryNumberExists,
			wantStatus: http.StatusConflict,
			wantText:   "Оборудование с таким инвентарным номером уже существует.",
			wantAria:   `aria-invalid="true" aria-describedby="create-inventory-number-error"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &equipmentServiceStub{
				create: func(_ context.Context, _ equipment.CreateInput) (equipment.Item, error) {
					return equipment.Item{}, tt.serviceErr
				},
			}
			form := url.Values{
				"inventory_number": {"SUP-001"},
				"kind":             {string(equipment.KindSUPBoard)},
			}
			request := httptest.NewRequest(
				http.MethodPost,
				"/equipment",
				strings.NewReader(form.Encode()),
			)
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			response := httptest.NewRecorder()

			newTestHandler(t, discardLogger(), service).ServeHTTP(response, request)

			if response.Code != tt.wantStatus {
				t.Errorf("status code = %d, want %d", response.Code, tt.wantStatus)
			}
			for _, want := range []string{tt.wantText, `value="SUP-001"`, `selected`} {
				if !strings.Contains(response.Body.String(), want) {
					t.Errorf("body = %q, want it to contain %q", response.Body.String(), want)
				}
			}
			if !strings.Contains(response.Body.String(), tt.wantAria) {
				t.Errorf("body = %q, want it to contain %q", response.Body.String(), tt.wantAria)
			}
		})
	}
}

func TestCreateEquipmentHidesInternalError(t *testing.T) {
	const internalErrorText = "database password leaked"
	service := &equipmentServiceStub{
		create: func(_ context.Context, _ equipment.CreateInput) (equipment.Item, error) {
			return equipment.Item{}, errors.New(internalErrorText)
		},
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/equipment",
		strings.NewReader("inventory_number=SUP-001&kind=sup_board"),
	)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()

	newTestHandler(t, discardLogger(), service).ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Errorf(
			"status code = %d, want %d",
			response.Code,
			http.StatusInternalServerError,
		)
	}
	if strings.Contains(response.Body.String(), internalErrorText) {
		t.Errorf("body = %q, want internal error hidden", response.Body.String())
	}
}

func TestCreateEquipmentRejectsMalformedForm(t *testing.T) {
	request := httptest.NewRequest(
		http.MethodPost,
		"/equipment",
		strings.NewReader("inventory_number=%zz"),
	)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()

	newTestHandler(t, discardLogger()).ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Errorf("status code = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestEquipmentEditPageShowsCurrentValues(t *testing.T) {
	var gotID int64
	service := &equipmentServiceStub{
		get: func(_ context.Context, id int64) (equipment.Item, error) {
			gotID = id
			return equipment.Item{
				ID:              id,
				InventoryNumber: "SUP-017",
				Kind:            equipment.KindSUPBoard,
				Status:          equipment.StatusAvailable,
			}, nil
		},
	}
	request := httptest.NewRequest(http.MethodGet, "/equipment/17/edit", nil)
	response := httptest.NewRecorder()

	newTestHandler(t, discardLogger(), service).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Errorf("status code = %d, want %d", response.Code, http.StatusOK)
	}
	if gotID != 17 {
		t.Errorf("Get() ID = %d, want 17", gotID)
	}
	for _, want := range []string{
		`action="/equipment/17/edit"`,
		`value="SUP-017"`,
		`value="sup_board" selected`,
		`value="available" selected`,
		`value="maintenance"`,
		`for="inventory-number"`,
		`for="kind"`,
		`for="status"`,
		`href="/equipment/17">Отмена</a>`,
		`href="/equipment/17/retire"`,
		`class="content-stack content-stack--narrow"`,
		`class="panel retirement-panel"`,
		`class="panel-header retirement-panel-header"`,
	} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("body = %q, want it to contain %q", response.Body.String(), want)
		}
	}
}

func TestEquipmentEditableStatusOptionsAlwaysShowBothStatuses(t *testing.T) {
	for _, current := range []equipment.Status{
		equipment.StatusAvailable,
		equipment.StatusMaintenance,
	} {
		t.Run(string(current), func(t *testing.T) {
			options := equipmentEditableStatusOptions(current)
			if len(options) != 2 {
				t.Fatalf("options count = %d, want 2", len(options))
			}

			if options[0].Value != string(equipment.StatusAvailable) {
				t.Errorf("first status = %q, want %q", options[0].Value, equipment.StatusAvailable)
			}
			if options[1].Value != string(equipment.StatusMaintenance) {
				t.Errorf(
					"second status = %q, want %q",
					options[1].Value,
					equipment.StatusMaintenance,
				)
			}

			selected := 0
			for _, option := range options {
				if option.Selected {
					selected++
					if option.Value != string(current) {
						t.Errorf("selected status = %q, want %q", option.Value, current)
					}
				}
			}
			if selected != 1 {
				t.Errorf("selected options = %d, want 1", selected)
			}
		})
	}
}

func TestEquipmentEditPageErrors(t *testing.T) {
	const internalErrorText = "database password leaked"
	tests := []struct {
		name       string
		path       string
		item       equipment.Item
		serviceErr error
		wantStatus int
		wantText   string
	}{
		{
			name:       "invalid equipment ID",
			path:       "/equipment/not-a-number/edit",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "equipment not found",
			path:       "/equipment/17/edit",
			serviceErr: equipment.ErrEquipmentNotFound,
			wantStatus: http.StatusNotFound,
		},
		{
			name: "issued equipment is not editable",
			path: "/equipment/17/edit",
			item: equipment.Item{
				ID:     17,
				Status: equipment.StatusIssued,
			},
			wantStatus: http.StatusConflict,
			wantText:   "Редактирование оборудования в текущем состоянии недоступно.",
		},
		{
			name: "retired equipment is not editable",
			path: "/equipment/17/edit",
			item: equipment.Item{
				ID:     17,
				Status: equipment.StatusRetired,
			},
			wantStatus: http.StatusConflict,
			wantText:   "Редактирование оборудования в текущем состоянии недоступно.",
		},
		{
			name:       "internal error is hidden",
			path:       "/equipment/17/edit",
			serviceErr: errors.New(internalErrorText),
			wantStatus: http.StatusInternalServerError,
			wantText:   "Internal Server Error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &equipmentServiceStub{
				get: func(_ context.Context, _ int64) (equipment.Item, error) {
					return tt.item, tt.serviceErr
				},
			}
			request := httptest.NewRequest(http.MethodGet, tt.path, nil)
			response := httptest.NewRecorder()

			newTestHandler(t, discardLogger(), service).ServeHTTP(response, request)

			if response.Code != tt.wantStatus {
				t.Errorf("status code = %d, want %d", response.Code, tt.wantStatus)
			}
			if tt.wantText != "" && !strings.Contains(response.Body.String(), tt.wantText) {
				t.Errorf("body = %q, want it to contain %q", response.Body.String(), tt.wantText)
			}
			if strings.Contains(response.Body.String(), internalErrorText) {
				t.Errorf("body = %q, want internal error hidden", response.Body.String())
			}
		})
	}
}

func TestUpdateEquipmentRedirectsToList(t *testing.T) {
	var gotID int64
	var gotInput equipment.UpdateInput
	service := &equipmentServiceStub{
		update: func(
			_ context.Context,
			id int64,
			input equipment.UpdateInput,
		) (equipment.Item, error) {
			gotID = id
			gotInput = input
			return equipment.Item{
				ID:              id,
				InventoryNumber: "SUP-017-UPDATED",
				Kind:            equipment.KindLifeJacket,
				Status:          equipment.StatusMaintenance,
			}, nil
		},
	}
	form := url.Values{
		"inventory_number": {"SUP-017-UPDATED"},
		"kind":             {string(equipment.KindLifeJacket)},
		"status":           {string(equipment.StatusMaintenance)},
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/equipment/17/edit",
		strings.NewReader(form.Encode()),
	)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()

	newTestHandler(t, discardLogger(), service).ServeHTTP(response, request)

	if response.Code != http.StatusSeeOther {
		t.Errorf("status code = %d, want %d", response.Code, http.StatusSeeOther)
	}
	assertEquipmentNoticeRedirect(
		t,
		response,
		equipmentNoticeUpdated,
		equipment.KindLifeJacket,
		"SUP-017-UPDATED",
	)
	if gotID != 17 {
		t.Errorf("Update() ID = %d, want 17", gotID)
	}
	wantInput := equipment.UpdateInput{
		InventoryNumber: "SUP-017-UPDATED",
		Kind:            equipment.KindLifeJacket,
		Status:          equipment.StatusMaintenance,
	}
	if gotInput != wantInput {
		t.Errorf("Update() input = %+v, want %+v", gotInput, wantInput)
	}
}

func TestUpdateEquipmentErrors(t *testing.T) {
	const internalErrorText = "database password leaked"
	tests := []struct {
		name       string
		path       string
		serviceErr error
		wantStatus int
		wantText   string
		wantField  string
		wantAria   string
	}{
		{
			name:       "invalid equipment ID",
			path:       "/equipment/not-a-number/edit",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "equipment not found",
			path:       "/equipment/17/edit",
			serviceErr: equipment.ErrEquipmentNotFound,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "equipment is not editable",
			path:       "/equipment/17/edit",
			serviceErr: equipment.ErrEquipmentUpdateNotAllowed,
			wantStatus: http.StatusConflict,
			wantText:   "Редактирование оборудования в текущем состоянии недоступно.",
		},
		{
			name:       "empty inventory number",
			path:       "/equipment/17/edit",
			serviceErr: equipment.ErrInventoryNumberRequired,
			wantStatus: http.StatusUnprocessableEntity,
			wantText:   "Введите инвентарный номер.",
			wantField:  `id="inventory-number-error"`,
			wantAria:   `aria-invalid="true" aria-describedby="inventory-number-error"`,
		},
		{
			name:       "invalid kind",
			path:       "/equipment/17/edit",
			serviceErr: equipment.ErrInvalidKind,
			wantStatus: http.StatusUnprocessableEntity,
			wantText:   "Выберите тип оборудования.",
			wantField:  `id="kind-error"`,
			wantAria:   `aria-invalid="true" aria-describedby="kind-error"`,
		},
		{
			name:       "duplicate inventory number",
			path:       "/equipment/17/edit",
			serviceErr: equipment.ErrInventoryNumberExists,
			wantStatus: http.StatusConflict,
			wantText:   "Оборудование с таким инвентарным номером уже существует.",
			wantField:  `id="inventory-number-error"`,
			wantAria:   `aria-invalid="true" aria-describedby="inventory-number-error"`,
		},
		{
			name:       "invalid status",
			path:       "/equipment/17/edit",
			serviceErr: equipment.ErrInvalidStatus,
			wantStatus: http.StatusUnprocessableEntity,
			wantText:   "Выберите допустимый статус оборудования.",
			wantField:  `id="status-error"`,
			wantAria:   `aria-invalid="true" aria-describedby="status-error"`,
		},
		{
			name:       "retirement requires separate confirmation",
			path:       "/equipment/17/edit",
			serviceErr: equipment.ErrStatusTransitionNotAllowed,
			wantStatus: http.StatusUnprocessableEntity,
			wantText:   "Этот переход статуса недоступен в форме редактирования.",
			wantField:  `id="status-error"`,
			wantAria:   `aria-invalid="true" aria-describedby="status-error"`,
		},
		{
			name:       "internal error is hidden",
			path:       "/equipment/17/edit",
			serviceErr: errors.New(internalErrorText),
			wantStatus: http.StatusInternalServerError,
			wantText:   "Internal Server Error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &equipmentServiceStub{
				update: func(
					_ context.Context,
					_ int64,
					_ equipment.UpdateInput,
				) (equipment.Item, error) {
					return equipment.Item{}, tt.serviceErr
				},
			}
			form := url.Values{
				"inventory_number": {"SUP-017"},
				"kind":             {string(equipment.KindSUPBoard)},
				"status":           {string(equipment.StatusAvailable)},
			}
			request := httptest.NewRequest(
				http.MethodPost,
				tt.path,
				strings.NewReader(form.Encode()),
			)
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			response := httptest.NewRecorder()

			newTestHandler(t, discardLogger(), service).ServeHTTP(response, request)

			if response.Code != tt.wantStatus {
				t.Errorf("status code = %d, want %d", response.Code, tt.wantStatus)
			}
			if tt.wantText != "" && !strings.Contains(response.Body.String(), tt.wantText) {
				t.Errorf("body = %q, want it to contain %q", response.Body.String(), tt.wantText)
			}
			if tt.wantField != "" && !strings.Contains(response.Body.String(), tt.wantField) {
				t.Errorf("body = %q, want it to contain %q", response.Body.String(), tt.wantField)
			}
			if tt.wantAria != "" && !strings.Contains(response.Body.String(), tt.wantAria) {
				t.Errorf("body = %q, want it to contain %q", response.Body.String(), tt.wantAria)
			}
			if strings.Contains(response.Body.String(), internalErrorText) {
				t.Errorf("body = %q, want internal error hidden", response.Body.String())
			}
		})
	}
}

func TestUpdateEquipmentRejectsMalformedForm(t *testing.T) {
	request := httptest.NewRequest(
		http.MethodPost,
		"/equipment/17/edit",
		strings.NewReader("inventory_number=%zz"),
	)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()

	newTestHandler(t, discardLogger()).ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Errorf("status code = %d, want %d", response.Code, http.StatusBadRequest)
	}
}

func TestEquipmentStatusEndpointIsNotAvailable(t *testing.T) {
	request := httptest.NewRequest(
		http.MethodPost,
		"/equipment/17/status",
		strings.NewReader("status=maintenance"),
	)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()

	newTestHandler(t, discardLogger()).ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Errorf("status code = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func TestEquipmentPageReturnsInternalErrorWhenListFails(t *testing.T) {
	service := &equipmentServiceStub{
		list: func(_ context.Context) ([]equipment.Item, error) {
			return nil, errors.New("list failed")
		},
	}
	request := httptest.NewRequest(http.MethodGet, "/equipment", nil)
	response := httptest.NewRecorder()

	newTestHandler(t, discardLogger(), service).ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Errorf(
			"status code = %d, want %d",
			response.Code,
			http.StatusInternalServerError,
		)
	}
}

func TestEquipmentPageRejectsUnsupportedMethod(t *testing.T) {
	request := httptest.NewRequest(http.MethodDelete, "/equipment", nil)
	response := httptest.NewRecorder()

	newTestHandler(t, discardLogger()).ServeHTTP(response, request)

	if response.Code != http.StatusMethodNotAllowed {
		t.Errorf("status code = %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}
	if got := response.Header().Get("Allow"); got != "GET, POST" {
		t.Errorf("Allow = %q, want %q", got, "GET, POST")
	}
}

func TestEquipmentSuccessMessageRejectsInvalidQuery(t *testing.T) {
	tests := []struct {
		name  string
		query url.Values
	}{
		{
			name: "unknown notice",
			query: url.Values{
				"notice":           {"unknown"},
				"kind":             {string(equipment.KindSUPBoard)},
				"inventory_number": {"SUP-017"},
			},
		},
		{
			name: "unknown kind",
			query: url.Values{
				"notice":           {equipmentNoticeUpdated},
				"kind":             {"unknown"},
				"inventory_number": {"SUP-017"},
			},
		},
		{
			name: "missing inventory number",
			query: url.Values{
				"notice": {equipmentNoticeUpdated},
				"kind":   {string(equipment.KindSUPBoard)},
			},
		},
		{
			name: "duplicate notice",
			query: url.Values{
				"notice":           {equipmentNoticeUpdated, equipmentNoticeDeleted},
				"kind":             {string(equipment.KindSUPBoard)},
				"inventory_number": {"SUP-017"},
			},
		},
		{
			name: "inventory number with outer spaces",
			query: url.Values{
				"notice":           {equipmentNoticeUpdated},
				"kind":             {string(equipment.KindSUPBoard)},
				"inventory_number": {" SUP-017 "},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := equipmentSuccessMessage(tt.query); got != "" {
				t.Errorf("equipmentSuccessMessage() = %q, want empty message", got)
			}
		})
	}
}

func TestEquipmentSuccessMessageEscapesInventoryNumber(t *testing.T) {
	request := httptest.NewRequest(
		http.MethodGet,
		"/equipment?inventory_number=%3Cscript%3E&kind=paddle&notice=updated",
		nil,
	)
	response := httptest.NewRecorder()

	newTestHandler(t, discardLogger()).ServeHTTP(response, request)

	body := response.Body.String()
	if strings.Contains(body, "<script>") {
		t.Errorf("body = %q, want inventory number HTML-escaped", body)
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Errorf("body = %q, want escaped inventory number", body)
	}
}

func assertEquipmentNoticeRedirect(
	t *testing.T,
	response *httptest.ResponseRecorder,
	wantNotice string,
	wantKind equipment.Kind,
	wantInventoryNumber string,
) {
	t.Helper()

	location := response.Header().Get("Location")
	redirectURL, err := url.Parse(location)
	if err != nil {
		t.Fatalf("parse Location %q: %v", location, err)
	}
	if redirectURL.Path != "/equipment" {
		t.Errorf("Location path = %q, want %q", redirectURL.Path, "/equipment")
	}

	query := redirectURL.Query()
	if len(query) != 3 {
		t.Errorf("Location query = %v, want exactly three parameters", query)
	}
	if got := query.Get("notice"); got != wantNotice {
		t.Errorf("notice = %q, want %q", got, wantNotice)
	}
	if got := query.Get("kind"); got != string(wantKind) {
		t.Errorf("kind = %q, want %q", got, wantKind)
	}
	if got := query.Get("inventory_number"); got != wantInventoryNumber {
		t.Errorf("inventory_number = %q, want %q", got, wantInventoryNumber)
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
