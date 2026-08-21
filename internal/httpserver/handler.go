// Package httpserver предоставляет HTTP-обработчики приложения.
package httpserver

import (
	"embed"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"

	"github.com/Dyuzhovsergey/sup-rental/internal/client"
	"github.com/Dyuzhovsergey/sup-rental/internal/user"
)

//go:embed templates/*.html
var templateFiles embed.FS

//go:embed static/app.css
var appStyles []byte

//go:embed static/rental.js
var rentalScript []byte

//go:embed static/theme.js
var themeScript []byte

// NewHandler создаёт HTTP-обработчик со всеми маршрутами приложения.
//
// Logger используется для записи ошибок HTTP-слоя, а equipmentService
// предоставляет сценарии учёта оборудования. Auth service, session resolver и
// cookie settings обеспечивают login/logout, а operator service — admin-only
// управление учётными записями операторов. Client и rental services реализуют
// пользовательские сценарии клиентов и аренд, а dashboard service формирует
// read-only панель администратора. Все зависимости должны быть созданы точкой
// входа приложения. NewHandler возвращает ошибку, если
// встроенные HTML-шаблоны невозможно разобрать.
func NewHandler(
	logger *slog.Logger,
	equipmentService equipmentService,
	authenticationService authService,
	sessions sessionResolver,
	operators operatorService,
	auditLog auditService,
	clients clientService,
	rentals rentalService,
	adminDashboard adminDashboardService,
	cookieSettings CookieSettings,
) (http.Handler, error) {
	pageTemplates, err := template.New("pages").Funcs(template.FuncMap{
		"phoneLabel": clientPhoneLabel,
	}).ParseFS(templateFiles, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse HTML templates: %w", err)
	}

	mux := http.NewServeMux()
	authenticated := func(handler http.Handler) http.Handler {
		return requireAuthentication(handler)
	}
	adminOnly := func(handler http.Handler) http.Handler {
		return authenticated(requireRole(logger, pageTemplates, user.RoleAdmin, handler))
	}
	operatorOnly := func(handler http.Handler) http.Handler {
		return authenticated(requireRole(logger, pageTemplates, user.RoleOperator, handler))
	}

	mux.Handle("GET /{$}", authenticated(http.HandlerFunc(redirectToRoleHome)))
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		health(logger, w, r)
	})
	mux.HandleFunc("GET /static/app.css", func(w http.ResponseWriter, r *http.Request) {
		stylesheet(logger, w, r)
	})
	mux.HandleFunc("GET /static/rental.js", func(w http.ResponseWriter, r *http.Request) {
		javascript(logger, w, r)
	})
	mux.HandleFunc("GET /static/theme.js", func(w http.ResponseWriter, r *http.Request) {
		writeJavaScript(logger, "write theme script", themeScript, w)
	})
	mux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) {
		showLoginPage(logger, pageTemplates, w, r)
	})
	mux.HandleFunc("POST /login", func(w http.ResponseWriter, r *http.Request) {
		login(logger, authenticationService, pageTemplates, cookieSettings, w, r)
	})
	mux.Handle("POST /logout", authenticated(requireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logout(logger, authenticationService, cookieSettings, w, r)
	}))))
	mux.Handle("GET /operator", operatorOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		showOperatorDashboard(logger, rentals, pageTemplates, w, r)
	})))
	mux.Handle("GET /admin", adminOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		showAdminDashboard(logger, adminDashboard, pageTemplates, w, r)
	})))
	mux.Handle("GET /admin/operators", adminOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		showOperatorsPage(logger, operators, pageTemplates, w, r)
	})))
	mux.Handle("POST /admin/operators", adminOnly(requireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		createOperator(logger, operators, pageTemplates, w, r)
	}))))
	mux.Handle("GET /admin/operators/{id}/disable", adminOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		showOperatorDisablePage(logger, operators, pageTemplates, w, r)
	})))
	mux.Handle("POST /admin/operators/{id}/disable", adminOnly(requireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		disableOperator(logger, operators, w, r)
	}))))
	mux.Handle("POST /admin/operators/{id}/activate", adminOnly(requireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		activateOperator(logger, operators, w, r)
	}))))
	mux.Handle("GET /admin/operators/{id}/password", adminOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		showOperatorPasswordPage(logger, operators, pageTemplates, w, r)
	})))
	mux.Handle("POST /admin/operators/{id}/password", adminOnly(requireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		changeOperatorPassword(logger, operators, pageTemplates, w, r)
	}))))
	mux.Handle("GET /admin/audit", adminOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		showAuditPage(logger, auditLog, pageTemplates, w, r)
	})))
	mux.Handle("GET /clients", authenticated(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientsPage(logger, clients, pageTemplates, w, r)
	})))
	mux.Handle("POST /clients", operatorOnly(requireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientsPage(logger, clients, pageTemplates, w, r)
	}))))
	mux.Handle("GET /clients/{id}", authenticated(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		showClientDetailPage(logger, clients, pageTemplates, w, r)
	})))
	mux.Handle("GET /clients/{id}/edit", operatorOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		showClientEditPage(logger, clients, pageTemplates, w, r)
	})))
	mux.Handle("POST /clients/{id}/edit", operatorOnly(requireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		updateClient(logger, clients, pageTemplates, w, r)
	}))))
	mux.Handle("GET /rentals", authenticated(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		showRentalsPage(logger, rentals, pageTemplates, w, r)
	})))
	mux.Handle("GET /rentals/new", operatorOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		showRentalPeriodStep(logger, clients, pageTemplates, w, r)
	})))
	mux.Handle("GET /rentals/new/client", operatorOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		showRentalClientStep(logger, rentals, clients, pageTemplates, w, r)
	})))
	mux.Handle("POST /rentals/new/client", operatorOnly(requireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		selectRentalClient(logger, rentals, clients, pageTemplates, w, r)
	}))))
	mux.Handle("GET /rentals/new/period", operatorOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		showRentalPeriodStep(logger, clients, pageTemplates, w, r)
	})))
	mux.Handle("GET /rentals/new/equipment", operatorOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		showRentalEquipmentStep(logger, rentals, clients, pageTemplates, w, r)
	})))
	mux.Handle("GET /rentals/new/review", operatorOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		showRentalReviewStep(logger, rentals, clients, pageTemplates, w, r)
	})))
	mux.Handle("POST /rentals", operatorOnly(requireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		createConfirmedRental(logger, rentals, clients, pageTemplates, w, r)
	}))))
	mux.Handle("GET /rentals/bulk/issue", operatorOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		showBulkRentalIssuePage(logger, rentals, clients, pageTemplates, w, r)
	})))
	mux.Handle("POST /rentals/bulk/issue", operatorOnly(requireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		issueSelectedRentals(logger, rentals, w, r)
	}))))
	mux.Handle("GET /rentals/bulk/cancel", operatorOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		showBulkRentalCancelPage(logger, rentals, clients, pageTemplates, w, r)
	})))
	mux.Handle("POST /rentals/bulk/cancel", operatorOnly(requireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cancelSelectedRentals(logger, rentals, w, r)
	}))))
	mux.Handle("GET /rentals/bulk/complete", operatorOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		showBulkRentalCompletePage(logger, rentals, clients, pageTemplates, w, r)
	})))
	mux.Handle("POST /rentals/bulk/complete", operatorOnly(requireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		completeSelectedRentals(logger, rentals, w, r)
	}))))
	mux.Handle("GET /rentals/{id}/issue", operatorOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		showRentalIssuePage(logger, rentals, clients, pageTemplates, w, r)
	})))
	mux.Handle("POST /rentals/{id}/issue", operatorOnly(requireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		issueRental(logger, rentals, w, r)
	}))))
	mux.Handle("GET /rentals/{id}/cancel", operatorOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		showRentalCancelPage(logger, rentals, clients, pageTemplates, w, r)
	})))
	mux.Handle("POST /rentals/{id}/cancel", operatorOnly(requireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cancelRental(logger, rentals, w, r)
	}))))
	mux.Handle("GET /rentals/{id}/complete", operatorOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		showRentalCompletePage(logger, rentals, clients, pageTemplates, w, r)
	})))
	mux.Handle("POST /rentals/{id}/complete", operatorOnly(requireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		completeRental(logger, rentals, w, r)
	}))))
	mux.Handle("GET /rentals/{id}", authenticated(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		showRentalDetailPage(logger, rentals, clients, pageTemplates, w, r)
	})))
	mux.Handle("GET /equipment", authenticated(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		equipmentPage(logger, equipmentService, pageTemplates, w, r)
	})))
	mux.Handle("POST /equipment", adminOnly(requireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		equipmentPage(logger, equipmentService, pageTemplates, w, r)
	}))))
	mux.Handle("GET /equipment/{id}", authenticated(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		showEquipmentDetailPage(logger, equipmentService, pageTemplates, w, r)
	})))
	mux.Handle("GET /equipment/{id}/retire", adminOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		showEquipmentRetirePage(logger, equipmentService, pageTemplates, w, r)
	})))
	mux.Handle("POST /equipment/{id}/retire", adminOnly(requireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		retireEquipment(logger, equipmentService, w, r)
	}))))
	mux.Handle("GET /equipment/{id}/delete", adminOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		showEquipmentDeletePage(logger, equipmentService, pageTemplates, w, r)
	})))
	mux.Handle("POST /equipment/{id}/delete", adminOnly(requireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deleteEquipment(logger, equipmentService, w, r)
	}))))
	mux.Handle("GET /equipment/{id}/edit", adminOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		showEquipmentEditPage(logger, equipmentService, pageTemplates, w, r)
	})))
	mux.Handle("POST /equipment/{id}/edit", adminOnly(requireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		updateEquipment(logger, equipmentService, pageTemplates, w, r)
	}))))
	mux.Handle("POST /equipment/{id}/model", adminOnly(requireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		changeEquipmentModel(logger, equipmentService, pageTemplates, w, r)
	}))))
	mux.Handle("POST /equipment/{id}/rate", adminOnly(requireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		changeEquipmentModelRate(logger, equipmentService, pageTemplates, w, r)
	}))))

	protected := http.NewCrossOriginProtection().Handler(
		optionalSession(logger, sessions, cookieSettings, mux),
	)

	return protected, nil
}

func clientPhoneLabel(phone client.Phone) string {
	value := phone.String()
	if len(value) != 12 || value[:2] != "+7" {
		return value
	}
	return fmt.Sprintf(
		"+7 (%s) %s-%s-%s",
		value[2:5], value[5:8], value[8:10], value[10:12],
	)
}

func stylesheet(logger *slog.Logger, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	if _, err := w.Write(appStyles); err != nil {
		logger.Error(
			"write application stylesheet",
			slog.Any("error", err),
		)
	}
}

func javascript(logger *slog.Logger, w http.ResponseWriter, r *http.Request) {
	writeJavaScript(logger, "write rental script", rentalScript, w)
}

func writeJavaScript(logger *slog.Logger, logMessage string, script []byte, w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(script); err != nil {
		logger.Error(logMessage, slog.Any("error", err))
	}
}

func health(logger *slog.Logger, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	if _, err := w.Write([]byte("ok\n")); err != nil {
		logger.Error(
			"write health response",
			slog.Any("error", err),
		)
	}
}
