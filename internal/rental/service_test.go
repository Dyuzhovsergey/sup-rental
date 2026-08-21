package rental

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/Dyuzhovsergey/sup-rental/internal/equipment"
	"github.com/Dyuzhovsergey/sup-rental/internal/user"
)

func TestServiceAvailableModelsGroupsPhysicalEquipment(t *testing.T) {
	service := NewService(&serviceRepositoryStub{available: func(context.Context, Interval) ([]equipment.Item, error) {
		return []equipment.Item{
			equipmentFixture(1, 10, equipment.KindSUPBoard, "TOURING", 100_000),
			equipmentFixture(2, 10, equipment.KindSUPBoard, "TOURING", 100_000),
			equipmentFixture(3, 20, equipment.KindPaddle, "CARBON", 35_000),
		}, nil
	}})
	models, err := service.AvailableModels(context.Background(), serviceInterval(t))
	if err != nil {
		t.Fatalf("AvailableModels() error = %v", err)
	}
	if len(models) != 2 || models[0].ModelID != 10 || models[0].AvailableCount != 2 || models[1].AvailableCount != 1 {
		t.Fatalf("models = %+v", models)
	}
}

func TestServiceCreateConfirmedDelegatesValidatedInput(t *testing.T) {
	var gotActor user.User
	var gotSelections []ModelSelection
	repository := &serviceRepositoryStub{create: func(
		_ context.Context, actor user.User, clientID int64, interval Interval, selections []ModelSelection,
	) (Rental, error) {
		gotActor = actor
		gotSelections = append([]ModelSelection(nil), selections...)
		return Restore(24, clientID, interval, StatusConfirmed, nil, nil, []Item{rentalItemFixture(1)})
	}}
	service := NewService(repository)
	operator := user.User{ID: 7, Login: "operator", Role: user.RoleOperator, Active: true}
	created, err := service.CreateConfirmed(
		context.Background(), operator, 18, serviceInterval(t),
		[]ModelSelection{{ModelID: 10, Quantity: 1}, {ModelID: 20, Quantity: 0}},
	)
	if err != nil {
		t.Fatalf("CreateConfirmed() error = %v", err)
	}
	if created.ID != 24 || gotActor != operator || len(gotSelections) != 2 {
		t.Fatalf("created = %+v actor = %+v selections = %+v", created, gotActor, gotSelections)
	}
}

