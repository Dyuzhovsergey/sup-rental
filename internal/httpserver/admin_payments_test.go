package httpserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Dyuzhovsergey/sup-rental/internal/dashboard"
	"github.com/Dyuzhovsergey/sup-rental/internal/rental"
	"github.com/Dyuzhovsergey/sup-rental/internal/user"
)

func TestAdminPaymentsPageShowsTodayOperationsAndNavigation(t *testing.T) {
	service := &adminDashboardServiceStub{payments: func(_ context.Context, period dashboard.FinancialPeriod, page, pageSize int) (dashboard.PaymentOperationsPage, error) {
		if page != 1 || pageSize != 5 {
			t.Fatalf("pagination = %d, %d", page, pageSize)
		}
		if period.Start.Format(time.RFC3339) != "2026-08-25T00:00:00+03:00" ||
			period.End.Format(time.RFC3339) != "2026-08-28T00:00:00+03:00" {
			t.Fatalf("period = %+v", period)
		}
		return dashboard.PaymentOperationsPage{
			Total: 3, Page: page, PageSize: pageSize,
			Operations: []dashboard.PaymentOperation{
				{ID: 3, RentalID: 42, ClientName: "Анна Смирнова", Kind: rental.PaymentKindRefund, AmountKopecks: 150_000, OccurredAt: time.Date(2026, 8, 27, 7, 30, 0, 0, time.UTC), ActorLogin: "operator.one"},
				{ID: 2, RentalID: 41, ClientName: "Иван Петров", Kind: rental.PaymentKindOverdue, AmountKopecks: 30_000, OccurredAt: time.Date(2026, 8, 27, 7, 0, 0, 0, time.UTC), ActorLogin: "operator.two"},
				{ID: 1, RentalID: 40, ClientName: "Ольга Соколова", Kind: rental.PaymentKindBase, AmountKopecks: 200_000, OccurredAt: time.Date(2026, 8, 27, 6, 30, 0, 0, time.UTC), ActorLogin: "operator.one"},
			},
		}, nil
	}}
	response := httptest.NewRecorder()
	newAdminDashboardTestHandler(t, service, user.RoleAdmin).ServeHTTP(
		response, authenticatedRequest(http.MethodGet, "/admin/payments?period=custom&from=2026-08-25&to=2026-08-27&page_size=5", ""),
	)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body %q", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, want := range []string{
		"Платёжные операции", "Операции за 25.08.2026–27.08.2026", "3 операции",
		"27.08.2026 10:30", "Основная оплата", "Доплата за просрочку", "Возврат",
		"Анна Смирнова", "operator.one", "−1 500 ₽", "&#43;300 ₽", "&#43;2 000 ₽",
		`href="/rentals/42"`, `data-row-href="/rentals/42"`, `tabindex="0"`,
		`href="/admin/payments" aria-current="page"`, "Страница 1 из 1",
		`class="payment-operations-table responsive-data-table"`,
		`href="/admin?from=2026-08-25&amp;period=custom&amp;to=2026-08-27"`,
		`name="from" type="date" value="2026-08-25"`,
		`<fieldset class="finance-period-filter__range">`, "Произвольный период",
		`<label class="visually-hidden" for="finance-period-from">Дата начала</label>`,
		`<label class="visually-hidden" for="finance-period-to">Дата окончания</label>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q", want)
		}
	}
}

func TestAdminPaymentsPageShowsEmptyState(t *testing.T) {
	response := httptest.NewRecorder()
	newAdminDashboardTestHandler(t, &adminDashboardServiceStub{}, user.RoleAdmin).ServeHTTP(
		response, authenticatedRequest(http.MethodGet, "/admin/payments", ""),
	)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Платёжных операций за сегодня нет") {
		t.Fatalf("status = %d body %q", response.Code, response.Body.String())
	}
}

func TestAdminPaymentsPageBuildsPaginationLinks(t *testing.T) {
	service := &adminDashboardServiceStub{payments: func(_ context.Context, _ dashboard.FinancialPeriod, page, pageSize int) (dashboard.PaymentOperationsPage, error) {
		return dashboard.PaymentOperationsPage{
			Total: 6, Page: page, PageSize: pageSize,
			Operations: []dashboard.PaymentOperation{{
				ID: 1, RentalID: 40, ClientName: "Ольга Соколова", Kind: rental.PaymentKindBase,
				AmountKopecks: 200_000, OccurredAt: time.Date(2026, 8, 27, 6, 30, 0, 0, time.UTC), ActorLogin: "operator.one",
			}},
		}, nil
	}}
	response := httptest.NewRecorder()
	newAdminDashboardTestHandler(t, service, user.RoleAdmin).ServeHTTP(
		response, authenticatedRequest(http.MethodGet, "/admin/payments?period=7d&page_size=5", ""),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body %q", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, "Страница 1 из 2") || !strings.Contains(body, `href="/admin/payments?page=2&amp;page_size=5&amp;period=7d"`) {
		t.Fatalf("body does not contain pagination: %q", body)
	}
}

func TestAdminPaymentsPageValidatesPaginationAndMissingPage(t *testing.T) {
	handler := newAdminDashboardTestHandler(t, &adminDashboardServiceStub{payments: func(_ context.Context, _ dashboard.FinancialPeriod, page, pageSize int) (dashboard.PaymentOperationsPage, error) {
		return dashboard.PaymentOperationsPage{Total: 6, Page: page, PageSize: pageSize}, nil
	}}, user.RoleAdmin)
	for _, target := range []string{
		"/admin/payments?page=0", "/admin/payments?page=x", "/admin/payments?page_size=7",
		"/admin/payments?page=2&page_size=5",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, authenticatedRequest(http.MethodGet, target, ""))
		if response.Code != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want %d", target, response.Code, http.StatusNotFound)
		}
	}
}

func TestAdminPaymentsPageHidesInternalError(t *testing.T) {
	internalError := errors.New("postgres password leaked")
	service := &adminDashboardServiceStub{payments: func(context.Context, dashboard.FinancialPeriod, int, int) (dashboard.PaymentOperationsPage, error) {
		return dashboard.PaymentOperationsPage{}, internalError
	}}
	response := httptest.NewRecorder()
	newAdminDashboardTestHandler(t, service, user.RoleAdmin).ServeHTTP(
		response, authenticatedRequest(http.MethodGet, "/admin/payments", ""),
	)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
	if strings.Contains(response.Body.String(), internalError.Error()) {
		t.Fatalf("body leaks internal error: %q", response.Body.String())
	}
}

func TestAdminPaymentsPageRequiresAdminAndRejectsUnsupportedMethod(t *testing.T) {
	operatorHandler := newAdminDashboardTestHandler(t, &adminDashboardServiceStub{}, user.RoleOperator)
	response := httptest.NewRecorder()
	operatorHandler.ServeHTTP(response, authenticatedRequest(http.MethodGet, "/admin/payments", ""))
	if response.Code != http.StatusForbidden {
		t.Fatalf("operator status = %d, want %d", response.Code, http.StatusForbidden)
	}

	adminHandler := newAdminDashboardTestHandler(t, &adminDashboardServiceStub{}, user.RoleAdmin)
	response = httptest.NewRecorder()
	adminHandler.ServeHTTP(response, authenticatedRequest(http.MethodPost, "/admin/payments", ""))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}

	response = httptest.NewRecorder()
	adminHandler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/payments", nil))
	if response.Code != http.StatusFound || response.Header().Get("Location") != "/login" {
		t.Fatalf("anonymous status = %d location = %q", response.Code, response.Header().Get("Location"))
	}
}

func TestAdminPaymentsPageShowsPeriodValidationErrors(t *testing.T) {
	for _, target := range []string{
		"/admin/payments?period=custom&from=2026-08-10&to=2026-08-08",
		"/admin/payments?period=custom&from=bad&to=2026-08-08",
		"/admin/payments?period=unknown",
	} {
		response := httptest.NewRecorder()
		newAdminDashboardTestHandler(t, &adminDashboardServiceStub{}, user.RoleAdmin).ServeHTTP(
			response, authenticatedRequest(http.MethodGet, target, ""),
		)
		if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), `role="alert"`) {
			t.Errorf("GET %s status = %d body %q", target, response.Code, response.Body.String())
		}
	}
}

func TestAdminPaymentsPageConnectsDateErrorToCompositeControl(t *testing.T) {
	response := httptest.NewRecorder()
	newAdminDashboardTestHandler(t, &adminDashboardServiceStub{}, user.RoleAdmin).ServeHTTP(
		response,
		authenticatedRequest(http.MethodGet, "/admin/payments?period=custom&from=bad&to=2026-08-08", ""),
	)

	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnprocessableEntity)
	}
	body := response.Body.String()
	for _, want := range []string{
		`aria-invalid="true" aria-describedby="finance-period-from-error"`,
		`id="finance-period-from-error" role="alert"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body does not contain %q", want)
		}
	}
}

func TestPaymentPresentationHelpers(t *testing.T) {
	for _, test := range []struct {
		kind       rental.PaymentKind
		amount     int64
		wantKind   string
		wantTone   string
		wantAmount string
	}{
		{rental.PaymentKindBase, 150_000, "Основная оплата", "base", "+1 500 ₽"},
		{rental.PaymentKindOverdue, 30_000, "Доплата за просрочку", "overdue", "+300 ₽"},
		{rental.PaymentKindRefund, 150_000, "Возврат", "refund", "−1 500 ₽"},
	} {
		kind, tone := paymentKindView(test.kind)
		if kind != test.wantKind || tone != test.wantTone || paymentAmountLabel(test.kind, test.amount) != test.wantAmount {
			t.Fatalf("presentation = %q %q %q", kind, tone, paymentAmountLabel(test.kind, test.amount))
		}
	}
}
