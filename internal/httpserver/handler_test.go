package httpserver

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Dyuzhovsergey/sup-rental/internal/audit"
	appauth "github.com/Dyuzhovsergey/sup-rental/internal/auth"
	"github.com/Dyuzhovsergey/sup-rental/internal/client"
	"github.com/Dyuzhovsergey/sup-rental/internal/equipment"
	"github.com/Dyuzhovsergey/sup-rental/internal/rental"
	"github.com/Dyuzhovsergey/sup-rental/internal/session"
	"github.com/Dyuzhovsergey/sup-rental/internal/user"
)

func TestRootRedirectsUnauthenticatedUserToLogin(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	newUnauthenticatedTestHandler(t, logger).ServeHTTP(response, request)

	if response.Code != http.StatusFound {
		t.Errorf("status code = %d, want %d", response.Code, http.StatusFound)
	}
	if got := response.Header().Get("Location"); got != "/login" {
		t.Errorf("Location = %q, want /login", got)
	}
}

func TestStatusRejectsUnsupportedMethod(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	response := httptest.NewRecorder()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	newTestHandler(t, logger).ServeHTTP(response, request)

	if response.Code != http.StatusMethodNotAllowed {
		t.Errorf("status code = %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}

	if got := response.Header().Get("Allow"); got != "GET, HEAD" {
		t.Errorf("Allow = %q, want %q", got, "GET, HEAD")
	}
}

func TestUnknownPathReturnsNotFound(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/unknown", nil)
	response := httptest.NewRecorder()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	newTestHandler(t, logger).ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Errorf("status code = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func TestHealth(t *testing.T) {
	request, err := http.NewRequest(http.MethodGet, "/health", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}

	response := newResponseRecorder()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	newTestHandler(t, logger).ServeHTTP(response, request)

	if response.statusCode != http.StatusOK {
		t.Errorf("status code = %d, want %d", response.statusCode, http.StatusOK)
	}

	const wantContentType = "text/plain; charset=utf-8"
	if got := response.header.Get("Content-Type"); got != wantContentType {
		t.Errorf("Content-Type = %q, want %q", got, wantContentType)
	}

	const wantBody = "ok\n"
	if got := response.body.String(); got != wantBody {
		t.Errorf("body = %q, want %q", got, wantBody)
	}
}

func TestHealthLogsWriteError(t *testing.T) {
	request, err := http.NewRequest(http.MethodGet, "/health", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}

	const writeErrorText = "write response"

	response := newResponseRecorder()
	response.writeErr = errors.New(writeErrorText)

	var logOutput bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logOutput, nil)).With(
		slog.String("component", "httpserver"),
	)

	newTestHandler(t, logger).ServeHTTP(response, request)

	for _, want := range []string{
		`level=ERROR`,
		`msg="write health response"`,
		`component=httpserver`,
		`error="` + writeErrorText + `"`,
	} {
		if !strings.Contains(logOutput.String(), want) {
			t.Errorf("log output = %q, want it to contain %q", logOutput.String(), want)
		}
	}
}

func TestStylesheet(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/static/app.css", nil)
	response := httptest.NewRecorder()

	newTestHandler(t, slog.New(slog.NewTextHandler(io.Discard, nil))).ServeHTTP(
		response,
		request,
	)

	if response.Code != http.StatusOK {
		t.Errorf("status code = %d, want %d", response.Code, http.StatusOK)
	}
	if got := response.Header().Get("Content-Type"); got != "text/css; charset=utf-8" {
		t.Errorf("Content-Type = %q, want %q", got, "text/css; charset=utf-8")
	}
	if got := response.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want %q", got, "no-cache")
	}

	for _, want := range []string{
		"--color-primary: #4f46e5;",
		".app-shell",
		".equipment-layout",
		".equipment-list-column",
		".button--compact",
		".button--edit",
		".retirement-panel",
		".limited-select__list",
		".limited-select__toggle",
		"max-height: 208px",
		".rental-review-summary",
		":focus-visible",
		"prefers-reduced-motion",
	} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("body does not contain %q", want)
		}
	}
}

func TestRentalScript(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/static/rental.js", nil)
	response := httptest.NewRecorder()

	newUnauthenticatedTestHandler(t, discardLogger()).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if got := response.Header().Get("Content-Type"); got != "text/javascript; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	for _, want := range []string{
		"data-rental-period-form", "data-rental-end", "data-rental-equipment-form",
		"data-rental-total", "data-rental-kind-count", "data-limited-select",
		"limited-select__option", `trigger.type = "number"`, "integerInRange", "Intl.NumberFormat",
	} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("script does not contain %q", want)
		}
	}
}

