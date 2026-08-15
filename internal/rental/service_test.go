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
	service := NewService(&serviceRepositoryStub{
		available: func(context.Context, Interval) ([]equipment.Item, error) {
			return []equipment.Item{
				equipmentFixture(1, 10, equipment.KindSUPBoard, "TOURING", 100_000),
				equipmentFixture(2, 10, equipment.KindSUPBoard, "TOURING", 100_000),
				equipmentFixture(3, 20, equipment.KindPaddle, "CARBON", 35_000),
			}, nil
		},
	})

	models, err := service.AvailableModels(context.Background(), serviceInterval(t))
	if err != nil {
		t.Fatalf("AvailableModels() error = %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("len(models) = %d, want 2", len(models))
	}
	if models[0].ModelID != 10 || models[0].AvailableCount != 2 ||
		models[1].ModelID != 20 || models[1].AvailableCount != 1 {
		t.Errorf("models = %+v", models)
	}
}

func TestServiceCreateDraftBuildsSnapshotsFromRepository(t *testing.T) {
	var stored Rental
	repository := &serviceRepositoryStub{
		available: func(context.Context, Interval) ([]equipment.Item, error) {
			return []equipment.Item{
				equipmentFixture(1, 10, equipment.KindSUPBoard, "TOURING", 100_000),
				equipmentFixture(2, 10, equipment.KindSUPBoard, "TOURING", 100_000),
				equipmentFixture(3, 20, equipment.KindPaddle, "CARBON", 35_000),
			}, nil
		},
		create: func(_ context.Context, value Rental) (Rental, error) {
			stored = value
			return Restore(24, value.ClientID, value.Interval, value.Status, value.Items())
		},
	}
	service := NewService(repository)

	created, err := service.CreateDraft(
		context.Background(),
		user.User{ID: 7, Login: "operator", Role: user.RoleOperator, Active: true},
		18,
		serviceInterval(t),
		[]ModelSelection{{ModelID: 10, Quantity: 2}, {ModelID: 20, Quantity: 1}},
	)
	if err != nil {
		t.Fatalf("CreateDraft() error = %v", err)
	}
	if created.ID != 24 || stored.ClientID != 18 || stored.ItemCount() != 3 {
		t.Fatalf("created = %+v, stored items = %d", created, stored.ItemCount())
	}
	items := stored.Items()
	if items[0].InventoryNumber != "SUP-TOURING-1" ||
		items[2].InventoryNumber != "PADDLE-CARBON-3" {
		t.Errorf("stored items = %+v", items)
	}
}

func TestServiceCreateDraftRejectsAccessAndSelections(t *testing.T) {
	interval := serviceInterval(t)
	available := []equipment.Item{
		equipmentFixture(1, 10, equipment.KindSUPBoard, "TOURING", 100_000),
	}
	service := NewService(&serviceRepositoryStub{
		available: func(context.Context, Interval) ([]equipment.Item, error) {
			return available, nil
		},
	})
	operator := user.User{ID: 7, Role: user.RoleOperator, Active: true}

	tests := []struct {
		name       string
		actor      user.User
		selections []ModelSelection
		want       error
	}{
		{name: "admin", actor: user.User{ID: 1, Role: user.RoleAdmin, Active: true}, want: user.ErrAccessDenied},
		{name: "inactive", actor: user.User{ID: 7, Role: user.RoleOperator}, want: user.ErrAccessDenied},
		{name: "invalid model", actor: operator, selections: []ModelSelection{{ModelID: 0, Quantity: 1}}, want: ErrInvalidModelSelection},
		{name: "negative", actor: operator, selections: []ModelSelection{{ModelID: 10, Quantity: -1}}, want: ErrInvalidModelSelection},
		{name: "duplicate", actor: operator, selections: []ModelSelection{{ModelID: 10}, {ModelID: 10}}, want: ErrInvalidModelSelection},
		{name: "unavailable model", actor: operator, selections: []ModelSelection{{ModelID: 99, Quantity: 1}}, want: ErrInsufficientEquipment},
		{name: "insufficient", actor: operator, selections: []ModelSelection{{ModelID: 10, Quantity: 2}}, want: ErrInsufficientEquipment},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := service.CreateDraft(context.Background(), tt.actor, 18, interval, tt.selections)
			if !errors.Is(err, tt.want) {
				t.Fatalf("CreateDraft() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestServicePropagatesRepositoryErrors(t *testing.T) {
	repositoryError := errors.New("repository unavailable")
	service := NewService(&serviceRepositoryStub{
		available: func(context.Context, Interval) ([]equipment.Item, error) {
			return nil, repositoryError
		},
		get: func(context.Context, int64) (Rental, error) {
			return Rental{}, repositoryError
		},
		list: func(context.Context, int, int) (Page, error) {
			return Page{}, repositoryError
		},
	})

	if _, err := service.AvailableModels(context.Background(), serviceInterval(t)); !errors.Is(err, repositoryError) {
		t.Errorf("AvailableModels() error = %v", err)
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
	if got := AllowedPageSizes(); len(got) != 3 || got[0] != 5 || got[2] != 15 {
		t.Errorf("AllowedPageSizes() = %v", got)
	}
}

type serviceRepositoryStub struct {
	create    func(context.Context, Rental) (Rental, error)
	get       func(context.Context, int64) (Rental, error)
	list      func(context.Context, int, int) (Page, error)
	available func(context.Context, Interval) ([]equipment.Item, error)
}

func (s *serviceRepositoryStub) Create(ctx context.Context, value Rental) (Rental, error) {
	if s.create == nil {
		return Restore(1, value.ClientID, value.Interval, value.Status, value.Items())
	}
	return s.create(ctx, value)
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
	interval, err := NewInterval(
		time.Date(2026, time.August, 15, 10, 0, 0, 0, location),
		time.Date(2026, time.August, 15, 11, 30, 0, 0, location),
	)
	if err != nil {
		t.Fatalf("NewInterval() error = %v", err)
	}
	return interval
}

func equipmentFixture(id, modelID int64, kind equipment.Kind, modelCode string, rate int64) equipment.Item {
	prefix, _ := kind.InventoryPrefix()
	return equipment.Item{
		ID: id, ModelID: modelID, SequenceNumber: id,
		InventoryNumber: prefix + "-" + modelCode + "-" + strconv.FormatInt(id, 10),
		Kind:            kind, ModelCode: modelCode, HourlyRateKopecks: rate,
		Status: equipment.StatusAvailable,
	}
}
