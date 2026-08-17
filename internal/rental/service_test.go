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
		return Restore(24, clientID, interval, StatusConfirmed, []Item{rentalItemFixture(1)})
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

func TestServicePropagatesRepositoryErrors(t *testing.T) {
	repositoryError := errors.New("repository unavailable")
	service := NewService(&serviceRepositoryStub{
		available: func(context.Context, Interval) ([]equipment.Item, error) { return nil, repositoryError },
		create: func(context.Context, user.User, int64, Interval, []ModelSelection) (Rental, error) {
			return Rental{}, repositoryError
		},
		get:  func(context.Context, int64) (Rental, error) { return Rental{}, repositoryError },
		list: func(context.Context, int, int) (Page, error) { return Page{}, repositoryError },
	})
	if _, err := service.AvailableModels(context.Background(), serviceInterval(t)); !errors.Is(err, repositoryError) {
		t.Errorf("AvailableModels() error = %v", err)
	}
	if _, err := service.CreateConfirmed(context.Background(), user.User{ID: 7, Role: user.RoleOperator, Active: true}, 18, serviceInterval(t), []ModelSelection{{ModelID: 10, Quantity: 1}}); !errors.Is(err, repositoryError) {
		t.Errorf("CreateConfirmed() error = %v", err)
	}
	if _, err := service.Get(context.Background(), 1); !errors.Is(err, repositoryError) {
		t.Errorf("Get() error = %v", err)
	}
	if _, err := service.ListPage(context.Background(), 1, 5); !errors.Is(err, repositoryError) {
		t.Errorf("ListPage() error = %v", err)
	}
}

func TestServiceListPageValidation(t *testing.T) {
	service := NewService(&serviceRepositoryStub{})
	for _, input := range [][2]int{{0, 5}, {1, 4}, {1, 20}} {
		if _, err := service.ListPage(context.Background(), input[0], input[1]); !errors.Is(err, ErrInvalidPage) {
			t.Errorf("ListPage(%d, %d) error = %v", input[0], input[1], err)
		}
	}
}

type serviceRepositoryStub struct {
	create    func(context.Context, user.User, int64, Interval, []ModelSelection) (Rental, error)
	get       func(context.Context, int64) (Rental, error)
	list      func(context.Context, int, int) (Page, error)
	available func(context.Context, Interval) ([]equipment.Item, error)
}

func (s *serviceRepositoryStub) CreateConfirmed(ctx context.Context, actor user.User, clientID int64, interval Interval, selections []ModelSelection) (Rental, error) {
	if s.create == nil {
		return Restore(1, clientID, interval, StatusConfirmed, []Item{rentalItemFixture(1)})
	}
	return s.create(ctx, actor, clientID, interval, selections)
}
func (s *serviceRepositoryStub) Get(ctx context.Context, id int64) (Rental, error) {
	if s.get == nil {
		return Rental{}, ErrRentalNotFound
	}
	return s.get(ctx, id)
}
func (s *serviceRepositoryStub) ListPage(ctx context.Context, page, pageSize int) (Page, error) {
	if s.list == nil {
		return Page{Page: page, PageSize: pageSize}, nil
	}
	return s.list(ctx, page, pageSize)
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