func TestStylesheetRejectsUnsupportedMethod(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/static/app.css", nil)
	response := httptest.NewRecorder()

	newTestHandler(t, slog.New(slog.NewTextHandler(io.Discard, nil))).ServeHTTP(
		response,
		request,
	)

	if response.Code != http.StatusMethodNotAllowed {
		t.Errorf("status code = %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}
	if got := response.Header().Get("Allow"); got != "GET, HEAD" {
		t.Errorf("Allow = %q, want %q", got, "GET, HEAD")
	}
}

func TestStylesheetLogsWriteError(t *testing.T) {
	const writeErrorText = "write response"

	request := httptest.NewRequest(http.MethodGet, "/static/app.css", nil)
	response := newResponseRecorder()
	response.writeErr = errors.New(writeErrorText)

	var logOutput bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logOutput, nil)).With(
		slog.String("component", "httpserver"),
	)

	stylesheet(logger, response, request)

	for _, want := range []string{
		`level=ERROR`,
		`msg="write application stylesheet"`,
		`component=httpserver`,
		`error="` + writeErrorText + `"`,
	} {
		if !strings.Contains(logOutput.String(), want) {
			t.Errorf("log output = %q, want it to contain %q", logOutput.String(), want)
		}
	}
}

func newTestHandler(
	t *testing.T,
	logger *slog.Logger,
	services ...equipmentService,
) http.Handler {
	t.Helper()

	var service equipmentService = &equipmentServiceStub{}
	if len(services) > 0 {
		service = services[0]
	}

	resolver := &sessionResolverStub{
		resolve: func(context.Context, string) (session.AuthenticatedSession, error) {
			return authenticatedFixture(), nil
		},
	}
	handler := newHandlerWithDependencies(t, logger, service, resolver)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "raw-session-token"})
		if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/equipment") {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read request body: %v", err)
			}
			encoded := string(body)
			if !strings.Contains(encoded, "csrf_token=") {
				if encoded != "" {
					encoded += "&"
				}
				encoded += "csrf_token=csrf-token"
			}
			r.Body = io.NopCloser(strings.NewReader(encoded))
			r.ContentLength = int64(len(encoded))
			if r.Header.Get("Content-Type") == "" {
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			}
		}
		handler.ServeHTTP(w, r)
	})
}

func newUnauthenticatedTestHandler(t *testing.T, logger *slog.Logger) http.Handler {
	t.Helper()
	return newHandlerWithDependencies(
		t,
		logger,
		&equipmentServiceStub{},
		&sessionResolverStub{},
	)
}

func newHandlerWithDependencies(
	t *testing.T,
	logger *slog.Logger,
	service equipmentService,
	resolver sessionResolver,
) http.Handler {
	t.Helper()

	handler, err := NewHandler(
		logger,
		service,
		&authServiceStub{},
		resolver,
		&operatorServiceStub{},
		&auditServiceStub{},
		&clientServiceStub{},
		&rentalServiceStub{},
		CookieSettings{},
	)
	if err != nil {
		t.Fatalf("create handler: %v", err)
	}

	return handler
}

type authServiceStub struct {
	login  func(context.Context, appauth.LoginInput) (appauth.LoginResult, error)
	logout func(context.Context, session.AuthenticatedSession) error
}

type auditServiceStub struct {
	list func(context.Context, user.User, audit.Filter) (audit.Page, error)
}

type clientServiceStub struct {
	create func(context.Context, user.User, string, string) (client.Client, error)
	update func(context.Context, user.User, int64, string, string) (client.Client, error)
	get    func(context.Context, int64) (client.Client, error)
	find   func(context.Context, string) (client.Client, error)
	list   func(context.Context, int, int) (client.Page, error)
}

type rentalServiceStub struct {
	available func(context.Context, rental.Interval) ([]rental.AvailableModel, error)
	create    func(context.Context, user.User, int64, rental.Interval, []rental.ModelSelection) (rental.Rental, error)
	get       func(context.Context, int64) (rental.Rental, error)
	list      func(context.Context, int, int) (rental.Page, error)
}

func (s *rentalServiceStub) AvailableModels(ctx context.Context, interval rental.Interval) ([]rental.AvailableModel, error) {
	if s.available == nil {
		return nil, nil
	}
	return s.available(ctx, interval)
}

func (s *rentalServiceStub) CreateConfirmed(ctx context.Context, actor user.User, clientID int64, interval rental.Interval, selections []rental.ModelSelection) (rental.Rental, error) {
	if s.create == nil {
		return rental.Restore(1, clientID, interval, rental.StatusConfirmed, []rental.Item{{
			EquipmentID: 1, InventoryNumber: "SUP-TEST-1", Kind: equipment.KindSUPBoard,
			ModelCode: "TEST", HourlyRateKopecks: 100_000,
		}})
	}
	return s.create(ctx, actor, clientID, interval, selections)
}

