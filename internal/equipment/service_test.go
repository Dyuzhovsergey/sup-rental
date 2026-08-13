package equipment

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/Dyuzhovsergey/sup-rental/internal/user"
)

func TestNormalizeModelCode(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    string
		wantErr error
	}{
		{name: "uppercases and trims", value: "  fusion  ", want: "FUSION"},
		{name: "normalizes separators", value: "carbon__pro  12---x", want: "CARBON-PRO-12-X"},
		{name: "trims separators", value: "_-touring-_", want: "TOURING"},
		{name: "empty", value: " _- ", wantErr: ErrModelCodeRequired},
		{name: "cyrillic", value: "Карбон", wantErr: ErrInvalidModelCode},
		{name: "punctuation", value: "CARBON+", wantErr: ErrInvalidModelCode},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeModelCode(tt.value)
			if !errors.Is(err, tt.wantErr) || got != tt.want {
				t.Fatalf("NormalizeModelCode(%q) = %q, %v; want %q, %v", tt.value, got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestInventoryNumberPrefixes(t *testing.T) {
	tests := []struct {
		kind Kind
		want string
	}{
		{KindSUPBoard, "SUP-FUSION-3"},
		{KindPaddle, "PADDLE-FUSION-3"},
		{KindLifeJacket, "VEST-FUSION-3"},
	}
	for _, tt := range tests {
		got, err := InventoryNumber(tt.kind, "fusion", 3)
		if err != nil || got != tt.want {
			t.Errorf("InventoryNumber(%q) = %q, %v; want %q", tt.kind, got, err, tt.want)
		}
	}
	if _, err := InventoryNumber(Kind("unknown"), "FUSION", 1); !errors.Is(err, ErrInvalidKind) {
		t.Errorf("unknown kind error = %v, want ErrInvalidKind", err)
	}
}

func TestHourlyRateKopecksAndQuantity(t *testing.T) {
	if got, err := HourlyRateKopecks(500); err != nil || got != 50000 {
		t.Fatalf("HourlyRateKopecks(500) = %d, %v", got, err)
	}
	for _, value := range []int64{0, -1} {
		if _, err := HourlyRateKopecks(value); !errors.Is(err, ErrInvalidHourlyRate) {
			t.Errorf("HourlyRateKopecks(%d) error = %v", value, err)
		}
	}
	for _, quantity := range []int{1, 100} {
		if err := ValidateBatchQuantity(quantity); err != nil {
			t.Errorf("ValidateBatchQuantity(%d) = %v", quantity, err)
		}
	}
	for _, quantity := range []int{0, 101} {
		if err := ValidateBatchQuantity(quantity); !errors.Is(err, ErrInvalidBatchQuantity) {
			t.Errorf("ValidateBatchQuantity(%d) = %v", quantity, err)
		}
	}
}

func TestServiceCreateBatch(t *testing.T) {
	input := BatchCreateInput{Kind: KindPaddle, ModelCode: " carbon__pro ", HourlyRateRubles: 350, Quantity: 3}
	wantBatch := Batch{Items: []Item{{ID: 1}}, FirstInventoryNumber: "PADDLE-CARBON-PRO-1", LastInventoryNumber: "PADDLE-CARBON-PRO-3"}
	var gotInput BatchCreateInput
	repository := &repositoryStub{createBatch: func(_ context.Context, input BatchCreateInput) (Batch, error) {
		gotInput = input
		return wantBatch, nil
	}}
	got, err := NewService(repository).CreateBatch(context.Background(), equipmentAdminFixture(), input)
	if err != nil {
		t.Fatalf("CreateBatch() error = %v", err)
	}
	if !reflect.DeepEqual(got, wantBatch) {
		t.Errorf("CreateBatch() = %#v, want %#v", got, wantBatch)
	}
	if gotInput.ModelCode != "CARBON-PRO" {
		t.Errorf("repository model code = %q", gotInput.ModelCode)
	}
}

func TestServiceCreateBatchValidationAndRoles(t *testing.T) {
	tests := []struct {
		name    string
		actor   user.User
		input   BatchCreateInput
		wantErr error
	}{
		{name: "operator", actor: user.User{ID: 2, Role: user.RoleOperator, Active: true}, input: validBatchInput(), wantErr: user.ErrAccessDenied},
		{name: "invalid kind", actor: equipmentAdminFixture(), input: BatchCreateInput{Kind: "board", ModelCode: "X", HourlyRateRubles: 1, Quantity: 1}, wantErr: ErrInvalidKind},
		{name: "empty model", actor: equipmentAdminFixture(), input: BatchCreateInput{Kind: KindPaddle, HourlyRateRubles: 1, Quantity: 1}, wantErr: ErrModelCodeRequired},
		{name: "invalid model", actor: equipmentAdminFixture(), input: BatchCreateInput{Kind: KindPaddle, ModelCode: "весло", HourlyRateRubles: 1, Quantity: 1}, wantErr: ErrInvalidModelCode},
		{name: "invalid rate", actor: equipmentAdminFixture(), input: BatchCreateInput{Kind: KindPaddle, ModelCode: "X", Quantity: 1}, wantErr: ErrInvalidHourlyRate},
		{name: "invalid quantity", actor: equipmentAdminFixture(), input: BatchCreateInput{Kind: KindPaddle, ModelCode: "X", HourlyRateRubles: 1, Quantity: 101}, wantErr: ErrInvalidBatchQuantity},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			service := NewService(&repositoryStub{createBatch: func(context.Context, BatchCreateInput) (Batch, error) {
				called = true
				return Batch{}, nil
			}})
			_, err := service.CreateBatch(context.Background(), tt.actor, tt.input)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if called {
				t.Error("repository was called for invalid input")
			}
		})
	}
}

func TestServiceCreateBatchPreservesRepositoryErrors(t *testing.T) {
	repository := &repositoryStub{createBatch: func(context.Context, BatchCreateInput) (Batch, error) {
		return Batch{}, ErrModelRateConflict
	}}
	_, err := NewService(repository).CreateBatch(context.Background(), equipmentAdminFixture(), validBatchInput())
	if !errors.Is(err, ErrModelRateConflict) {
		t.Fatalf("error = %v, want ErrModelRateConflict", err)
	}
}

func TestServiceUpdateChangesOnlyStatus(t *testing.T) {
	before := equipmentFixture(StatusAvailable)
	repository := &repositoryStub{
		get: func(context.Context, int64) (Item, error) { return before, nil },
		updateStatus: func(_ context.Context, _ int64, status Status) (Item, error) {
			before.Status = status
			return before, nil
		},
	}
	got, err := NewService(repository).Update(context.Background(), equipmentAdminFixture(), before.ID, UpdateInput{Status: StatusMaintenance})
	if err != nil || got.Status != StatusMaintenance || got.InventoryNumber != before.InventoryNumber {
		t.Fatalf("Update() = %#v, %v", got, err)
	}
}

func TestServiceUpdateRejectsForbiddenStatuses(t *testing.T) {
	for _, test := range []struct {
		current, target Status
		want            error
	}{
		{StatusRetired, StatusAvailable, ErrEquipmentUpdateNotAllowed},
		{StatusIssued, StatusAvailable, ErrEquipmentUpdateNotAllowed},
		{StatusAvailable, StatusRetired, ErrStatusTransitionNotAllowed},
		{StatusAvailable, Status("broken"), ErrInvalidStatus},
	} {
		service := NewService(&repositoryStub{get: func(context.Context, int64) (Item, error) {
			return equipmentFixture(test.current), nil
		}})
		_, err := service.Update(context.Background(), equipmentAdminFixture(), 17, UpdateInput{Status: test.target})
		if !errors.Is(err, test.want) {
			t.Errorf("Update(%s -> %s) error = %v, want %v", test.current, test.target, err, test.want)
		}
	}
}

func TestServiceChangeModelNormalizesAndMovesEquipment(t *testing.T) {
	before := equipmentFixture(StatusAvailable)
	var gotInput ModelChangeInput
	repository := &repositoryStub{
		get: func(context.Context, int64) (Item, error) { return before, nil },
		changeModel: func(_ context.Context, _ int64, input ModelChangeInput) (Item, error) {
			gotInput = input
			return Item{
				ID: before.ID, InventoryNumber: "VEST-TOURING-PRO-2",
				ModelID: 3, ModelCode: "TOURING-PRO", SequenceNumber: 2,
				Kind: KindLifeJacket, HourlyRateKopecks: 25000, Status: before.Status,
			}, nil
		},
	}

	got, err := NewService(repository).ChangeModel(
		context.Background(), equipmentAdminFixture(), before.ID,
		ModelChangeInput{Kind: KindLifeJacket, ModelCode: " touring__pro ", HourlyRateRubles: 250},
	)
	if err != nil {
		t.Fatalf("ChangeModel() error = %v", err)
	}
	if got.InventoryNumber != "VEST-TOURING-PRO-2" || got.Status != before.Status {
		t.Errorf("ChangeModel() = %#v", got)
	}
	if gotInput.ModelCode != "TOURING-PRO" {
		t.Errorf("repository model code = %q, want TOURING-PRO", gotInput.ModelCode)
	}
}

func TestServiceChangeModelValidation(t *testing.T) {
	tests := []struct {
		name    string
		item    Item
		input   ModelChangeInput
		wantErr error
	}{
		{name: "invalid kind", item: equipmentFixture(StatusAvailable), input: ModelChangeInput{Kind: "board", ModelCode: "X", HourlyRateRubles: 1}, wantErr: ErrInvalidKind},
		{name: "invalid model", item: equipmentFixture(StatusAvailable), input: ModelChangeInput{Kind: KindPaddle, ModelCode: "модель", HourlyRateRubles: 1}, wantErr: ErrInvalidModelCode},
		{name: "invalid rate", item: equipmentFixture(StatusAvailable), input: ModelChangeInput{Kind: KindPaddle, ModelCode: "X"}, wantErr: ErrInvalidHourlyRate},
		{name: "issued", item: equipmentFixture(StatusIssued), input: ModelChangeInput{Kind: KindPaddle, ModelCode: "X", HourlyRateRubles: 1}, wantErr: ErrEquipmentUpdateNotAllowed},
		{name: "retired", item: equipmentFixture(StatusRetired), input: ModelChangeInput{Kind: KindPaddle, ModelCode: "X", HourlyRateRubles: 1}, wantErr: ErrEquipmentUpdateNotAllowed},
		{name: "unchanged", item: equipmentFixture(StatusAvailable), input: ModelChangeInput{Kind: KindSUPBoard, ModelCode: "fusion", HourlyRateRubles: 500}, wantErr: ErrEquipmentModelUnchanged},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			service := NewService(&repositoryStub{
				get: func(context.Context, int64) (Item, error) { return tt.item, nil },
				changeModel: func(context.Context, int64, ModelChangeInput) (Item, error) {
					called = true
					return Item{}, nil
				},
			})
			_, err := service.ChangeModel(context.Background(), equipmentAdminFixture(), 17, tt.input)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if called {
				t.Error("repository mutation was called")
			}
		})
	}
}

