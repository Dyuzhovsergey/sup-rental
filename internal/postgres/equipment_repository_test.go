package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Dyuzhovsergey/sup-rental/internal/equipment"
	"github.com/Dyuzhovsergey/sup-rental/internal/user"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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
	actor := equipmentTestAdmin(t, ctx, pool)
	inventoryNumber := fmt.Sprintf("TEST-%d", time.Now().UnixNano())

	created, err := repository.Create(ctx, actor, equipment.Item{
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
		cleanupEquipmentAudit(t, cleanupCtx, pool, created.ID)
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

	updatedInventoryNumber := inventoryNumber + "-UPDATED"
	updated, err := repository.Update(
		ctx,
		actor,
		created.ID,
		updatedInventoryNumber,
		equipment.KindLifeJacket,
		equipment.StatusMaintenance,
	)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.InventoryNumber != updatedInventoryNumber ||
		updated.Kind != equipment.KindLifeJacket ||
		updated.Status != equipment.StatusMaintenance {
		t.Errorf(
			"Update() = %+v, want number %q, kind %q and status %q",
			updated,
			updatedInventoryNumber,
			equipment.KindLifeJacket,
			equipment.StatusMaintenance,
		)
	}
	created = updated

	updated, err = repository.UpdateStatus(ctx, actor, created.ID, equipment.StatusAvailable)
	if err != nil {
		t.Fatalf("UpdateStatus() error = %v", err)
	}
	if updated.Status != equipment.StatusAvailable {
		t.Errorf(
			"UpdateStatus() Status = %q, want %q",
			updated.Status,
			equipment.StatusAvailable,
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

	_, err = repository.Create(ctx, actor, equipment.Item{
		InventoryNumber: strings.ToLower(updatedInventoryNumber),
		Kind:            equipment.KindPaddle,
		Status:          equipment.StatusAvailable,
	})
	if !errors.Is(err, equipment.ErrInventoryNumberExists) {
		t.Fatalf("duplicate Create() error = %v, want ErrInventoryNumberExists", err)
	}

	conflicting, err := repository.Create(ctx, actor, equipment.Item{
		InventoryNumber: inventoryNumber + "-CONFLICT",
		Kind:            equipment.KindPaddle,
		Status:          equipment.StatusAvailable,
	})
	if err != nil {
		t.Fatalf("create conflicting equipment: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()

		if _, err := pool.Exec(cleanupCtx, "DELETE FROM equipment WHERE id = $1", conflicting.ID); err != nil {
			t.Errorf("clean up conflicting equipment: %v", err)
		}
		cleanupEquipmentAudit(t, cleanupCtx, pool, conflicting.ID)
	})

	_, err = repository.Update(
		ctx,
		actor,
		created.ID,
		strings.ToLower(conflicting.InventoryNumber),
		equipment.KindSUPBoard,
		equipment.StatusMaintenance,
	)
	if !errors.Is(err, equipment.ErrInventoryNumberExists) {
		t.Fatalf("duplicate Update() error = %v, want ErrInventoryNumberExists", err)
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

func TestEquipmentRepositoryDelete(t *testing.T) {
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
	actor := equipmentTestAdmin(t, ctx, pool)
	created, err := repository.Create(ctx, actor, equipment.Item{
		InventoryNumber: fmt.Sprintf("TEST-DELETE-%d", time.Now().UnixNano()),
		Kind:            equipment.KindPaddle,
		Status:          equipment.StatusAvailable,
	})
	if err != nil {
		t.Fatalf("Create() error = %v; apply migrations to TEST_DATABASE_URL first", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()

		if _, err := pool.Exec(
			cleanupCtx,
			"DELETE FROM equipment WHERE id = $1",
			created.ID,
		); err != nil {
			t.Errorf("clean up equipment: %v", err)
		}
		cleanupEquipmentAudit(t, cleanupCtx, pool, created.ID)
	})

	updated, err := repository.Update(
		ctx,
		actor,
		created.ID,
		created.InventoryNumber+"-UPDATED",
		equipment.KindLifeJacket,
		equipment.StatusAvailable,
	)
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	updated, err = repository.UpdateStatus(ctx, actor, created.ID, equipment.StatusMaintenance)
	if err != nil {
		t.Fatalf("UpdateStatus() maintenance error = %v", err)
	}
	updated, err = repository.UpdateStatus(ctx, actor, created.ID, equipment.StatusRetired)
	if err != nil {
		t.Fatalf("UpdateStatus() retired error = %v", err)
	}

	deleted, err := repository.Delete(ctx, actor, created.ID)
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if deleted != updated {
		t.Errorf("Delete() = %+v, want %+v", deleted, updated)
	}

	if _, err := repository.Get(ctx, created.ID); !errors.Is(err, equipment.ErrEquipmentNotFound) {
		t.Errorf("Get() after Delete() error = %v, want ErrEquipmentNotFound", err)
	}
	if _, err := repository.Delete(ctx, actor, created.ID); !errors.Is(err, equipment.ErrEquipmentNotFound) {
		t.Errorf("second Delete() error = %v, want ErrEquipmentNotFound", err)
	}

	assertEquipmentAuditLifecycle(t, ctx, pool, actor, created, updated)
}

func assertEquipmentAuditLifecycle(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	actor user.User,
	created equipment.Item,
	deleted equipment.Item,
) {
	t.Helper()
	rows, err := pool.Query(
		ctx,
		`SELECT action, actor_user_id, actor_login, actor_role,
		        target_type, target_id, target_label, result, details
		 FROM audit_events
		 WHERE target_type = 'equipment' AND target_id = $1
		 ORDER BY id`,
		created.ID,
	)
	if err != nil {
		t.Fatalf("query equipment audit events: %v", err)
	}
	defer rows.Close()

	wantActions := []string{
		actionEquipmentCreated,
		actionEquipmentUpdated,
		actionEquipmentStatusChanged,
		actionEquipmentRetired,
		actionEquipmentDeleted,
	}
	index := 0
	for rows.Next() {
		var action, actorLogin, actorRole, targetType, targetLabel, result string
		var actorID, targetID int64
		var encodedDetails []byte
		if err := rows.Scan(
			&action,
			&actorID,
			&actorLogin,
			&actorRole,
			&targetType,
			&targetID,
			&targetLabel,
			&result,
			&encodedDetails,
		); err != nil {
			t.Fatalf("scan equipment audit event: %v", err)
		}
		if index >= len(wantActions) {
			t.Fatalf("unexpected extra audit action %q", action)
		}
		if action != wantActions[index] {
			t.Fatalf("audit action %d = %q, want %q", index, action, wantActions[index])
		}
		if actorID != actor.ID || actorLogin != actor.Login ||
			actorRole != string(user.RoleAdmin) || targetType != "equipment" ||
			targetID != created.ID || result != "success" {
			t.Errorf("audit event %q has unexpected actor, target or result", action)
		}
		if action == actionEquipmentDeleted && targetLabel != deleted.InventoryNumber {
			t.Errorf("deleted target label = %q, want %q", targetLabel, deleted.InventoryNumber)
		}

		var details equipmentAuditDetails
		if err := json.Unmarshal(encodedDetails, &details); err != nil {
			t.Fatalf("decode %q details: %v", action, err)
		}
		if action == actionEquipmentCreated && details.After == nil {
			t.Error("created event has no after snapshot")
		}
		if action == actionEquipmentDeleted {
			if details.Before == nil || details.After != nil ||
				details.Before.InventoryNumber != deleted.InventoryNumber ||
				details.Before.Kind != deleted.Kind || details.Before.Status != deleted.Status {
				t.Errorf("deleted details = %+v, want deleted snapshot", details)
			}
		}
		index++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate equipment audit events: %v", err)
	}
	if index != len(wantActions) {
		t.Errorf("audit event count = %d, want %d", index, len(wantActions))
	}
}

func TestEquipmentRepositoryRollsBackMutationWhenAuditFails(t *testing.T) {
	pool, ctx := openUserRepositoryTestDatabase(t)
	actor := equipmentTestAdmin(t, ctx, pool)
	auditError := errors.New("audit unavailable")
	failingWriter := func(
		context.Context,
		pgx.Tx,
		string,
		user.User,
		equipment.Item,
		equipmentAuditDetails,
	) error {
		return auditError
	}

	t.Run("create", func(t *testing.T) {
		repository := NewEquipmentRepository(pool)
		repository.writeAudit = failingWriter
		inventoryNumber := fmt.Sprintf("AUDIT-ROLLBACK-CREATE-%d", time.Now().UnixNano())
		_, err := repository.Create(ctx, actor, equipment.Item{
			InventoryNumber: inventoryNumber,
			Kind:            equipment.KindSUPBoard,
			Status:          equipment.StatusAvailable,
		})
		if !errors.Is(err, auditError) {
			t.Fatalf("Create() error = %v, want audit error", err)
		}
		var count int
		if err := pool.QueryRow(
			ctx,
			"SELECT count(*) FROM equipment WHERE inventory_number = $1",
			inventoryNumber,
		).Scan(&count); err != nil {
			t.Fatalf("count rolled back equipment: %v", err)
		}
		if count != 0 {
			t.Errorf("equipment count after rollback = %d, want 0", count)
		}
	})

	t.Run("update", func(t *testing.T) {
		item := createEquipmentAuditFixture(t, ctx, pool, actor, equipment.StatusAvailable)
		repository := NewEquipmentRepository(pool)
		repository.writeAudit = failingWriter
		_, err := repository.Update(
			ctx,
			actor,
			item.ID,
			item.InventoryNumber+"-CHANGED",
			equipment.KindLifeJacket,
			equipment.StatusMaintenance,
		)
		if !errors.Is(err, auditError) {
			t.Fatalf("Update() error = %v, want audit error", err)
		}
		stored, err := NewEquipmentRepository(pool).Get(ctx, item.ID)
		if err != nil || stored != item {
			t.Errorf("equipment after update rollback = %+v, error = %v, want %+v", stored, err, item)
		}
	})

	t.Run("retire", func(t *testing.T) {
		item := createEquipmentAuditFixture(t, ctx, pool, actor, equipment.StatusAvailable)
		repository := NewEquipmentRepository(pool)
		repository.writeAudit = failingWriter
		_, err := repository.UpdateStatus(ctx, actor, item.ID, equipment.StatusRetired)
		if !errors.Is(err, auditError) {
			t.Fatalf("UpdateStatus() error = %v, want audit error", err)
		}
		stored, err := NewEquipmentRepository(pool).Get(ctx, item.ID)
		if err != nil || stored.Status != equipment.StatusAvailable {
			t.Errorf("status after retirement rollback = %q, error = %v", stored.Status, err)
		}
	})

	t.Run("delete", func(t *testing.T) {
		item := createEquipmentAuditFixture(t, ctx, pool, actor, equipment.StatusRetired)
		repository := NewEquipmentRepository(pool)
		repository.writeAudit = failingWriter
		_, err := repository.Delete(ctx, actor, item.ID)
		if !errors.Is(err, auditError) {
			t.Fatalf("Delete() error = %v, want audit error", err)
		}
		stored, err := NewEquipmentRepository(pool).Get(ctx, item.ID)
		if err != nil || stored != item {
			t.Errorf("equipment after delete rollback = %+v, error = %v, want %+v", stored, err, item)
		}
	})
}

func createEquipmentAuditFixture(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	actor user.User,
	status equipment.Status,
) equipment.Item {
	t.Helper()
	item, err := NewEquipmentRepository(pool).Create(ctx, actor, equipment.Item{
		InventoryNumber: fmt.Sprintf("AUDIT-ROLLBACK-%d", time.Now().UnixNano()),
		Kind:            equipment.KindPaddle,
		Status:          status,
	})
	if err != nil {
		t.Fatalf("create equipment audit fixture: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, "DELETE FROM equipment WHERE id = $1", item.ID)
		cleanupEquipmentAudit(t, cleanupCtx, pool, item.ID)
	})
	return item
}

func equipmentTestAdmin(t *testing.T, ctx context.Context, pool *pgxpool.Pool) user.User {
	t.Helper()
	actor, created := operatorTestAdmin(t, ctx, pool)
	if created {
		t.Cleanup(func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := pool.Exec(cleanupCtx, "DELETE FROM users WHERE id = $1", actor.ID); err != nil {
				t.Errorf("clean up equipment test admin: %v", err)
			}
		})
	}
	return actor
}

func cleanupEquipmentAudit(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id int64) {
	t.Helper()
	if _, err := pool.Exec(
		ctx,
		"DELETE FROM audit_events WHERE target_type = 'equipment' AND target_id = $1",
		id,
	); err != nil {
		t.Errorf("clean up equipment audit events: %v", err)
	}
}