func (s *rentalServiceStub) Get(ctx context.Context, id int64) (rental.Rental, error) {
	if s.get == nil {
		return rental.Rental{}, rental.ErrRentalNotFound
	}
	return s.get(ctx, id)
}

func (s *rentalServiceStub) ListPage(ctx context.Context, page, pageSize int) (rental.Page, error) {
	if s.list == nil {
		return rental.Page{Page: page, PageSize: pageSize}, nil
	}
	return s.list(ctx, page, pageSize)
}

func (s *clientServiceStub) Create(ctx context.Context, actor user.User, fullName, phone string) (client.Client, error) {
	if s.create == nil {
		return client.Client{}, nil
	}
	return s.create(ctx, actor, fullName, phone)
}

func (s *clientServiceStub) Update(ctx context.Context, actor user.User, id int64, fullName, phone string) (client.Client, error) {
	if s.update == nil {
		return client.Client{}, nil
	}
	return s.update(ctx, actor, id, fullName, phone)
}

func (s *clientServiceStub) Get(ctx context.Context, id int64) (client.Client, error) {
	if s.get == nil {
		return client.Client{}, client.ErrClientNotFound
	}
	return s.get(ctx, id)
}

func (s *clientServiceStub) FindByPhone(ctx context.Context, phone string) (client.Client, error) {
	if s.find == nil {
		return client.Client{}, client.ErrClientNotFound
	}
	return s.find(ctx, phone)
}

func (s *clientServiceStub) ListPage(ctx context.Context, page, pageSize int) (client.Page, error) {
	if s.list == nil {
		return client.Page{Page: page}, nil
	}
	return s.list(ctx, page, pageSize)
}

func (s *auditServiceStub) List(ctx context.Context, actor user.User, filter audit.Filter) (audit.Page, error) {
	if s.list == nil {
		return audit.Page{Page: filter.Page}, nil
	}
	return s.list(ctx, actor, filter)
}

type operatorServiceStub struct {
	list           func(context.Context, user.User) ([]user.User, error)
	get            func(context.Context, user.User, int64) (user.User, error)
	create         func(context.Context, user.User, string, string) (user.User, error)
	disable        func(context.Context, user.User, int64) (user.User, error)
	activate       func(context.Context, user.User, int64) (user.User, error)
	changePassword func(context.Context, user.User, int64, string) (user.User, error)
}

func (s *operatorServiceStub) List(ctx context.Context, actor user.User) ([]user.User, error) {
	if s.list == nil {
		return nil, nil
	}
	return s.list(ctx, actor)
}
func (s *operatorServiceStub) Get(ctx context.Context, actor user.User, id int64) (user.User, error) {
	if s.get == nil {
		return user.User{}, user.ErrOperatorNotFound
	}
	return s.get(ctx, actor, id)
}
func (s *operatorServiceStub) Create(ctx context.Context, actor user.User, login, plainPassword string) (user.User, error) {
	if s.create == nil {
		return user.User{}, nil
	}
	return s.create(ctx, actor, login, plainPassword)
}
func (s *operatorServiceStub) Disable(ctx context.Context, actor user.User, id int64) (user.User, error) {
	if s.disable == nil {
		return user.User{}, nil
	}
	return s.disable(ctx, actor, id)
}
func (s *operatorServiceStub) Activate(ctx context.Context, actor user.User, id int64) (user.User, error) {
	if s.activate == nil {
		return user.User{}, nil
	}
	return s.activate(ctx, actor, id)
}
func (s *operatorServiceStub) ChangePassword(ctx context.Context, actor user.User, id int64, plainPassword string) (user.User, error) {
	if s.changePassword == nil {
		return user.User{}, nil
	}
	return s.changePassword(ctx, actor, id, plainPassword)
}

func (s *authServiceStub) Login(
	ctx context.Context,
	input appauth.LoginInput,
) (appauth.LoginResult, error) {
	if s.login == nil {
		return appauth.LoginResult{}, appauth.ErrInvalidCredentials
	}
	return s.login(ctx, input)
}

func (s *authServiceStub) Logout(
	ctx context.Context,
	authenticated session.AuthenticatedSession,
) error {
	if s.logout == nil {
		return nil
	}
	return s.logout(ctx, authenticated)
}

type sessionResolverStub struct {
	resolve func(context.Context, string) (session.AuthenticatedSession, error)
}

func (s *sessionResolverStub) Resolve(
	ctx context.Context,
	token string,
) (session.AuthenticatedSession, error) {
	if s.resolve == nil {
		return session.AuthenticatedSession{}, session.ErrSessionNotFound
	}
	return s.resolve(ctx, token)
}