func TestServiceCreateConfirmedRejectsAccessAndSelections(t *testing.T) {
	interval := serviceInterval(t)
	service := NewService(&serviceRepositoryStub{})
	operator := user.User{ID: 7, Role: user.RoleOperator, Active: true}
	tests := []struct {
		name       string
		actor      user.User
		clientID   int64
		selections []ModelSelection
		want       error
	}{
		{name: "admin", actor: user.User{ID: 1, Role: user.RoleAdmin, Active: true}, clientID: 18, selections: []ModelSelection{{ModelID: 10, Quantity: 1}}, want: user.ErrAccessDenied},
		{name: "inactive", actor: user.User{ID: 7, Role: user.RoleOperator}, clientID: 18, selections: []ModelSelection{{ModelID: 10, Quantity: 1}}, want: user.ErrAccessDenied},
		{name: "client", actor: operator, selections: []ModelSelection{{ModelID: 10, Quantity: 1}}, want: ErrInvalidClientID},
		{name: "invalid model", actor: operator, clientID: 18, selections: []ModelSelection{{ModelID: 0, Quantity: 1}}, want: ErrInvalidModelSelection},
		{name: "negative", actor: operator, clientID: 18, selections: []ModelSelection{{ModelID: 10, Quantity: -1}}, want: ErrInvalidModelSelection},
		{name: "duplicate", actor: operator, clientID: 18, selections: []ModelSelection{{ModelID: 10}, {ModelID: 10}}, want: ErrInvalidModelSelection},
		{name: "empty", actor: operator, clientID: 18, selections: []ModelSelection{{ModelID: 10}}, want: ErrRentalItemsRequired},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := service.CreateConfirmed(context.Background(), tt.actor, tt.clientID, interval, tt.selections)
			if !errors.Is(err, tt.want) {
				t.Fatalf("CreateConfirmed() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestServiceIssueUsesCurrentTimeAndActor(t *testing.T) {
	issuedAt := time.Date(2026, 8, 17, 12, 34, 56, 0, time.FixedZone("МСК", 3*60*60))
	var gotActor user.User
	var gotTime time.Time
	repository := &serviceRepositoryStub{issue: func(
		_ context.Context, actor user.User, id int64, at time.Time,
	) (Rental, error) {
		gotActor = actor
		gotTime = at
		value, err := Restore(id, 18, serviceInterval(t), StatusConfirmed, nil, nil, []Item{rentalItemFixture(1)})
		if err != nil {
			return Rental{}, err
		}
		if err := value.Issue(at); err != nil {
			return Rental{}, err
		}
		return value, nil
	}}
	service := NewService(repository)
	service.now = func() time.Time { return issuedAt }
	actor := user.User{ID: 7, Login: "operator", Role: user.RoleOperator, Active: true}

	value, err := service.Issue(context.Background(), actor, 24)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if value.Status != StatusActive || gotActor != actor || !gotTime.Equal(issuedAt.UTC()) || gotTime.Location() != time.UTC {
		t.Fatalf("value = %+v actor = %+v issuedAt = %v", value, gotActor, gotTime)
	}
}

func TestServiceIssueRejectsAccessAndInvalidID(t *testing.T) {
	service := NewService(&serviceRepositoryStub{})
	for _, tt := range []struct {
		name  string
		actor user.User
		id    int64
		want  error
	}{
		{name: "admin", actor: user.User{ID: 1, Role: user.RoleAdmin, Active: true}, id: 24, want: user.ErrAccessDenied},
		{name: "inactive operator", actor: user.User{ID: 7, Role: user.RoleOperator}, id: 24, want: user.ErrAccessDenied},
		{name: "invalid ID", actor: user.User{ID: 7, Role: user.RoleOperator, Active: true}, want: ErrRentalNotFound},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := service.Issue(context.Background(), tt.actor, tt.id); !errors.Is(err, tt.want) {
				t.Fatalf("Issue() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestServiceCancelUsesActorAndValidatesAccess(t *testing.T) {
	actor := user.User{ID: 7, Login: "operator", Role: user.RoleOperator, Active: true}
	var gotActor user.User
	repository := &serviceRepositoryStub{cancel: func(_ context.Context, value user.User, id int64) (Rental, error) {
		gotActor = value
		rentalValue, err := Restore(id, 18, serviceInterval(t), StatusConfirmed, nil, nil, []Item{rentalItemFixture(1)})
		if err != nil {
			return Rental{}, err
		}
		if err := rentalValue.Cancel(); err != nil {
			return Rental{}, err
		}
		return rentalValue, nil
	}}
	cancelled, err := NewService(repository).Cancel(context.Background(), actor, 24)
	if err != nil || cancelled.Status != StatusCancelled || gotActor != actor {
		t.Fatalf("Cancel() = %+v, %v; actor = %+v", cancelled, err, gotActor)
	}

	service := NewService(&serviceRepositoryStub{})
	for _, tt := range []struct {
		name  string
		actor user.User
		id    int64
		want  error
	}{
		{name: "admin", actor: user.User{ID: 1, Role: user.RoleAdmin, Active: true}, id: 24, want: user.ErrAccessDenied},
		{name: "inactive", actor: user.User{ID: 7, Role: user.RoleOperator}, id: 24, want: user.ErrAccessDenied},
		{name: "invalid ID", actor: actor, want: ErrRentalNotFound},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := service.Cancel(context.Background(), tt.actor, tt.id); !errors.Is(err, tt.want) {
				t.Fatalf("Cancel() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestServiceCompleteUsesCurrentTimeAndActor(t *testing.T) {
	issuedAt := time.Date(2026, 8, 20, 9, 15, 0, 0, time.UTC)
	returnedAt := time.Date(2026, 8, 20, 12, 45, 0, 0, time.FixedZone("МСК", 3*60*60))
	actor := user.User{ID: 7, Login: "operator", Role: user.RoleOperator, Active: true}
	var gotActor user.User
	var gotTime time.Time
	repository := &serviceRepositoryStub{complete: func(
		_ context.Context, value user.User, id int64, at time.Time,
	) (Rental, error) {
		gotActor = value
		gotTime = at
		rentalValue, err := Restore(
			id, 18, serviceInterval(t), StatusActive, &issuedAt, nil,
			[]Item{rentalItemFixture(1)},
		)
		if err != nil {
			return Rental{}, err
		}
		if err := rentalValue.Complete(at); err != nil {
			return Rental{}, err
		}
		return rentalValue, nil
	}}
	service := NewService(repository)
	service.now = func() time.Time { return returnedAt }

	completed, err := service.Complete(context.Background(), actor, 24)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if completed.Status != StatusCompleted || gotActor != actor ||
		!gotTime.Equal(returnedAt.UTC()) || gotTime.Location() != time.UTC {
		t.Fatalf("Complete() = %+v actor = %+v returnedAt = %v", completed, gotActor, gotTime)
	}
}

func TestServiceCompleteRejectsAccessAndInvalidID(t *testing.T) {
	service := NewService(&serviceRepositoryStub{})
	for _, tt := range []struct {
		name  string
		actor user.User
		id    int64
		want  error
	}{
		{name: "admin", actor: user.User{ID: 1, Role: user.RoleAdmin, Active: true}, id: 24, want: user.ErrAccessDenied},
		{name: "inactive", actor: user.User{ID: 7, Role: user.RoleOperator}, id: 24, want: user.ErrAccessDenied},
		{name: "invalid ID", actor: user.User{ID: 7, Role: user.RoleOperator, Active: true}, want: ErrRentalNotFound},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := service.Complete(context.Background(), tt.actor, tt.id); !errors.Is(err, tt.want) {
				t.Fatalf("Complete() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestServiceBulkActionsValidateSelectionAndDelegate(t *testing.T) {
	actor := user.User{ID: 7, Login: "operator", Role: user.RoleOperator, Active: true}
	issuedAt := time.Date(2026, 8, 17, 12, 34, 56, 0, time.FixedZone("МСК", 3*60*60))
	var issuedIDs, cancelledIDs, completedIDs []int64
	var gotIssuedAt, gotReturnedAt time.Time
	repository := &serviceRepositoryStub{
		issueMany: func(_ context.Context, gotActor user.User, ids []int64, at time.Time) ([]Rental, error) {
			if gotActor != actor {
				t.Errorf("IssueMany() actor = %+v", gotActor)
			}
			issuedIDs = append([]int64(nil), ids...)
			gotIssuedAt = at
			return []Rental{{ID: ids[0], Status: StatusActive}}, nil
		},
		cancelMany: func(_ context.Context, gotActor user.User, ids []int64) ([]Rental, error) {
			if gotActor != actor {
				t.Errorf("CancelMany() actor = %+v", gotActor)
			}
			cancelledIDs = append([]int64(nil), ids...)
			return []Rental{{ID: ids[0], Status: StatusCancelled}}, nil
		},
		completeMany: func(_ context.Context, gotActor user.User, ids []int64, at time.Time) ([]Rental, error) {
			if gotActor != actor {
				t.Errorf("CompleteMany() actor = %+v", gotActor)
			}
			completedIDs = append([]int64(nil), ids...)
			gotReturnedAt = at
			return []Rental{{ID: ids[0], Status: StatusCompleted}}, nil
		},
	}
	service := NewService(repository)
	service.now = func() time.Time { return issuedAt }

	if _, err := service.IssueMany(context.Background(), actor, []int64{24, 25}); err != nil {
		t.Fatalf("IssueMany() error = %v", err)
	}
	if _, err := service.CancelMany(context.Background(), actor, []int64{26, 27}); err != nil {
		t.Fatalf("CancelMany() error = %v", err)
	}
	if _, err := service.CompleteMany(context.Background(), actor, []int64{28, 29}); err != nil {
		t.Fatalf("CompleteMany() error = %v", err)
	}
	if len(issuedIDs) != 2 || issuedIDs[0] != 24 || issuedIDs[1] != 25 ||
		!gotIssuedAt.Equal(issuedAt.UTC()) || gotIssuedAt.Location() != time.UTC {
		t.Fatalf("IssueMany() ids = %v issuedAt = %v", issuedIDs, gotIssuedAt)
	}
	if len(cancelledIDs) != 2 || cancelledIDs[0] != 26 || cancelledIDs[1] != 27 {
		t.Fatalf("CancelMany() ids = %v", cancelledIDs)
	}
	if len(completedIDs) != 2 || completedIDs[0] != 28 || completedIDs[1] != 29 ||
		!gotReturnedAt.Equal(issuedAt.UTC()) || gotReturnedAt.Location() != time.UTC {
		t.Fatalf("CompleteMany() ids = %v returnedAt = %v", completedIDs, gotReturnedAt)
	}

	tooMany := make([]int64, MaxBulkSelection+1)
	for index := range tooMany {
		tooMany[index] = int64(index + 1)
	}
	invalidSelections := [][]int64{nil, {}, {0}, {-1}, {24, 24}, tooMany}
	for _, ids := range invalidSelections {
		if _, err := service.IssueMany(context.Background(), actor, ids); !errors.Is(err, ErrInvalidBulkSelection) {
			t.Errorf("IssueMany(%v) error = %v", ids, err)
		}
		if _, err := service.CancelMany(context.Background(), actor, ids); !errors.Is(err, ErrInvalidBulkSelection) {
			t.Errorf("CancelMany(%v) error = %v", ids, err)
		}
		if _, err := service.CompleteMany(context.Background(), actor, ids); !errors.Is(err, ErrInvalidBulkSelection) {
			t.Errorf("CompleteMany(%v) error = %v", ids, err)
		}
	}
	admin := user.User{ID: 1, Role: user.RoleAdmin, Active: true}
	if _, err := service.IssueMany(context.Background(), admin, []int64{24}); !errors.Is(err, user.ErrAccessDenied) {
		t.Errorf("IssueMany(admin) error = %v", err)
	}
	if _, err := service.CancelMany(context.Background(), admin, []int64{24}); !errors.Is(err, user.ErrAccessDenied) {
		t.Errorf("CancelMany(admin) error = %v", err)
	}
	if _, err := service.CompleteMany(context.Background(), admin, []int64{24}); !errors.Is(err, user.ErrAccessDenied) {
		t.Errorf("CompleteMany(admin) error = %v", err)
	}
}

func TestServicePropagatesRepositoryErrors(t *testing.T) {
	repositoryError := errors.New("repository unavailable")
	service := NewService(&serviceRepositoryStub{
		available: func(context.Context, Interval) ([]equipment.Item, error) { return nil, repositoryError },
		create: func(context.Context, user.User, int64, Interval, []ModelSelection) (Rental, error) {
			return Rental{}, repositoryError
		},
		issue:    func(context.Context, user.User, int64, time.Time) (Rental, error) { return Rental{}, repositoryError },
		cancel:   func(context.Context, user.User, int64) (Rental, error) { return Rental{}, repositoryError },
		complete: func(context.Context, user.User, int64, time.Time) (Rental, error) { return Rental{}, repositoryError },
		issueMany: func(context.Context, user.User, []int64, time.Time) ([]Rental, error) {
			return nil, repositoryError
		},
		cancelMany: func(context.Context, user.User, []int64) ([]Rental, error) {
			return nil, repositoryError
		},
		completeMany: func(context.Context, user.User, []int64, time.Time) ([]Rental, error) {
			return nil, repositoryError
		},
		get:  func(context.Context, int64) (Rental, error) { return Rental{}, repositoryError },
		list: func(context.Context, []Status, int, int) (Page, error) { return Page{}, repositoryError },
	})
	if _, err := service.AvailableModels(context.Background(), serviceInterval(t)); !errors.Is(err, repositoryError) {
		t.Errorf("AvailableModels() error = %v", err)
	}
	if _, err := service.CreateConfirmed(context.Background(), user.User{ID: 7, Role: user.RoleOperator, Active: true}, 18, serviceInterval(t), []ModelSelection{{ModelID: 10, Quantity: 1}}); !errors.Is(err, repositoryError) {
		t.Errorf("CreateConfirmed() error = %v", err)
	}
	if _, err := service.Issue(context.Background(), user.User{ID: 7, Role: user.RoleOperator, Active: true}, 1); !errors.Is(err, repositoryError) {
		t.Errorf("Issue() error = %v", err)
	}
	if _, err := service.Cancel(context.Background(), user.User{ID: 7, Role: user.RoleOperator, Active: true}, 1); !errors.Is(err, repositoryError) {
		t.Errorf("Cancel() error = %v", err)
	}
	if _, err := service.Complete(context.Background(), user.User{ID: 7, Role: user.RoleOperator, Active: true}, 1); !errors.Is(err, repositoryError) {
		t.Errorf("Complete() error = %v", err)
	}
	if _, err := service.IssueMany(context.Background(), user.User{ID: 7, Role: user.RoleOperator, Active: true}, []int64{1}); !errors.Is(err, repositoryError) {
		t.Errorf("IssueMany() error = %v", err)
	}
	if _, err := service.CancelMany(context.Background(), user.User{ID: 7, Role: user.RoleOperator, Active: true}, []int64{1}); !errors.Is(err, repositoryError) {
		t.Errorf("CancelMany() error = %v", err)
	}
	if _, err := service.CompleteMany(context.Background(), user.User{ID: 7, Role: user.RoleOperator, Active: true}, []int64{1}); !errors.Is(err, repositoryError) {
		t.Errorf("CompleteMany() error = %v", err)
	}
	if _, err := service.Get(context.Background(), 1); !errors.Is(err, repositoryError) {
		t.Errorf("Get() error = %v", err)
	}
	if _, err := service.ListPage(context.Background(), []Status{StatusConfirmed}, 1, 5); !errors.Is(err, repositoryError) {
		t.Errorf("ListPage() error = %v", err)
	}
}

func TestServiceListPageValidation(t *testing.T) {
	service := NewService(&serviceRepositoryStub{})
	for _, input := range [][2]int{{0, 5}, {1, 4}, {1, 20}} {
		if _, err := service.ListPage(context.Background(), []Status{StatusConfirmed}, input[0], input[1]); !errors.Is(err, ErrInvalidPage) {
			t.Errorf("ListPage(%d, %d) error = %v", input[0], input[1], err)
		}
	}
	for _, statuses := range [][]Status{nil, {}, {Status("draft")}, {StatusConfirmed, StatusConfirmed}} {
		if _, err := service.ListPage(context.Background(), statuses, 1, 5); !errors.Is(err, ErrInvalidPage) {
			t.Errorf("ListPage(%v) error = %v", statuses, err)
		}
	}
}

type serviceRepositoryStub struct {
	create       func(context.Context, user.User, int64, Interval, []ModelSelection) (Rental, error)
	issue        func(context.Context, user.User, int64, time.Time) (Rental, error)
	issueMany    func(context.Context, user.User, []int64, time.Time) ([]Rental, error)
	cancel       func(context.Context, user.User, int64) (Rental, error)
	cancelMany   func(context.Context, user.User, []int64) ([]Rental, error)
	complete     func(context.Context, user.User, int64, time.Time) (Rental, error)
	completeMany func(context.Context, user.User, []int64, time.Time) ([]Rental, error)
	get          func(context.Context, int64) (Rental, error)
	list         func(context.Context, []Status, int, int) (Page, error)
	monitoring   func(context.Context, MonitoringQuery) (MonitoringData, error)
	available    func(context.Context, Interval) ([]equipment.Item, error)
}

func (s *serviceRepositoryStub) CancelMany(ctx context.Context, actor user.User, ids []int64) ([]Rental, error) {
	if s.cancelMany == nil {
		return nil, ErrRentalNotFound
	}
	return s.cancelMany(ctx, actor, ids)
}

func (s *serviceRepositoryStub) Cancel(ctx context.Context, actor user.User, id int64) (Rental, error) {
	if s.cancel == nil {
		return Rental{}, ErrRentalNotFound
	}
	return s.cancel(ctx, actor, id)
}

func (s *serviceRepositoryStub) Complete(ctx context.Context, actor user.User, id int64, returnedAt time.Time) (Rental, error) {
	if s.complete == nil {
		return Rental{}, ErrRentalNotFound
	}
	return s.complete(ctx, actor, id, returnedAt)
}

func (s *serviceRepositoryStub) CompleteMany(ctx context.Context, actor user.User, ids []int64, returnedAt time.Time) ([]Rental, error) {
	if s.completeMany == nil {
		return nil, ErrRentalNotFound
	}
	return s.completeMany(ctx, actor, ids, returnedAt)
}

func (s *serviceRepositoryStub) Issue(ctx context.Context, actor user.User, id int64, issuedAt time.Time) (Rental, error) {
	if s.issue == nil {
		return Rental{}, ErrRentalNotFound
	}
	return s.issue(ctx, actor, id, issuedAt)
}

func (s *serviceRepositoryStub) IssueMany(ctx context.Context, actor user.User, ids []int64, issuedAt time.Time) ([]Rental, error) {
	if s.issueMany == nil {
		return nil, ErrRentalNotFound
	}
	return s.issueMany(ctx, actor, ids, issuedAt)
}

func (s *serviceRepositoryStub) CreateConfirmed(ctx context.Context, actor user.User, clientID int64, interval Interval, selections []ModelSelection) (Rental, error) {
	if s.create == nil {
		return Restore(1, clientID, interval, StatusConfirmed, nil, nil, []Item{rentalItemFixture(1)})
	}
	return s.create(ctx, actor, clientID, interval, selections)
}
func (s *serviceRepositoryStub) Get(ctx context.Context, id int64) (Rental, error) {
	if s.get == nil {
		return Rental{}, ErrRentalNotFound
	}
	return s.get(ctx, id)
}
func (s *serviceRepositoryStub) ListPage(ctx context.Context, statuses []Status, page, pageSize int) (Page, error) {
	if s.list == nil {
		return Page{Page: page, PageSize: pageSize}, nil
	}
	return s.list(ctx, statuses, page, pageSize)
}
func (s *serviceRepositoryStub) Monitoring(ctx context.Context, query MonitoringQuery) (MonitoringData, error) {
	if s.monitoring == nil {
		return MonitoringData{}, nil
	}
	return s.monitoring(ctx, query)
}
func (s *serviceRepositoryStub) AvailableEquipment(ctx context.Context, interval Interval) ([]equipment.Item, error) {
	if s.available == nil {
		return nil, nil
	}
	return s.available(ctx, interval)
}

func serviceInterval(t *testing.T) Interval {
	t.Helper()
	location := time.FixedZone("МСК", 3*60*60)
	interval, err := NewInterval(time.Date(2026, 8, 15, 10, 0, 0, 0, location), time.Date(2026, 8, 15, 11, 30, 0, 0, location))
	if err != nil {
		t.Fatalf("NewInterval() error = %v", err)
	}
	return interval
}

func equipmentFixture(id, modelID int64, kind equipment.Kind, modelCode string, rate int64) equipment.Item {
	prefix, _ := kind.InventoryPrefix()
	return equipment.Item{ID: id, ModelID: modelID, SequenceNumber: id, InventoryNumber: prefix + "-" + modelCode + "-" + strconv.FormatInt(id, 10), Kind: kind, ModelCode: modelCode, HourlyRateKopecks: rate, Status: equipment.StatusAvailable}
}

func rentalItemFixture(id int64) Item {
	return Item{EquipmentID: id, InventoryNumber: "SUP-TOURING-" + strconv.FormatInt(id, 10), Kind: equipment.KindSUPBoard, ModelCode: "TOURING", HourlyRateKopecks: 100_000}
}
