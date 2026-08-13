package httpserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Dyuzhovsergey/sup-rental/internal/equipment"
)

func TestEquipmentDeletionPageShowsWarningAndItem(t *testing.T) {
	service := &equipmentServiceStub{
		get: func(_ context.Context, id int64) (equipment.Item, error) {
			return equipment.Item{
				ID:                id,
				InventoryNumber:   "SUP-FUSION-1",
				Kind:              equipment.KindSUPBoard,
				ModelCode:         "FUSION",
				HourlyRateKopecks: 50000,
				Status:            equipment.StatusRetired,
			}, nil
		},
	}
	request := httptest.NewRequest(http.MethodGet, "/equipment/17/delete", nil)
	response := httptest.NewRecorder()

	newTestHandler(t, discardLogger(), service).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Errorf("status code = %d, want %d", response.Code, http.StatusOK)
	}
	for _, want := range []string{
		`role="alert"`,
		"Удалить оборудование SUP-FUSION-1?",
		"Удаление нельзя отменить.",
		"безвозвратно удалено из базы данных",
		"SUP-доска",
		"FUSION",
		"500 ₽/час",
		"Списан",
		`action="/equipment/17/delete"`,
		`name="csrf_token" value="csrf-token"`,
		"Подтвердить удаление",
		`href="/equipment/17">Отмена</a>`,
		`<link rel="stylesheet" href="/static/app.css">`,
	} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("body = %q, want it to contain %q", response.Body.String(), want)
		}
	}
}

func TestEquipmentDeletionPageRequiresRetiredStatus(t *testing.T) {
	service := &equipmentServiceStub{
		get: func(_ context.Context, id int64) (equipment.Item, error) {
			return equipment.Item{ID: id, Status: equipment.StatusAvailable}, nil
		},
	}
	request := httptest.NewRequest(http.MethodGet, "/equipment/17/delete", nil)
	response := httptest.NewRecorder()

	newTestHandler(t, discardLogger(), service).ServeHTTP(response, request)

	if response.Code != http.StatusConflict {
		t.Errorf("status code = %d, want %d", response.Code, http.StatusConflict)
	}
	if !strings.Contains(response.Body.String(), deletionUnavailableMessage) {
		t.Errorf("body = %q, want deletion unavailable message", response.Body.String())
	}
	for _, want := range []string{
		"Удаление оборудования недоступно",
		"Текущий статус не позволяет удалить этот предмет.",
		`href="/equipment/17"`,
	} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("body = %q, want it to contain %q", response.Body.String(), want)
		}
	}
	for _, unwanted := range []string{
		`action="/equipment/17/delete"`,
		"Подтвердить удаление",
	} {
		if strings.Contains(response.Body.String(), unwanted) {
			t.Errorf("body = %q, want it not to contain %q", response.Body.String(), unwanted)
		}
	}
}

func TestEquipmentDeletionPageErrors(t *testing.T) {
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
			path:       "/equipment/not-a-number/delete",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "equipment not found",
			path:       "/equipment/17/delete",
			serviceErr: equipment.ErrEquipmentNotFound,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "internal error is hidden",
			path:       "/equipment/17/delete",
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

func TestDeleteEquipmentRedirectsToList(t *testing.T) {
	var gotID int64
	service := &equipmentServiceStub{
		delete: func(_ context.Context, id int64) (equipment.Item, error) {
			gotID = id
			return equipment.Item{
				ID:              id,
				InventoryNumber: "SUP-017",
				Kind:            equipment.KindSUPBoard,
				Status:          equipment.StatusRetired,
			}, nil
		},
	}
	request := httptest.NewRequest(http.MethodPost, "/equipment/17/delete", nil)
	response := httptest.NewRecorder()

	newTestHandler(t, discardLogger(), service).ServeHTTP(response, request)

	if response.Code != http.StatusSeeOther {
		t.Errorf("status code = %d, want %d", response.Code, http.StatusSeeOther)
	}
	assertEquipmentNoticeRedirect(
		t,
		response,
		equipmentNoticeDeleted,
		equipment.KindSUPBoard,
		"SUP-017",
	)
	if gotID != 17 {
		t.Errorf("Delete() ID = %d, want 17", gotID)
	}
}

func TestDeleteEquipmentErrors(t *testing.T) {
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
			path:       "/equipment/not-a-number/delete",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "equipment not found",
			path:       "/equipment/17/delete",
			serviceErr: equipment.ErrEquipmentNotFound,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "equipment is not retired",
			path:       "/equipment/17/delete",
			serviceErr: equipment.ErrEquipmentDeleteNotAllowed,
			wantStatus: http.StatusConflict,
			wantText:   deletionUnavailableMessage,
		},
		{
			name:       "equipment has history",
			path:       "/equipment/17/delete",
			serviceErr: equipment.ErrEquipmentHasHistory,
			wantStatus: http.StatusConflict,
			wantText:   "Оборудование связано с историей операций",
		},
		{
			name:       "internal error is hidden",
			path:       "/equipment/17/delete",
			serviceErr: errors.New(internalErrorText),
			wantStatus: http.StatusInternalServerError,
			wantText:   "Internal Server Error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &equipmentServiceStub{
				delete: func(_ context.Context, _ int64) (equipment.Item, error) {
					return equipment.Item{}, tt.serviceErr
				},
			}
			request := httptest.NewRequest(http.MethodPost, tt.path, nil)
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

func TestEquipmentDeletionRejectsUnsupportedMethod(t *testing.T) {
	request := httptest.NewRequest(http.MethodDelete, "/equipment/17/delete", nil)
	response := httptest.NewRecorder()

	newTestHandler(t, discardLogger()).ServeHTTP(response, request)

	if response.Code != http.StatusMethodNotAllowed {
		t.Errorf("status code = %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}
	if got := response.Header().Get("Allow"); got != "GET, HEAD, POST" {
		t.Errorf("Allow = %q, want %q", got, "GET, HEAD, POST")
	}
}
