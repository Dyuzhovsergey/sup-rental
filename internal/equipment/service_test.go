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
	}{
		{
			name:          "updates available equipment and trims number",
			currentStatus: StatusAvailable,
			input: UpdateInput{
				InventoryNumber: "  SUP-017-UPDATED  ",
				Kind:            KindLifeJacket,
			},
			wantNumber: "SUP-017-UPDATED",
			wantKind:   KindLifeJacket,
		},
		{
			name:          "updates equipment in maintenance",
			currentStatus: StatusMaintenance,
			input: UpdateInput{
				InventoryNumber: "PADDLE-017",
				Kind:            KindPaddle,
			},
			wantNumber: "PADDLE-017",
			wantKind:   KindPaddle,
		},
		{
			name:          "rejects empty inventory number",
			currentStatus: StatusAvailable,
			input: UpdateInput{
				InventoryNumber: " \t ",
				Kind:            KindPaddle,
			},
			wantErr: ErrInventoryNumberRequired,
		},
		{
			name:          "rejects invalid kind",
			currentStatus: StatusAvailable,
			input: UpdateInput{
				InventoryNumber: "SUP-017",
				Kind:            Kind("unknown"),
			},
			wantErr: ErrInvalidKind,
		},
		{
			name:          "rejects issued equipment",
			currentStatus: StatusIssued,
			input: UpdateInput{
				InventoryNumber: "SUP-017",
				Kind:            KindSUPBoard,
			},
			wantErr: ErrEquipmentUpdateNotAllowed,
		},
		{
			name:          "rejects retired equipment",
			currentStatus: StatusRetired,
			input: UpdateInput{
				InventoryNumber: "SUP-017",
				Kind:            KindSUPBoard,
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
			},
			updateErr: updateError,
			wantErr:   updateError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotNumber string
			var gotKind Kind
			repository := &repositoryStub{
				get: func(_ context.Context, _ int64) (Item, error) {
					if tt.getErr != nil {
						return Item{}, tt.getErr
					}

					return Item{ID: 17, Status: tt.currentStatus}, nil
				},
				updateDetails: func(
					_ context.Context,
					id int64,
					inventoryNumber string,
					kind Kind,
				) (Item, error) {
					gotNumber = inventoryNumber
					gotKind = kind
					if tt.updateErr != nil {
						return Item{}, tt.updateErr
					}

					return Item{
						ID:              id,
						InventoryNumber: inventoryNumber,
						Kind:            kind,
						Status:          tt.currentStatus,
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
				if repository.updateDetailsCalls != wantCalls {
					t.Errorf(
						"repository UpdateDetails() calls = %d, want %d",
						repository.updateDetailsCalls,
						wantCalls,
					)
				}
				return
			}

			if gotNumber != tt.wantNumber || gotKind != tt.wantKind {
				t.Errorf(
					"UpdateDetails() input = (%q, %q), want (%q, %q)",
					gotNumber,
					gotKind,
					tt.wantNumber,
					tt.wantKind,
				)
			}
			if got.InventoryNumber != tt.wantNumber || got.Kind != tt.wantKind {
				t.Errorf("Update() = %+v, want number %q and kind %q", got, tt.wantNumber, tt.wantKind)
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

type repositoryStub struct {
	create             func(context.Context, Item) (Item, error)
	list               func(context.Context) ([]Item, error)
	get                func(context.Context, int64) (Item, error)
	updateDetails      func(context.Context, int64, string, Kind) (Item, error)
	updateStatus       func(context.Context, int64, Status) (Item, error)
	createCalls        int
	updateDetailsCalls int
	updateStatusCalls  int
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

func (r *repositoryStub) UpdateDetails(
	ctx context.Context,
	id int64,
	inventoryNumber string,
	kind Kind,
) (Item, error) {
	r.updateDetailsCalls++
	return r.updateDetails(ctx, id, inventoryNumber, kind)
}

func (r *repositoryStub) UpdateStatus(
	ctx context.Context,
	id int64,
	status Status,
) (Item, error) {
	r.updateStatusCalls++
	return r.updateStatus(ctx, id, status)
}
