package httpserver

import (
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/Dyuzhovsergey/sup-rental/internal/dashboard"
	"github.com/Dyuzhovsergey/sup-rental/internal/rental"
)

const defaultPaymentPageSize = 10

type adminPaymentOperationView struct {
	RentalID   int64
	ClientName string
	Kind       string
	Tone       string
	Amount     string
	OccurredAt string
	ActorLogin string
}

type adminPaymentsPageData struct {
	Authentication  *authenticationView
	Title           string
	Operations      []adminPaymentOperationView
	TotalLabel      string
	PageSize        int
	PageSizeOptions []pageSizeOption
	HasPrevious     bool
	HasNext         bool
	PreviousURL     string
	NextURL         string
	PageLabel       string
}

func showAdminPaymentsPage(
	logger *slog.Logger,
	service adminDashboardService,
	pageTemplates *template.Template,
	w http.ResponseWriter,
	r *http.Request,
) {
	page, pageSize, ok := paymentPagination(r.URL.Query())
	if !ok {
		http.NotFound(w, r)
		return
	}
	result, err := service.PaymentOperations(r.Context(), page, pageSize)
	if errors.Is(err, dashboard.ErrInvalidPaymentPagination) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		logger.Error("list payment operations", slog.Any("error", err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	if result.Total > 0 && len(result.Operations) == 0 {
		http.NotFound(w, r)
		return
	}

	totalPages := pageCount(int(result.Total), pageSize)
	data := adminPaymentsPageData{
		Authentication:  authenticationForPage(r),
		Title:           "Платёжные операции — SUP Rental",
		Operations:      adminPaymentOperationViews(result.Operations),
		TotalLabel:      paymentCountLabel(int(result.Total)),
		PageSize:        pageSize,
		PageSizeOptions: paymentPageSizeOptions(pageSize),
		HasPrevious:     page > 1,
		HasNext:         page < totalPages,
		PreviousURL:     paymentPageURL(page-1, pageSize),
		NextURL:         paymentPageURL(page+1, pageSize),
		PageLabel:       pageLabel(page, totalPages),
	}
	renderPage(
		logger, pageTemplates, w, http.StatusOK, "admin_payments.html", data,
		"render admin payments", "write admin payments response",
	)
}

func adminPaymentOperationViews(operations []dashboard.PaymentOperation) []adminPaymentOperationView {
	views := make([]adminPaymentOperationView, 0, len(operations))
	for _, operation := range operations {
		kind, tone := paymentKindView(operation.Kind)
		views = append(views, adminPaymentOperationView{
			RentalID: operation.RentalID, ClientName: operation.ClientName,
			Kind: kind, Tone: tone, Amount: paymentAmountLabel(operation.Kind, operation.AmountKopecks),
			OccurredAt: operation.OccurredAt.In(moscowTimeZone).Format("02.01.2006 15:04"),
			ActorLogin: operation.ActorLogin,
		})
	}
	return views
}

func paymentKindView(kind rental.PaymentKind) (string, string) {
	switch kind {
	case rental.PaymentKindBase:
		return "Основная оплата", "base"
	case rental.PaymentKindOverdue:
		return "Доплата за просрочку", "overdue"
	case rental.PaymentKindRefund:
		return "Возврат", "refund"
	default:
		return "Неизвестная операция", "unknown"
	}
}

func paymentAmountLabel(kind rental.PaymentKind, amount int64) string {
	if kind == rental.PaymentKindRefund {
		return adminMoneyLabel(-amount)
	}
	return "+" + adminMoneyLabel(amount)
}

func paymentPagination(query url.Values) (int, int, bool) {
	page, ok := positiveQueryPage(query.Get("page"))
	if !ok {
		return 0, 0, false
	}
	pageSize := defaultPaymentPageSize
	if raw := query.Get("page_size"); raw != "" {
		var err error
		pageSize, err = strconv.Atoi(raw)
		if err != nil {
			return 0, 0, false
		}
	}
	for _, allowed := range []int{5, 10, 15} {
		if pageSize == allowed {
			return page, pageSize, true
		}
	}
	return 0, 0, false
}

func paymentPageSizeOptions(selected int) []pageSizeOption {
	options := make([]pageSizeOption, 0, 3)
	for _, size := range []int{5, 10, 15} {
		options = append(options, pageSizeOption{Value: size, Selected: size == selected})
	}
	return options
}

func paymentPageURL(page, pageSize int) string {
	query := url.Values{"page_size": {strconv.Itoa(pageSize)}}
	if page > 1 {
		query.Set("page", strconv.Itoa(page))
	}
	return "/admin/payments?" + query.Encode()
}

func paymentCountLabel(count int) string {
	word := "операций"
	if count%10 == 1 && count%100 != 11 {
		word = "операция"
	} else if count%10 >= 2 && count%10 <= 4 && (count%100 < 12 || count%100 > 14) {
		word = "операции"
	}
	return strconv.Itoa(count) + " " + word
}
