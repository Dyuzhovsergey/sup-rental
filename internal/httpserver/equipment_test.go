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

func TestEquipmentPageShowsBatchFormAndModelData(t *testing.T) {
	service := &equipmentServiceStub{list: func(context.Context) ([]equipment.Item, error) {
		return []equipment.Item{equipmentHTTPFixture(equipment.StatusAvailable)}, nil
	}}
	response := httptest.NewRecorder()
	newTestHandler(t, discardLogger(), service).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/equipment", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	for _, want := range []string{
		`name="kind"`, `name="model_code"`, `name="hourly_rate_rubles"`, `name="quantity"`,
		`name="csrf_token" value="csrf-token"`, "Модель", "Тариф", "CARBON", "350 ₽/час",
		`PADDLE-CARBON-1`, "Весло", "Доступен", `href="/equipment/17/edit"`,
	} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("body does not contain %q", want)
		}
	}
	if strings.Contains(response.Body.String(), `name="inventory_number"`) {
		t.Error("batch form still contains manual inventory number")
	}
}

func TestCreateEquipmentBatchRedirectsWithCreatedRange(t *testing.T) {
	var gotInput equipment.BatchCreateInput
	service := &equipmentServiceStub{create: func(_ context.Context, input equipment.BatchCreateInput) (equipment.Batch, error) {
		gotInput = input
		return equipment.Batch{
			Items:                []equipment.Item{{ID: 17}, {ID: 18}, {ID: 19}},
			FirstInventoryNumber: "PADDLE-CARBON-1",
			LastInventoryNumber:  "PADDLE-CARBON-3",
		}, nil
	}}
	form := url.Values{
		"kind": {"paddle"}, "model_code": {"carbon"},
		"hourly_rate_rubles": {"350"}, "quantity": {"3"},
	}
	request := httptest.NewRequest(http.MethodPost, "/equipment", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	newTestHandler(t, discardLogger(), service).ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", response.Code)
	}
	location, err := url.Parse(response.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if location.Path != "/equipment" || location.Query().Get("notice") != equipmentNoticeBatchCreated ||
		location.Query().Get("first") != "PADDLE-CARBON-1" || location.Query().Get("last") != "PADDLE-CARBON-3" {
		t.Errorf("Location = %q", location.String())
	}
	want := equipment.BatchCreateInput{Kind: equipment.KindPaddle, ModelCode: "carbon", HourlyRateRubles: 350, Quantity: 3}
	if gotInput != want {
		t.Errorf("input = %#v, want %#v", gotInput, want)
	}
}

func TestCreateEquipmentBatchValidation(t *testing.T) {
	tests := []struct {
		name       string
		form       url.Values
		serviceErr error
		wantStatus int
		wantText   string
		wantAria   string
	}{
		{name: "kind", form: validBatchForm(), serviceErr: equipment.ErrInvalidKind, wantStatus: 422, wantText: "Выберите тип оборудования.", wantAria: `aria-describedby="create-kind-error"`},
		{name: "model required", form: validBatchForm(), serviceErr: equipment.ErrModelCodeRequired, wantStatus: 422, wantText: "Введите код модели", wantAria: `aria-describedby="create-model-code-error"`},
		{name: "model invalid", form: validBatchForm(), serviceErr: equipment.ErrInvalidModelCode, wantStatus: 422, wantText: "Используйте только латинские", wantAria: `aria-describedby="create-model-code-error"`},
		{name: "rate value", form: url.Values{"kind": {"paddle"}, "model_code": {"CARBON"}, "hourly_rate_rubles": {"3.5"}, "quantity": {"1"}}, wantStatus: 422, wantText: "положительное целое число рублей", wantAria: `aria-describedby="create-hourly-rate-error"`},
		{name: "quantity value", form: url.Values{"kind": {"paddle"}, "model_code": {"CARBON"}, "hourly_rate_rubles": {"350"}, "quantity": {"many"}}, wantStatus: 422, wantText: "целое количество от 1 до 100", wantAria: `aria-describedby="create-quantity-error"`},
		{name: "quantity range", form: validBatchForm(), serviceErr: equipment.ErrInvalidBatchQuantity, wantStatus: 422, wantText: "от 1 до 100", wantAria: `aria-describedby="create-quantity-error"`},
		{name: "rate conflict", form: validBatchForm(), serviceErr: equipment.ErrModelRateConflict, wantStatus: 409, wantText: "уже существует с другим часовым тарифом", wantAria: `aria-describedby="create-hourly-rate-error"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &equipmentServiceStub{create: func(context.Context, equipment.BatchCreateInput) (equipment.Batch, error) {
				return equipment.Batch{}, tt.serviceErr
			}}
			request := httptest.NewRequest(http.MethodPost, "/equipment", strings.NewReader(tt.form.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			response := httptest.NewRecorder()
			newTestHandler(t, discardLogger(), service).ServeHTTP(response, request)
			if response.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", response.Code, tt.wantStatus)
			}
			for _, want := range []string{tt.wantText, tt.wantAria, `value="CARBON"`} {
				if !strings.Contains(response.Body.String(), want) {
					t.Errorf("body does not contain %q", want)
				}
			}
		})
	}
}

func TestCreateEquipmentBatchHidesInternalError(t *testing.T) {
	const secret = "database password leaked"
	service := &equipmentServiceStub{create: func(context.Context, equipment.BatchCreateInput) (equipment.Batch, error) {
		return equipment.Batch{}, errors.New(secret)
	}}
	request := httptest.NewRequest(http.MethodPost, "/equipment", strings.NewReader(validBatchForm().Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	newTestHandler(t, discardLogger(), service).ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), secret) {
		t.Errorf("status = %d body = %q", response.Code, response.Body.String())
	}
}

func TestEquipmentDetailShowsModelAndRate(t *testing.T) {
	service := &equipmentServiceStub{get: func(context.Context, int64) (equipment.Item, error) {
		return equipmentHTTPFixture(equipment.StatusAvailable), nil
	}}
	response := httptest.NewRecorder()
	newTestHandler(t, discardLogger(), service).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/equipment/17", nil))
	for _, want := range []string{"PADDLE-CARBON-1", "Весло", "CARBON", "350 ₽/час", "Доступен", "17"} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("body does not contain %q", want)
		}
	}
}

func TestEquipmentDetailErrorsAndMethod(t *testing.T) {
	for _, tt := range []struct {
		name       string
		path       string
		method     string
		serviceErr error
		wantStatus int
	}{
		{name: "invalid ID", path: "/equipment/no", method: http.MethodGet, wantStatus: http.StatusNotFound},
		{name: "not found", path: "/equipment/17", method: http.MethodGet, serviceErr: equipment.ErrEquipmentNotFound, wantStatus: http.StatusNotFound},
		{name: "internal", path: "/equipment/17", method: http.MethodGet, serviceErr: errors.New("database secret"), wantStatus: http.StatusInternalServerError},
		{name: "method", path: "/equipment/17", method: http.MethodPost, wantStatus: http.StatusMethodNotAllowed},
	} {
		t.Run(tt.name, func(t *testing.T) {
			service := &equipmentServiceStub{get: func(context.Context, int64) (equipment.Item, error) {
				return equipment.Item{}, tt.serviceErr
			}}
			response := httptest.NewRecorder()
			newTestHandler(t, discardLogger(), service).ServeHTTP(response, httptest.NewRequest(tt.method, tt.path, nil))
			if response.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", response.Code, tt.wantStatus)
			}
			if strings.Contains(response.Body.String(), "database secret") {
				t.Error("internal error leaked")
			}
		})
	}
}

func TestEquipmentEditValidationAndMalformedForm(t *testing.T) {
	item := equipmentHTTPFixture(equipment.StatusAvailable)
	service := &equipmentServiceStub{
		get: func(context.Context, int64) (equipment.Item, error) { return item, nil },
		update: func(context.Context, int64, equipment.UpdateInput) (equipment.Item, error) {
			return equipment.Item{}, equipment.ErrInvalidStatus
		},
	}
	request := httptest.NewRequest(http.MethodPost, "/equipment/17/edit", strings.NewReader("status=broken"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	newTestHandler(t, discardLogger(), service).ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), `aria-describedby="status-error"`) {
		t.Errorf("status = %d body = %q", response.Code, response.Body.String())
	}

	malformed := httptest.NewRequest(http.MethodPost, "/equipment/17/edit", strings.NewReader("status=%zz"))
	malformed.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	malformedResponse := httptest.NewRecorder()
	newTestHandler(t, discardLogger(), service).ServeHTTP(malformedResponse, malformed)
	if malformedResponse.Code != http.StatusBadRequest {
		t.Errorf("malformed status = %d", malformedResponse.Code)
	}
}

func TestEquipmentEditShowsSeparateStatusModelAndRateForms(t *testing.T) {
	item := equipmentHTTPFixture(equipment.StatusAvailable)
	var gotInput equipment.UpdateInput
	service := &equipmentServiceStub{
		get: func(context.Context, int64) (equipment.Item, error) { return item, nil },
		update: func(_ context.Context, _ int64, input equipment.UpdateInput) (equipment.Item, error) {
			gotInput = input
			item.Status = input.Status
			return item, nil
		},
	}
	getResponse := httptest.NewRecorder()
	newTestHandler(t, discardLogger(), service).ServeHTTP(getResponse, httptest.NewRequest(http.MethodGet, "/equipment/17/edit", nil))
	for _, want := range []string{
		"PADDLE-CARBON-1", "CARBON", "350 ₽/час", `name="status"`,
		`class="equipment-edit-top"`, "Списание оборудования",
		`action="/equipment/17/model"`, `name="kind"`, `name="model_code"`,
		`action="/equipment/17/rate"`, `name="hourly_rate_rubles"`,
		"Новый тариф будет применён ко всем единицам модели CARBON.",
	} {
		if !strings.Contains(getResponse.Body.String(), want) {
			t.Errorf("GET body does not contain %q", want)
		}
	}
	body := getResponse.Body.String()
	statusPosition := strings.Index(body, `id="status-heading"`)
	retirePosition := strings.Index(body, `id="retire-equipment-heading"`)
	ratePosition := strings.Index(body, `id="rate-heading"`)
	modelPosition := strings.Index(body, `id="model-heading"`)
	if statusPosition < 0 || retirePosition < statusPosition || ratePosition < retirePosition || modelPosition < ratePosition {
		t.Errorf("panel order status=%d retire=%d rate=%d model=%d", statusPosition, retirePosition, ratePosition, modelPosition)
	}
	for _, unwanted := range []string{`name="inventory_number"`} {
		if strings.Contains(getResponse.Body.String(), unwanted) {
			t.Errorf("GET body contains editable field %q", unwanted)
		}
	}

	request := httptest.NewRequest(http.MethodPost, "/equipment/17/edit", strings.NewReader("status=maintenance"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postResponse := httptest.NewRecorder()
	newTestHandler(t, discardLogger(), service).ServeHTTP(postResponse, request)
	if postResponse.Code != http.StatusSeeOther || gotInput.Status != equipment.StatusMaintenance {
		t.Errorf("POST status = %d input = %#v", postResponse.Code, gotInput)
	}
}

func TestEquipmentModelChangeRedirectsAndReceivesInput(t *testing.T) {
	item := equipmentHTTPFixture(equipment.StatusAvailable)
	var got equipment.ModelChangeInput
	service := &equipmentServiceStub{
		get: func(context.Context, int64) (equipment.Item, error) { return item, nil },
		changeModel: func(_ context.Context, _ int64, input equipment.ModelChangeInput) (equipment.Item, error) {
			got = input
			item.InventoryNumber = "VEST-TOURING-1"
			return item, nil
		},
	}
	form := url.Values{"kind": {"life_jacket"}, "model_code": {"touring"}, "hourly_rate_rubles": {"250"}}
	request := httptest.NewRequest(http.MethodPost, "/equipment/17/model", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	newTestHandler(t, discardLogger(), service).ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/equipment/17/edit?notice=model_changed" {
		t.Fatalf("response = %d Location %q", response.Code, response.Header().Get("Location"))
	}
	want := equipment.ModelChangeInput{Kind: equipment.KindLifeJacket, ModelCode: "touring", HourlyRateRubles: 250}
	if got != want {
		t.Errorf("input = %#v, want %#v", got, want)
	}
}

func TestEquipmentModelChangeValidation(t *testing.T) {
	item := equipmentHTTPFixture(equipment.StatusAvailable)
	for _, tt := range []struct {
		name       string
		form       url.Values
		serviceErr error
		wantStatus int
		wantText   string
		wantAria   string
	}{
		{name: "invalid kind", form: validModelForm(), serviceErr: equipment.ErrInvalidKind, wantStatus: 422, wantText: "Выберите тип оборудования.", wantAria: `aria-describedby="model-kind-error"`},
		{name: "invalid model", form: validModelForm(), serviceErr: equipment.ErrInvalidModelCode, wantStatus: 422, wantText: "Используйте только латинские", wantAria: `aria-describedby="model-code-error"`},
		{name: "invalid rate", form: url.Values{"kind": {"paddle"}, "model_code": {"TOURING"}, "hourly_rate_rubles": {"3.5"}}, wantStatus: 422, wantText: "положительное целое число рублей", wantAria: `aria-describedby="model-rate-error"`},
		{name: "rate conflict", form: validModelForm(), serviceErr: equipment.ErrModelRateConflict, wantStatus: 409, wantText: "У существующей модели другой тариф", wantAria: `aria-describedby="model-rate-error"`},
		{name: "unchanged", form: validModelForm(), serviceErr: equipment.ErrEquipmentModelUnchanged, wantStatus: 422, wantText: "Выберите другую модель", wantAria: `aria-describedby="model-code-error"`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			service := &equipmentServiceStub{
				get: func(context.Context, int64) (equipment.Item, error) { return item, nil },
				changeModel: func(context.Context, int64, equipment.ModelChangeInput) (equipment.Item, error) {
					return equipment.Item{}, tt.serviceErr
				},
			}
			request := httptest.NewRequest(http.MethodPost, "/equipment/17/model", strings.NewReader(tt.form.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			response := httptest.NewRecorder()
			newTestHandler(t, discardLogger(), service).ServeHTTP(response, request)
			if response.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", response.Code, tt.wantStatus)
			}
			for _, want := range []string{tt.wantText, tt.wantAria} {
				if !strings.Contains(response.Body.String(), want) {
					t.Errorf("body does not contain %q", want)
				}
			}
		})
	}
}

func TestEquipmentModelRateChangeRedirectsAndValidates(t *testing.T) {
	item := equipmentHTTPFixture(equipment.StatusMaintenance)
	var gotRate int64
	service := &equipmentServiceStub{
		get: func(context.Context, int64) (equipment.Item, error) { return item, nil },
		changeRate: func(_ context.Context, _ int64, rate int64) (equipment.ModelRateChange, error) {
			gotRate = rate
			return equipment.ModelRateChange{Item: item, AffectedItems: 4}, nil
		},
	}
	request := httptest.NewRequest(http.MethodPost, "/equipment/17/rate", strings.NewReader("hourly_rate_rubles=425"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	newTestHandler(t, discardLogger(), service).ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/equipment/17/edit?affected=4&notice=rate_changed" || gotRate != 425 {
		t.Fatalf("response = %d Location %q rate %d", response.Code, response.Header().Get("Location"), gotRate)
	}

	service.changeRate = func(context.Context, int64, int64) (equipment.ModelRateChange, error) {
		return equipment.ModelRateChange{}, equipment.ErrModelRateUnchanged
	}
	errorResponse := httptest.NewRecorder()
	newTestHandler(t, discardLogger(), service).ServeHTTP(errorResponse,
		httptest.NewRequest(http.MethodPost, "/equipment/17/rate", strings.NewReader("hourly_rate_rubles=350")))
	if errorResponse.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(errorResponse.Body.String(), `aria-describedby="rate-error"`) {
		t.Errorf("status = %d body = %q", errorResponse.Code, errorResponse.Body.String())
	}
}

func TestEquipmentModelMutationsHideInternalErrors(t *testing.T) {
	const secret = "database connection string"
	item := equipmentHTTPFixture(equipment.StatusAvailable)
	service := &equipmentServiceStub{
		get: func(context.Context, int64) (equipment.Item, error) { return item, nil },
		changeModel: func(context.Context, int64, equipment.ModelChangeInput) (equipment.Item, error) {
			return equipment.Item{}, errors.New(secret)
		},
		changeRate: func(context.Context, int64, int64) (equipment.ModelRateChange, error) {
			return equipment.ModelRateChange{}, errors.New(secret)
		},
	}
	for path, body := range map[string]string{
		"/equipment/17/model": validModelForm().Encode(),
		"/equipment/17/rate":  "hourly_rate_rubles=425",
	} {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		response := httptest.NewRecorder()
		newTestHandler(t, discardLogger(), service).ServeHTTP(response, request)
		if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), secret) {
			t.Errorf("%s status = %d body = %q", path, response.Code, response.Body.String())
		}
	}
}

func TestEquipmentEditSuccessMessages(t *testing.T) {
	item := equipmentHTTPFixture(equipment.StatusAvailable)
	if got := equipmentEditSuccessMessage(url.Values{"notice": {"model_changed"}}, item); got != "Модель оборудования изменена. Новый инвентарный номер: PADDLE-CARBON-1." {
		t.Errorf("model message = %q", got)
	}
	if got := equipmentEditSuccessMessage(url.Values{"notice": {"rate_changed"}, "affected": {"3"}}, item); got != "Тариф модели CARBON обновлён. Затронуто: 3 единицы." {
		t.Errorf("rate message = %q", got)
	}
}

func TestEquipmentBatchSuccessMessage(t *testing.T) {
	query := url.Values{"notice": {equipmentNoticeBatchCreated}, "count": {"3"}, "first": {"PADDLE-CARBON-1"}, "last": {"PADDLE-CARBON-3"}}
	got := equipmentSuccessMessage(query)
	if got != "Добавлено 3 единицы оборудования: PADDLE-CARBON-1 — PADDLE-CARBON-3." {
		t.Errorf("message = %q", got)
	}
}

func TestEquipmentPageRejectsInvalidPaginationAndMethod(t *testing.T) {
	for _, target := range []string{"/equipment?page_size=7", "/equipment?active_page=0", "/equipment?retired_page=no"} {
		response := httptest.NewRecorder()
		newTestHandler(t, discardLogger()).ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		if response.Code != http.StatusNotFound {
			t.Errorf("%s status = %d", target, response.Code)
		}
	}
	response := httptest.NewRecorder()
	newTestHandler(t, discardLogger()).ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/equipment", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Errorf("PUT status = %d", response.Code)
	}
}

func TestEquipmentCountLabel(t *testing.T) {
	for count, want := range map[int]string{1: "1 позиция", 2: "2 позиции", 5: "5 позиций", 11: "11 позиций", 21: "21 позиция"} {
		if got := equipmentCountLabel(count); got != want {
			t.Errorf("%d: %q, want %q", count, got, want)
		}
	}
}

func validBatchForm() url.Values {
	return url.Values{"kind": {"paddle"}, "model_code": {"CARBON"}, "hourly_rate_rubles": {"350"}, "quantity": {"3"}}
}

func validModelForm() url.Values {
	return url.Values{"kind": {"life_jacket"}, "model_code": {"TOURING"}, "hourly_rate_rubles": {"250"}}
}

func equipmentHTTPFixture(status equipment.Status) equipment.Item {
	return equipment.Item{ID: 17, InventoryNumber: "PADDLE-CARBON-1", ModelID: 2, ModelCode: "CARBON", SequenceNumber: 1, Kind: equipment.KindPaddle, HourlyRateKopecks: 35000, Status: status}
}

func assertEquipmentNoticeRedirect(t *testing.T, response *httptest.ResponseRecorder, notice string, kind equipment.Kind, inventoryNumber string) {
	t.Helper()
	location, err := url.Parse(response.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	if location.Path != "/equipment" || location.Query().Get("notice") != notice ||
		location.Query().Get("kind") != string(kind) || location.Query().Get("inventory_number") != inventoryNumber {
		t.Errorf("Location = %q", location.String())
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