type responseRecorder struct {
	header     http.Header
	body       bytes.Buffer
	statusCode int
	writeErr   error
}

func newResponseRecorder() *responseRecorder {
	return &responseRecorder{
		header: make(http.Header),
	}
}

func (r *responseRecorder) Header() http.Header {
	return r.header
}

func (r *responseRecorder) Write(body []byte) (int, error) {
	if r.statusCode == 0 {
		r.statusCode = http.StatusOK
	}

	if r.writeErr != nil {
		return 0, r.writeErr
	}

	return r.body.Write(body)
}

func (r *responseRecorder) WriteHeader(statusCode int) {
	r.statusCode = statusCode
}

type equipmentServiceStub struct {
	create        func(context.Context, equipment.BatchCreateInput) (equipment.Batch, error)
	list          func(context.Context) ([]equipment.Item, error)
	get           func(context.Context, int64) (equipment.Item, error)
	update        func(context.Context, int64, equipment.UpdateInput) (equipment.Item, error)
	changeModel   func(context.Context, int64, equipment.ModelChangeInput) (equipment.Item, error)
	changeRate    func(context.Context, int64, int64) (equipment.ModelRateChange, error)
	changeStatus  func(context.Context, int64, equipment.Status) (equipment.Item, error)
	delete        func(context.Context, int64) (equipment.Item, error)
	mutationActor user.User
}

func (s *equipmentServiceStub) CreateBatch(
	ctx context.Context,
	actor user.User,
	input equipment.BatchCreateInput,
) (equipment.Batch, error) {
	s.mutationActor = actor
	if s.create == nil {
		return equipment.Batch{}, nil
	}

	return s.create(ctx, input)
}

func (s *equipmentServiceStub) List(ctx context.Context) ([]equipment.Item, error) {
	if s.list == nil {
		return []equipment.Item{}, nil
	}

	return s.list(ctx)
}

func (s *equipmentServiceStub) ListPage(
	ctx context.Context,
	input equipment.ListPageInput,
) (equipment.ListPage, error) {
	items, err := s.List(ctx)
	if err != nil {
		return equipment.ListPage{}, err
	}
	filtered := make([]equipment.Item, 0, len(items))
	for _, item := range items {
		retired := item.Status == equipment.StatusRetired
		if (input.Scope == equipment.ListScopeRetired && retired) ||
			(input.Scope == equipment.ListScopeActive && !retired) {
			filtered = append(filtered, item)
		}
	}
	start := (input.Page - 1) * input.PageSize
	if start > len(filtered) {
		start = len(filtered)
	}
	end := start + input.PageSize
	if end > len(filtered) {
		end = len(filtered)
	}
	return equipment.ListPage{
		Scope: input.Scope, Items: filtered[start:end], Total: len(filtered),
		Page: input.Page, PageSize: input.PageSize,
	}, nil
}

func (s *equipmentServiceStub) Get(
	ctx context.Context,
	id int64,
) (equipment.Item, error) {
	if s.get == nil {
		return equipment.Item{}, nil
	}

	return s.get(ctx, id)
}

func (s *equipmentServiceStub) Update(
	ctx context.Context,
	actor user.User,
	id int64,
	input equipment.UpdateInput,
) (equipment.Item, error) {
	s.mutationActor = actor
	if s.update == nil {
		return equipment.Item{}, nil
	}

	return s.update(ctx, id, input)
}

func (s *equipmentServiceStub) ChangeModel(
	ctx context.Context,
	actor user.User,
	id int64,
	input equipment.ModelChangeInput,
) (equipment.Item, error) {
	s.mutationActor = actor
	if s.changeModel == nil {
		return equipment.Item{}, nil
	}
	return s.changeModel(ctx, id, input)
}

func (s *equipmentServiceStub) ChangeModelRate(
	ctx context.Context,
	actor user.User,
	id int64,
	hourlyRateRubles int64,
) (equipment.ModelRateChange, error) {
	s.mutationActor = actor
	if s.changeRate == nil {
		return equipment.ModelRateChange{}, nil
	}
	return s.changeRate(ctx, id, hourlyRateRubles)
}

func (s *equipmentServiceStub) ChangeStatus(
	ctx context.Context,
	actor user.User,
	id int64,
	status equipment.Status,
) (equipment.Item, error) {
	s.mutationActor = actor
	if s.changeStatus == nil {
		return equipment.Item{}, nil
	}

	return s.changeStatus(ctx, id, status)
}

func (s *equipmentServiceStub) Delete(
	ctx context.Context,
	actor user.User,
	id int64,
) (equipment.Item, error) {
	s.mutationActor = actor
	if s.delete == nil {
		return equipment.Item{}, nil
	}

	return s.delete(ctx, id)
}