func TestServiceChangeModelRate(t *testing.T) {
	before := equipmentFixture(StatusMaintenance)
	repository := &repositoryStub{
		get: func(context.Context, int64) (Item, error) { return before, nil },
		changeModelRate: func(_ context.Context, _ int64, rate int64) (ModelRateChange, error) {
			if rate != 65000 {
				t.Errorf("repository rate = %d, want 65000", rate)
			}
			before.HourlyRateKopecks = rate
			return ModelRateChange{Item: before, AffectedItems: 4}, nil
		},
	}
	got, err := NewService(repository).ChangeModelRate(
		context.Background(), equipmentAdminFixture(), before.ID, 650,
	)
	if err != nil || got.Item.HourlyRateKopecks != 65000 || got.AffectedItems != 4 {
		t.Fatalf("ChangeModelRate() = %#v, %v", got, err)
	}
}

func TestServiceChangeModelRateValidation(t *testing.T) {
	for _, tt := range []struct {
		name    string
		item    Item
		rate    int64
		wantErr error
	}{
		{name: "invalid rate", item: equipmentFixture(StatusAvailable), rate: 0, wantErr: ErrInvalidHourlyRate},
		{name: "issued", item: equipmentFixture(StatusIssued), rate: 600, wantErr: ErrEquipmentUpdateNotAllowed},
		{name: "retired", item: equipmentFixture(StatusRetired), rate: 600, wantErr: ErrEquipmentUpdateNotAllowed},
		{name: "unchanged", item: equipmentFixture(StatusAvailable), rate: 500, wantErr: ErrModelRateUnchanged},
	} {
		t.Run(tt.name, func(t *testing.T) {
			service := NewService(&repositoryStub{get: func(context.Context, int64) (Item, error) {
				return tt.item, nil
			}})
			_, err := service.ChangeModelRate(context.Background(), equipmentAdminFixture(), 17, tt.rate)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestServiceListPageValidation(t *testing.T) {
	service := NewService(&repositoryStub{})
	for _, input := range []ListPageInput{
		{Scope: "unknown", Page: 1, PageSize: 5},
		{Scope: ListScopeActive, Page: 0, PageSize: 5},
		{Scope: ListScopeActive, Page: 1, PageSize: 20},
	} {
		if _, err := service.ListPage(context.Background(), input); err == nil {
			t.Errorf("ListPage(%#v) returned nil error", input)
		}
	}
}

func TestServiceDeleteRequiresRetiredEquipment(t *testing.T) {
	service := NewService(&repositoryStub{get: func(context.Context, int64) (Item, error) {
		return equipmentFixture(StatusAvailable), nil
	}})
	if _, err := service.Delete(context.Background(), equipmentAdminFixture(), 17); !errors.Is(err, ErrEquipmentDeleteNotAllowed) {
		t.Fatalf("Delete() error = %v", err)
	}
}

func TestServiceMutationsRequireActiveAdmin(t *testing.T) {
	actors := []user.User{
		{},
		{ID: 2, Role: user.RoleOperator, Active: true},
		{ID: 1, Role: user.RoleAdmin, Active: false},
	}
	for _, actor := range actors {
		service := NewService(&repositoryStub{})
		if _, err := service.Update(context.Background(), actor, 17, UpdateInput{Status: StatusMaintenance}); !errors.Is(err, user.ErrAccessDenied) {
			t.Errorf("Update actor %#v error = %v", actor, err)
		}
		if _, err := service.ChangeStatus(context.Background(), actor, 17, StatusRetired); !errors.Is(err, user.ErrAccessDenied) {
			t.Errorf("ChangeStatus actor %#v error = %v", actor, err)
		}
		if _, err := service.Delete(context.Background(), actor, 17); !errors.Is(err, user.ErrAccessDenied) {
			t.Errorf("Delete actor %#v error = %v", actor, err)
		}
		if _, err := service.ChangeModel(context.Background(), actor, 17, ModelChangeInput{}); !errors.Is(err, user.ErrAccessDenied) {
			t.Errorf("ChangeModel actor %#v error = %v", actor, err)
		}
		if _, err := service.ChangeModelRate(context.Background(), actor, 17, 500); !errors.Is(err, user.ErrAccessDenied) {
			t.Errorf("ChangeModelRate actor %#v error = %v", actor, err)
		}
	}
}

func TestServicePreservesRepositoryErrors(t *testing.T) {
	infrastructureErr := errors.New("database unavailable")
	service := NewService(&repositoryStub{get: func(context.Context, int64) (Item, error) {
		return Item{}, infrastructureErr
	}})
	if _, err := service.Get(context.Background(), 17); !errors.Is(err, infrastructureErr) {
		t.Errorf("Get() error = %v", err)
	}
	if _, err := service.Update(context.Background(), equipmentAdminFixture(), 17, UpdateInput{Status: StatusMaintenance}); !errors.Is(err, infrastructureErr) {
		t.Errorf("Update() error = %v", err)
	}
	if _, err := service.ChangeStatus(context.Background(), equipmentAdminFixture(), 17, StatusRetired); !errors.Is(err, infrastructureErr) {
		t.Errorf("ChangeStatus() error = %v", err)
	}
	if _, err := service.Delete(context.Background(), equipmentAdminFixture(), 17); !errors.Is(err, infrastructureErr) {
		t.Errorf("Delete() error = %v", err)
	}
}

func validBatchInput() BatchCreateInput {
	return BatchCreateInput{Kind: KindSUPBoard, ModelCode: "FUSION", HourlyRateRubles: 500, Quantity: 3}
}

func equipmentFixture(status Status) Item {
	return Item{ID: 17, InventoryNumber: "SUP-FUSION-1", ModelID: 2, ModelCode: "FUSION", SequenceNumber: 1, Kind: KindSUPBoard, HourlyRateKopecks: 50000, Status: status}
}

func equipmentAdminFixture() user.User {
	return user.User{ID: 1, Login: "admin", Role: user.RoleAdmin, Active: true}
}

type repositoryStub struct {
	createBatch     func(context.Context, BatchCreateInput) (Batch, error)
	list            func(context.Context) ([]Item, error)
	listPage        func(context.Context, ListPageInput) (ListPage, error)
	get             func(context.Context, int64) (Item, error)
	updateStatus    func(context.Context, int64, Status) (Item, error)
	delete          func(context.Context, int64) (Item, error)
	changeModel     func(context.Context, int64, ModelChangeInput) (Item, error)
	changeModelRate func(context.Context, int64, int64) (ModelRateChange, error)
}

func (r *repositoryStub) CreateBatch(ctx context.Context, _ user.User, input BatchCreateInput) (Batch, error) {
	if r.createBatch == nil {
		return Batch{}, nil
	}
	return r.createBatch(ctx, input)
}
func (r *repositoryStub) List(ctx context.Context) ([]Item, error) {
	if r.list == nil {
		return []Item{}, nil
	}
	return r.list(ctx)
}
func (r *repositoryStub) ListPage(ctx context.Context, input ListPageInput) (ListPage, error) {
	if r.listPage == nil {
		return ListPage{Scope: input.Scope, Page: input.Page, PageSize: input.PageSize}, nil
	}
	return r.listPage(ctx, input)
}
func (r *repositoryStub) Get(ctx context.Context, id int64) (Item, error) {
	if r.get == nil {
		return Item{}, ErrEquipmentNotFound
	}
	return r.get(ctx, id)
}
func (r *repositoryStub) UpdateStatus(ctx context.Context, _ user.User, id int64, status Status) (Item, error) {
	if r.updateStatus == nil {
		return Item{}, nil
	}
	return r.updateStatus(ctx, id, status)
}
func (r *repositoryStub) ChangeModel(ctx context.Context, _ user.User, id int64, input ModelChangeInput) (Item, error) {
	if r.changeModel == nil {
		return Item{}, nil
	}
	return r.changeModel(ctx, id, input)
}
func (r *repositoryStub) ChangeModelRate(ctx context.Context, _ user.User, id int64, hourlyRateKopecks int64) (ModelRateChange, error) {
	if r.changeModelRate == nil {
		return ModelRateChange{}, nil
	}
	return r.changeModelRate(ctx, id, hourlyRateKopecks)
}
func (r *repositoryStub) Delete(ctx context.Context, _ user.User, id int64) (Item, error) {
	if r.delete == nil {
		return Item{}, nil
	}
	return r.delete(ctx, id)
}
