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

func TestEquipmentRetirementPageShowsWarningAndItem(t *testing.T) {
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
	request := httptest.NewRequest(http.MethodGet, "/equipment/17/retire", nil)
	response := httptest.NewRecorder()

	newTestHandler(t, discardLogger(), service).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Errorf("status code = %d, want %d", response.Code, http.StatusOK)
	}
	if gotID != 17 {
		t.Errorf("Get() ID = %d, want 17", gotID)
	}
	for _, want := range []string{
		`role="alert"`,
		"Списать оборудование SUP-017?",
		"Списание нельзя отменить.",
		"После списания оборудование нельзя редактировать",
		"SUP-доска",
		"Доступен",
		`action="/equipment/17/retire"`,
		`name="csrf_token" value="csrf-token"`,
		"Подтвердить списание",
		`href="/equipment/17/edit">Отмена</a>`,
		`<link rel="stylesheet" href="/static/app.css">`,
	} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("body = %q, want it to contain %q", response.Body.String(), want)
		}
	}
}

func TestEquipmentRetirementPageErrors(t *testing.T) {
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
			path:       "/equipment/not-a-number/retire",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "equipment not found",
			path:       "/equipment/17/retire",
			serviceErr: equipment.ErrEquipmentNotFound,
			wantStatus: http.StatusNotFound,
		},
		{
			name: "issued equipment",
			path: "/equipment/17/retire",
			item: equipment.Item{
				ID:     17,
				Status: equipment.StatusIssued,
			},
			wantStatus: http.StatusConflict,
			wantText:   retirementUnavailableMessage,
		},
		{
			name: "already retired equipment",
			path: "/equipment/17/retire",
			item: equipment.Item{
				ID:     17,
				Status: equipment.StatusRetired,
			},
			wantStatus: http.StatusConflict,
			wantText:   retirementUnavailableMessage,
		},
		{
			name:       "internal error is hidden",
			path:       "/equipment/17/retire",
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

func TestRetireEquipmentRedirectsToList(t *testing.T) {
	var gotID int64
	var gotStatus equipment.Status
	service := &equipmentServiceStub{
		changeStatus: func(
			_ context.Context,
			id int64,
			status equipment.Status,
		) (equipment.Item, error) {
			gotID = id
			gotStatus = status
			return equipment.Item{
				ID:              id,
				InventoryNumber: "SUP-017",
				Kind:            equipment.KindSUPBoard,
				Status:          status,
			}, nil
		},
	}
	request := httptest.NewRequest(http.MethodPost, "/equipment/17/retire", nil)
	response := httptest.NewRecorder()

	newTestHandler(t, discardLogger(), service).ServeHTTP(response, request)

	if response.Code != http.StatusSeeOther {
		t.Errorf("status code = %d, want %d", response.Code, http.StatusSeeOther)
	}
	assertEquipmentNoticeRedirect(
		t,
		response,
		equipmentNoticeRetired,
		equipment.KindSUPBoard,
		"SUP-017",
	)
	if gotID != 17 || gotStatus != equipment.StatusRetired {
		t.Errorf(
			"ChangeStatus() = (%d, %q), want (17, %q)",
			gotID,
			gotStatus,
			equipment.StatusRetired,
		)
	}
}

func TestRetireEquipmentErrors(t *testing.T) {
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
			path:       "/equipment/not-a-number/retire",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "equipment not found",
			path:       "/equipment/17/retire",
			serviceErr: equipment.ErrEquipmentNotFound,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "transition is no longer allowed",
			path:       "/equipment/17/retire",
			serviceErr: equipment.ErrStatusTransitionNotAllowed,
			wantStatus: http.StatusConflict,
			wantText:   retirementUnavailableMessage,
		},
		{
			name:       "internal error is hidden",
			path:       "/equipment/17/retire",
			serviceErr: errors.New(internalErrorText),
			wantStatus: http.StatusInternalServerError,
			wantText:   "Internal Server Error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &equipmentServiceStub{
				changeStatus: func(
					_ context.Context,
					_ int64,
					_ equipment.Status,
				) (equipment.Item, error) {
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

func TestEquipmentRetirementRejectsUnsupportedMethod(t *testing.T) {
	request := httptest.NewRequest(http.MethodDelete, "/equipment/17/retire", nil)
	response := httptest.NewRecorder()

	newTestHandler(t, discardLogger()).ServeHTTP(response, request)

	if response.Code != http.StatusMethodNotAllowed {
		t.Errorf("status code = %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}
	if got := response.Header().Get("Allow"); got != "GET, HEAD, POST" {
		t.Errorf("Allow = %q, want %q", got, "GET, HEAD, POST")
	}
}
