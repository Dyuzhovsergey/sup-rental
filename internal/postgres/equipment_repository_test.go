package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Dyuzhovsergey/sup-rental/internal/equipment"
)

func TestEquipmentRepositoryCreateAndList(t *testing.T) {
	connectionString := os.Getenv("TEST_DATABASE_URL")
	if connectionString == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := Open(ctx, connectionString)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(pool.Close)

	repository := NewEquipmentRepository(pool)
	inventoryNumber := fmt.Sprintf("TEST-%d", time.Now().UnixNano())

	created, err := repository.Create(ctx, equipment.Item{
		InventoryNumber: inventoryNumber,
		Kind:            equipment.KindSUPBoard,
		Status:          equipment.StatusAvailable,
	})
	if err != nil {
		t.Fatalf("Create() error = %v; apply migrations to TEST_DATABASE_URL first", err)
	}

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()

		if _, err := pool.Exec(cleanupCtx, "DELETE FROM equipment WHERE id = $1", created.ID); err != nil {
			t.Errorf("clean up equipment: %v", err)
		}
	})

	if created.ID == 0 {
		t.Error("Create() ID = 0, want generated ID")
	}
	if created.InventoryNumber != inventoryNumber {
		t.Errorf(
			"Create() InventoryNumber = %q, want %q",
			created.InventoryNumber,
			inventoryNumber,
		)
	}

	updated, err := repository.UpdateStatus(ctx, created.ID, equipment.StatusMaintenance)
	if err != nil {
		t.Fatalf("UpdateStatus() error = %v", err)
	}
	if updated.Status != equipment.StatusMaintenance {
		t.Errorf(
			"UpdateStatus() Status = %q, want %q",
			updated.Status,
			equipment.StatusMaintenance,
		)
	}
	created = updated

	gotByID, err := repository.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if gotByID != updated {
		t.Errorf("Get() = %+v, want %+v", gotByID, updated)
	}

	_, err = repository.Create(ctx, equipment.Item{
		InventoryNumber: strings.ToLower(inventoryNumber),
		Kind:            equipment.KindPaddle,
		Status:          equipment.StatusAvailable,
	})
	if !errors.Is(err, equipment.ErrInventoryNumberExists) {
		t.Fatalf("duplicate Create() error = %v, want ErrInventoryNumberExists", err)
	}

	items, err := repository.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	for _, item := range items {
		if item.ID == created.ID {
			if item != created {
				t.Errorf("List() item = %+v, want %+v", item, created)
			}
			return
		}
	}

	t.Errorf("List() does not contain created item ID %d", created.ID)
}
