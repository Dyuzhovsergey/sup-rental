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
		"SUP-001",
		"SUP-доска",
		"Доступен",
	} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("body = %q, want it to contain %q", response.Body.String(), want)
		}
	}
}

func TestEquipmentPageShowsOnlyAllowedStatusActions(t *testing.T) {
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
		`action="/equipment/1/status"`,
		`action="/equipment/2/status"`,
		`value="maintenance"`,
		`value="retired"`,
		`value="available"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body = %q, want it to contain %q", body, want)
		}
	}

	for _, unwanted := range []string{
		`action="/equipment/3/status"`,
		`action="/equipment/4/status"`,
	} {
		if strings.Contains(body, unwanted) {
			t.Errorf("body = %q, want it not to contain %q", body, unwanted)
		}
	}
}

func TestEquipmentPageShowsEditLinksOnlyForEditableItems(t *testing.T) {
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
		`href="/equipment/1/edit"`,
		`href="/equipment/2/edit"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body = %q, want it to contain %q", body, want)
		}
	}
	for _, unwanted := range []string{
		`href="/equipment/3/edit"`,
		`href="/equipment/4/edit"`,
	} {
		if strings.Contains(body, unwanted) {
			t.Errorf("body = %q, want it not to contain %q", body, unwanted)
		}
	}
}

func TestEquipmentPageShowsUpdateSuccess(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/equipment?updated=1", nil)
	response := httptest.NewRecorder()

	newTestHandler(t, discardLogger()).ServeHTTP(response, request)

	for _, want := range []string{
		`role="status"`,
		`aria-live="polite"`,
		"Оборудование обновлено.",
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
	}{
		{
			name:       "empty inventory number",
			serviceErr: equipment.ErrInventoryNumberRequired,
			wantStatus: http.StatusUnprocessableEntity,
			wantText:   "Введите инвентарный номер.",
		},
		{
			name:       "invalid kind",
			serviceErr: equipment.ErrInvalidKind,
			wantStatus: http.StatusUnprocessableEntity,
			wantText:   "Выберите тип оборудования.",
		},
		{
			name:       "duplicate inventory number",
			serviceErr: equipment.ErrInventoryNumberExists,
			wantStatus: http.StatusConflict,
			wantText:   "Оборудование с таким инвентарным номером уже существует.",
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
		`for="inventory-number"`,
		`for="kind"`,
		`href="/equipment">Отмена</a>`,
		`:focus-visible`,
	} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("body = %q, want it to contain %q", response.Body.String(), want)
		}
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
			return equipment.Item{ID: id}, nil
		},
	}
	form := url.Values{
		"inventory_number": {"SUP-017-UPDATED"},
		"kind":             {string(equipment.KindLifeJacket)},
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
	if got := response.Header().Get("Location"); got != "/equipment?updated=1" {
		t.Errorf("Location = %q, want %q", got, "/equipment?updated=1")
	}
	if gotID != 17 {
		t.Errorf("Update() ID = %d, want 17", gotID)
	}
	wantInput := equipment.UpdateInput{
		InventoryNumber: "SUP-017-UPDATED",
		Kind:            equipment.KindLifeJacket,
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

func TestChangeEquipmentStatusRedirectsToList(t *testing.T) {
	var gotID int64
	var gotStatus equipment.Status
	service := &equipmentServiceStub{
		changeStatus: func(_ context.Context, id int64, status equipment.Status) (equipment.Item, error) {
			gotID = id
			gotStatus = status
			return equipment.Item{ID: id, Status: status}, nil
		},
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/equipment/17/status",
		strings.NewReader("status=maintenance"),
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
	if gotID != 17 || gotStatus != equipment.StatusMaintenance {
		t.Errorf("ChangeStatus() = (%d, %q), want (17, %q)", gotID, gotStatus, equipment.StatusMaintenance)
	}
}

func TestChangeEquipmentStatusErrors(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		serviceErr error
		wantStatus int
		wantText   string
	}{
		{
			name:       "invalid equipment ID",
			path:       "/equipment/not-a-number/status",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "equipment not found",
			path:       "/equipment/17/status",
			serviceErr: equipment.ErrEquipmentNotFound,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "invalid status",
			path:       "/equipment/17/status",
			serviceErr: equipment.ErrInvalidStatus,
			wantStatus: http.StatusUnprocessableEntity,
			wantText:   "Выберите допустимое состояние оборудования.",
		},
		{
			name:       "disallowed transition",
			path:       "/equipment/17/status",
			serviceErr: equipment.ErrStatusTransitionNotAllowed,
			wantStatus: http.StatusUnprocessableEntity,
			wantText:   "Этот переход состояния сейчас недоступен.",
		},
		{
			name:       "internal error is hidden",
			path:       "/equipment/17/status",
			serviceErr: errors.New("database password leaked"),
			wantStatus: http.StatusInternalServerError,
			wantText:   "Internal Server Error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &equipmentServiceStub{
				changeStatus: func(_ context.Context, _ int64, _ equipment.Status) (equipment.Item, error) {
					return equipment.Item{}, tt.serviceErr
				},
			}
			request := httptest.NewRequest(
				http.MethodPost,
				tt.path,
				strings.NewReader("status=maintenance"),
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
			if strings.Contains(response.Body.String(), "database password leaked") {
				t.Errorf("body = %q, want internal error hidden", response.Body.String())
			}
		})
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

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
