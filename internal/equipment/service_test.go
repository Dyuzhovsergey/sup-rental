package equipment

import (
	"context"
	"errors"
	"testing"
)

func TestServiceCreate(t *testing.T) {
	tests := []struct {
		name    string
		input   CreateInput
		want    Item
		wantErr error
	}{
		{
			name: "creates SUP board",
			input: CreateInput{
				InventoryNumber: "  SUP-001  ",
				Kind:            KindSUPBoard,
			},
			want: Item{
				ID:              1,
				InventoryNumber: "SUP-001",
				Kind:            KindSUPBoard,
				Status:          StatusAvailable,
			},
		},
		{
			name: "rejects empty inventory number",
			input: CreateInput{
				InventoryNumber: " \t ",
				Kind:            KindPaddle,
			},
			wantErr: ErrInventoryNumberRequired,
		},
		{
			name: "rejects invalid kind",
			input: CreateInput{
				InventoryNumber: "ITEM-001",
				Kind:            Kind("unknown"),
			},
			wantErr: ErrInvalidKind,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repository := &repositoryStub{
				create: func(_ context.Context, item Item) (Item, error) {
					item.ID = 1
					return item, nil
				},
			}
			service := NewService(repository)

			got, err := service.Create(context.Background(), tt.input)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Create() error = %v, want %v", err, tt.wantErr)
			}

			if tt.wantErr != nil {
				if repository.createCalls != 0 {
					t.Errorf("repository Create() calls = %d, want 0", repository.createCalls)
				}
				return
			}

			if got != tt.want {
				t.Errorf("Create() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestServiceCreatePreservesDuplicateError(t *testing.T) {
	repository := &repositoryStub{
		create: func(_ context.Context, _ Item) (Item, error) {
			return Item{}, ErrInventoryNumberExists
		},
	}
	service := NewService(repository)

	_, err := service.Create(context.Background(), CreateInput{
		InventoryNumber: "SUP-001",
		Kind:            KindSUPBoard,
	})
	if !errors.Is(err, ErrInventoryNumberExists) {
		t.Fatalf("Create() error = %v, want ErrInventoryNumberExists", err)
	}
}

func TestServiceList(t *testing.T) {
	want := []Item{{
		ID:              1,
		InventoryNumber: "SUP-001",
		Kind:            KindSUPBoard,
		Status:          StatusAvailable,
	}}
	repository := &repositoryStub{
		list: func(_ context.Context) ([]Item, error) {
			return want, nil
		},
	}
	service := NewService(repository)

	got, err := service.List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	if len(got) != len(want) || got[0] != want[0] {
		t.Errorf("List() = %+v, want %+v", got, want)
	}
}

func TestStatusCanEditDetails(t *testing.T) {
	tests := []struct {
		status Status
		want   bool
	}{
		{status: StatusAvailable, want: true},
		{status: StatusMaintenance, want: true},
		{status: StatusIssued, want: false},
		{status: StatusRetired, want: false},
		{status: Status("unknown"), want: false},
	}

	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.CanEditDetails(); got != tt.want {
				t.Errorf("CanEditDetails() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestServiceGet(t *testing.T) {
	want := Item{
		ID:              17,
		InventoryNumber: "SUP-017",
		Kind:            KindSUPBoard,
		Status:          StatusAvailable,
	}
	repository := &repositoryStub{
		get: func(_ context.Context, _ int64) (Item, error) {
			return want, nil
		},
	}

	got, err := NewService(repository).Get(context.Background(), want.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got != want {
		t.Errorf("Get() = %+v, want %+v", got, want)
	}
}

func TestServiceUpdate(t *testing.T) {
	updateError := errors.New("update failed")
	tests := []struct {
		name          string
		currentStatus Status
		input         UpdateInput
		getErr        error
		updateErr     error
		wantErr       error
		wantNumber    string
		wantKind      Kind
		wantStatus    Status
	}{
		{
			name:          "updates available equipment and trims number",
			currentStatus: StatusAvailable,
			input: UpdateInput{
				InventoryNumber: "  SUP-017-UPDATED  ",
				Kind:            KindLifeJacket,
				Status:          StatusMaintenance,
			},
			wantNumber: "SUP-017-UPDATED",
			wantKind:   KindLifeJacket,
			wantStatus: StatusMaintenance,
		},
		{
			name:          "updates equipment in maintenance",
			currentStatus: StatusMaintenance,
			input: UpdateInput{
				InventoryNumber: "PADDLE-017",
				Kind:            KindPaddle,
				Status:          StatusAvailable,
			},
			wantNumber: "PADDLE-017",
			wantKind:   KindPaddle,
			wantStatus: StatusAvailable,
		},
		{
			name:          "rejects empty inventory number",
			currentStatus: StatusAvailable,
			input: UpdateInput{
				InventoryNumber: " \t ",
				Kind:            KindPaddle,
				Status:          StatusAvailable,
			},
			wantErr: ErrInventoryNumberRequired,
		},
		{
			name:          "rejects invalid kind",
			currentStatus: StatusAvailable,
			input: UpdateInput{
				InventoryNumber: "SUP-017",
				Kind:            Kind("unknown"),
				Status:          StatusAvailable,
			},
			wantErr: ErrInvalidKind,
		},
		{
			name:          "rejects invalid status",
			currentStatus: StatusAvailable,
			input: UpdateInput{
				InventoryNumber: "SUP-017",
				Kind:            KindSUPBoard,
				Status:          Status("unknown"),
			},
			wantErr: ErrInvalidStatus,
		},
		{
			name:          "requires separate retirement confirmation",
			currentStatus: StatusAvailable,
			input: UpdateInput{
				InventoryNumber: "SUP-017",
				Kind:            KindSUPBoard,
				Status:          StatusRetired,
			},
			wantErr: ErrStatusTransitionNotAllowed,
		},
		{
			name:          "rejects issued equipment",
			currentStatus: StatusIssued,
			input: UpdateInput{
				InventoryNumber: "SUP-017",
				Kind:            KindSUPBoard,
				Status:          StatusAvailable,
			},
			wantErr: ErrEquipmentUpdateNotAllowed,
		},
		{
			name:          "rejects retired equipment",
			currentStatus: StatusRetired,
			input: UpdateInput{
				InventoryNumber: "SUP-017",
				Kind:            KindSUPBoard,
				Status:          StatusAvailable,
			},
			wantErr: ErrEquipmentUpdateNotAllowed,
		},
		{
			name:    "returns not found error",
			getErr:  ErrEquipmentNotFound,
			wantErr: ErrEquipmentNotFound,
		},
		{
			name:          "preserves duplicate error",
			currentStatus: StatusAvailable,
			input: UpdateInput{
				InventoryNumber: "SUP-001",
				Kind:            KindSUPBoard,
				Status:          StatusAvailable,
			},
			updateErr: ErrInventoryNumberExists,
			wantErr:   ErrInventoryNumberExists,
		},
		{
			name:          "preserves repository error",
			currentStatus: StatusAvailable,
			input: UpdateInput{
				InventoryNumber: "SUP-017",
				Kind:            KindSUPBoard,
				Status:          StatusAvailable,
			},
			updateErr: updateError,
			wantErr:   updateError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotNumber string
			var gotKind Kind
			var gotStatus Status
			repository := &repositoryStub{
				get: func(_ context.Context, _ int64) (Item, error) {
					if tt.getErr != nil {
						return Item{}, tt.getErr
					}

					return Item{ID: 17, Status: tt.currentStatus}, nil
				},
				update: func(
					_ context.Context,
					id int64,
					inventoryNumber string,
					kind Kind,
					status Status,
				) (Item, error) {
					gotNumber = inventoryNumber
					gotKind = kind
					gotStatus = status
					if tt.updateErr != nil {
						return Item{}, tt.updateErr
					}

					return Item{
						ID:              id,
						InventoryNumber: inventoryNumber,
						Kind:            kind,
						Status:          status,
					}, nil
				},
			}

			got, err := NewService(repository).Update(context.Background(), 17, tt.input)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Update() error = %v, want %v", err, tt.wantErr)
			}

			if tt.wantErr != nil {
				wantCalls := 0
				if tt.updateErr != nil {
					wantCalls = 1
				}
				if repository.updateCalls != wantCalls {
					t.Errorf(
						"repository Update() calls = %d, want %d",
						repository.updateCalls,
						wantCalls,
					)
				}
				return
			}

			if gotNumber != tt.wantNumber || gotKind != tt.wantKind || gotStatus != tt.wantStatus {
				t.Errorf(
					"Update() input = (%q, %q, %q), want (%q, %q, %q)",
					gotNumber,
					gotKind,
					gotStatus,
					tt.wantNumber,
					tt.wantKind,
					tt.wantStatus,
				)
			}
			if got.InventoryNumber != tt.wantNumber ||
				got.Kind != tt.wantKind ||
				got.Status != tt.wantStatus {
				t.Errorf(
					"Update() = %+v, want number %q, kind %q and status %q",
					got,
					tt.wantNumber,
					tt.wantKind,
					tt.wantStatus,
				)
			}
		})
	}
}

func TestServiceChangeStatus(t *testing.T) {
	tests := []struct {
		name          string
		currentStatus Status
		target        Status
		getErr        error
		updateErr     error
		wantErr       error
		wantStatus    Status
	}{
		{
			name:          "sends available equipment to maintenance",
			currentStatus: StatusAvailable,
			target:        StatusMaintenance,
			wantStatus:    StatusMaintenance,
		},
		{
			name:          "returns equipment from maintenance",
			currentStatus: StatusMaintenance,
			target:        StatusAvailable,
			wantStatus:    StatusAvailable,
		},
		{
			name:          "retires available equipment",
			currentStatus: StatusAvailable,
			target:        StatusRetired,
			wantStatus:    StatusRetired,
		},
		{
			name:          "retires equipment from maintenance",
			currentStatus: StatusMaintenance,
			target:        StatusRetired,
			wantStatus:    StatusRetired,
		},
		{
			name:          "does not manually issue equipment",
			currentStatus: StatusAvailable,
			target:        StatusIssued,
			wantErr:       ErrStatusTransitionNotAllowed,
		},
		{
			name:          "does not change issued equipment",
			currentStatus: StatusIssued,
			target:        StatusAvailable,
			wantErr:       ErrStatusTransitionNotAllowed,
		},
		{
			name:          "does not restore retired equipment",
			currentStatus: StatusRetired,
			target:        StatusAvailable,
			wantErr:       ErrStatusTransitionNotAllowed,
		},
		{
			name:          "rejects unknown target status",
			currentStatus: StatusAvailable,
			target:        Status("unknown"),
			wantErr:       ErrInvalidStatus,
		},
		{
			name:    "returns not found error",
			target:  StatusMaintenance,
			getErr:  ErrEquipmentNotFound,
			wantErr: ErrEquipmentNotFound,
		},
		{
			name:          "preserves repository update error",
			currentStatus: StatusAvailable,
			target:        StatusMaintenance,
			updateErr:     errors.New("update failed"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repository := &repositoryStub{
				get: func(_ context.Context, _ int64) (Item, error) {
					if tt.getErr != nil {
						return Item{}, tt.getErr
					}

					return Item{ID: 1, Status: tt.currentStatus}, nil
				},
				updateStatus: func(_ context.Context, id int64, status Status) (Item, error) {
					if tt.updateErr != nil {
						return Item{}, tt.updateErr
					}

					return Item{ID: id, Status: status}, nil
				},
			}
			service := NewService(repository)

			got, err := service.ChangeStatus(context.Background(), 1, tt.target)
			if tt.updateErr != nil {
				if !errors.Is(err, tt.updateErr) {
					t.Fatalf("ChangeStatus() error = %v, want %v", err, tt.updateErr)
				}
				return
			}

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ChangeStatus() error = %v, want %v", err, tt.wantErr)
			}

			if tt.wantErr != nil {
				if repository.updateStatusCalls != 0 {
					t.Errorf("repository UpdateStatus() calls = %d, want 0", repository.updateStatusCalls)
				}
				return
			}

			if got.Status != tt.wantStatus {
				t.Errorf("ChangeStatus() Status = %q, want %q", got.Status, tt.wantStatus)
			}
		})
	}
}

func TestServiceDelete(t *testing.T) {
	deleteError := errors.New("delete failed")
	tests := []struct {
		name          string
		currentStatus Status
		getErr        error
		deleteErr     error
		wantErr       error
		wantCalls     int
	}{
		{
			name:          "deletes retired equipment",
			currentStatus: StatusRetired,
			wantCalls:     1,
		},
		{
			name:          "rejects available equipment",
			currentStatus: StatusAvailable,
			wantErr:       ErrEquipmentDeleteNotAllowed,
		},
		{
			name:          "rejects equipment under maintenance",
			currentStatus: StatusMaintenance,
			wantErr:       ErrEquipmentDeleteNotAllowed,
		},
		{
			name:          "rejects issued equipment",
			currentStatus: StatusIssued,
			wantErr:       ErrEquipmentDeleteNotAllowed,
		},
		{
			name:    "returns not found error",
			getErr:  ErrEquipmentNotFound,
			wantErr: ErrEquipmentNotFound,
		},
		{
			name:          "preserves repository error",
			currentStatus: StatusRetired,
			deleteErr:     deleteError,
			wantErr:       deleteError,
			wantCalls:     1,
		},
		{
			name:          "preserves history conflict",
			currentStatus: StatusRetired,
			deleteErr:     ErrEquipmentHasHistory,
			wantErr:       ErrEquipmentHasHistory,
			wantCalls:     1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := Item{
				ID:              17,
				InventoryNumber: "SUP-017",
				Kind:            KindSUPBoard,
				Status:          tt.currentStatus,
			}
			repository := &repositoryStub{
				get: func(_ context.Context, _ int64) (Item, error) {
					if tt.getErr != nil {
						return Item{}, tt.getErr
					}

					return item, nil
				},
				delete: func(_ context.Context, id int64) error {
					if id != 17 {
						t.Errorf("Delete() ID = %d, want 17", id)
					}
					return tt.deleteErr
				},
			}

			got, err := NewService(repository).Delete(context.Background(), 17)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Delete() error = %v, want %v", err, tt.wantErr)
			}
			if repository.deleteCalls != tt.wantCalls {
				t.Errorf(
					"repository Delete() calls = %d, want %d",
					repository.deleteCalls,
					tt.wantCalls,
				)
			}
			if tt.wantErr == nil && got != item {
				t.Errorf("Delete() = %+v, want %+v", got, item)
			}
		})
	}
}

type repositoryStub struct {
	create            func(context.Context, Item) (Item, error)
	list              func(context.Context) ([]Item, error)
	get               func(context.Context, int64) (Item, error)
	update            func(context.Context, int64, string, Kind, Status) (Item, error)
	updateStatus      func(context.Context, int64, Status) (Item, error)
	delete            func(context.Context, int64) error
	createCalls       int
	updateCalls       int
	updateStatusCalls int
	deleteCalls       int
}

func (r *repositoryStub) Create(ctx context.Context, item Item) (Item, error) {
	r.createCalls++
	return r.create(ctx, item)
}

func (r *repositoryStub) List(ctx context.Context) ([]Item, error) {
	return r.list(ctx)
}

func (r *repositoryStub) Get(ctx context.Context, id int64) (Item, error) {
	return r.get(ctx, id)
}

func (r *repositoryStub) Update(
	ctx context.Context,
	id int64,
	inventoryNumber string,
	kind Kind,
	status Status,
) (Item, error) {
	r.updateCalls++
	return r.update(ctx, id, inventoryNumber, kind, status)
}

func (r *repositoryStub) UpdateStatus(
	ctx context.Context,
	id int64,
	status Status,
) (Item, error) {
	r.updateStatusCalls++
	return r.updateStatus(ctx, id, status)
}

func (r *repositoryStub) Delete(ctx context.Context, id int64) error {
	r.deleteCalls++
	return r.delete(ctx, id)
}
