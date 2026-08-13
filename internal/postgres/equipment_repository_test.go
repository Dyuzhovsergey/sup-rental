package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Dyuzhovsergey/sup-rental/internal/equipment"
	"github.com/Dyuzhovsergey/sup-rental/internal/user"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestEquipmentRepositoryCreatesSequentialBatches(t *testing.T) {
	pool, ctx := equipmentTestPool(t)
	repository := NewEquipmentRepository(pool)
	actor := equipmentTestAdmin(t, ctx, pool)
	code := uniqueEquipmentModelCode()
	cleanupEquipmentModel(t, pool, actor.ID, code)

	first, err := repository.CreateBatch(ctx, actor, equipment.BatchCreateInput{
		Kind: equipment.KindPaddle, ModelCode: code, HourlyRateRubles: 350, Quantity: 3,
	})
	if err != nil {
		t.Fatalf("CreateBatch() first error = %v; apply migrations first", err)
	}
	second, err := repository.CreateBatch(ctx, actor, equipment.BatchCreateInput{
		Kind: equipment.KindPaddle, ModelCode: code, HourlyRateRubles: 350, Quantity: 2,
	})
	if err != nil {
		t.Fatalf("CreateBatch() second error = %v", err)
	}

	wantNumbers := []string{
		"PADDLE-" + code + "-1", "PADDLE-" + code + "-2", "PADDLE-" + code + "-3",
		"PADDLE-" + code + "-4", "PADDLE-" + code + "-5",
	}
	items := append(first.Items, second.Items...)
	for i, item := range items {
		if item.InventoryNumber != wantNumbers[i] || item.ModelCode != code ||
			item.HourlyRateKopecks != 35000 || item.Status != equipment.StatusAvailable {
			t.Errorf("item %d = %#v, want number %q", i, item, wantNumbers[i])
		}
		got, err := repository.Get(ctx, item.ID)
		if err != nil || got != item {
			t.Errorf("Get(%d) = %#v, %v; want %#v", item.ID, got, err, item)
		}
	}
	updated, err := repository.UpdateStatus(ctx, actor, items[0].ID, equipment.StatusMaintenance)
	if err != nil {
		t.Fatalf("UpdateStatus() error = %v", err)
	}
	if updated.Status != equipment.StatusMaintenance || updated.ModelCode != code || updated.HourlyRateKopecks != 35000 {
		t.Errorf("UpdateStatus() = %#v", updated)
	}
	page, err := repository.ListPage(ctx, equipment.ListPageInput{
		Scope: equipment.ListScopeActive, Page: 1, PageSize: 15,
	})
	if err != nil {
		t.Fatalf("ListPage() error = %v", err)
	}
	found := false
	for _, item := range page.Items {
		if item.ID == updated.ID && item.InventoryNumber == updated.InventoryNumber {
			found = true
		}
	}
	if !found {
		t.Errorf("ListPage() does not contain updated item %d", updated.ID)
	}
	if len(page.Items) > 1 && page.Items[0].ID < page.Items[1].ID {
		t.Errorf("ListPage() IDs start with %d, %d; want newest first", page.Items[0].ID, page.Items[1].ID)
	}

	var events int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_events
		WHERE action = 'equipment.batch_created' AND target_id = ANY($1)
	`, []int64{first.Items[0].ID, second.Items[0].ID}).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 2 {
		t.Errorf("batch audit events = %d, want 2", events)
	}
}

func TestEquipmentRepositoryRejectsExistingModelRateConflict(t *testing.T) {
	pool, ctx := equipmentTestPool(t)
	repository := NewEquipmentRepository(pool)
	actor := equipmentTestAdmin(t, ctx, pool)
	code := uniqueEquipmentModelCode()
	cleanupEquipmentModel(t, pool, actor.ID, code)

	input := equipment.BatchCreateInput{Kind: equipment.KindSUPBoard, ModelCode: code, HourlyRateRubles: 500, Quantity: 1}
	if _, err := repository.CreateBatch(ctx, actor, input); err != nil {
		t.Fatal(err)
	}
	input.HourlyRateRubles = 600
	if _, err := repository.CreateBatch(ctx, actor, input); !errors.Is(err, equipment.ErrModelRateConflict) {
		t.Fatalf("conflicting CreateBatch() error = %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM equipment e JOIN equipment_models m ON m.id=e.model_id WHERE m.kind=$1 AND m.model_code=$2`, input.Kind, code).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("equipment count after conflict = %d, want 1", count)
	}
}

func TestEquipmentRepositoryAllocatesConcurrentRanges(t *testing.T) {
	pool, ctx := equipmentTestPool(t)
	repository := NewEquipmentRepository(pool)
	actor := equipmentTestAdmin(t, ctx, pool)
	code := uniqueEquipmentModelCode()
	cleanupEquipmentModel(t, pool, actor.ID, code)

	input := equipment.BatchCreateInput{Kind: equipment.KindLifeJacket, ModelCode: code, HourlyRateRubles: 200, Quantity: 10}
	results := make(chan equipment.Batch, 2)
	errorsChannel := make(chan error, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			batch, err := repository.CreateBatch(ctx, actor, input)
			if err != nil {
				errorsChannel <- err
				return
			}
			results <- batch
		}()
	}
	wait.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		t.Errorf("concurrent CreateBatch() error = %v", err)
	}
	close(results)
	sequences := map[int64]bool{}
	for batch := range results {
		for _, item := range batch.Items {
			sequences[item.SequenceNumber] = true
		}
	}
	if len(sequences) != 20 {
		t.Fatalf("unique sequence count = %d, want 20", len(sequences))
	}
	for sequence := int64(1); sequence <= 20; sequence++ {
		if !sequences[sequence] {
			t.Errorf("sequence %d is missing", sequence)
		}
	}
}

func TestEquipmentRepositoryRollsBackBatchWhenAuditFails(t *testing.T) {
	pool, ctx := equipmentTestPool(t)
	actor := equipmentTestAdmin(t, ctx, pool)
	code := uniqueEquipmentModelCode()
	cleanupEquipmentModel(t, pool, actor.ID, code)
	repository := NewEquipmentRepository(pool)
	repository.writeAudit = func(context.Context, pgx.Tx, string, user.User, equipment.Item, equipmentAuditDetails) error {
		return errors.New("audit unavailable")
	}

	_, err := repository.CreateBatch(ctx, actor, equipment.BatchCreateInput{
		Kind: equipment.KindPaddle, ModelCode: code, HourlyRateRubles: 350, Quantity: 3,
	})
	if err == nil || !strings.Contains(err.Error(), "audit unavailable") {
		t.Fatalf("CreateBatch() error = %v", err)
	}
	var models, items int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM equipment_models WHERE kind='paddle' AND model_code=$1`, code).Scan(&models); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM equipment e JOIN equipment_models m ON m.id=e.model_id WHERE m.model_code=$1`, code).Scan(&items); err != nil {
		t.Fatal(err)
	}
	if models != 0 || items != 0 {
		t.Errorf("after rollback models=%d items=%d, want 0,0", models, items)
	}
}

func TestEquipmentRepositoryChangesModelAndRateWithAudit(t *testing.T) {
	pool, ctx := equipmentTestPool(t)
	repository := NewEquipmentRepository(pool)
	actor := equipmentTestAdmin(t, ctx, pool)
	sourceCode := uniqueEquipmentModelCode()
	targetCode := uniqueEquipmentModelCode()
	cleanupEquipmentModel(t, pool, actor.ID, sourceCode)
	cleanupEquipmentModel(t, pool, actor.ID, targetCode)

	source, err := repository.CreateBatch(ctx, actor, equipment.BatchCreateInput{
		Kind: equipment.KindPaddle, ModelCode: sourceCode, HourlyRateRubles: 350, Quantity: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	target, err := repository.CreateBatch(ctx, actor, equipment.BatchCreateInput{
		Kind: equipment.KindLifeJacket, ModelCode: targetCode, HourlyRateRubles: 250, Quantity: 2,
	})
	if err != nil {
		t.Fatal(err)
	}

	moved, err := repository.ChangeModel(ctx, actor, source.Items[0].ID, equipment.ModelChangeInput{
		Kind: equipment.KindLifeJacket, ModelCode: targetCode, HourlyRateRubles: 250,
	})
	if err != nil {
		t.Fatalf("ChangeModel() error = %v", err)
	}
	wantNumber := "VEST-" + targetCode + "-3"
	if moved.InventoryNumber != wantNumber || moved.Status != equipment.StatusAvailable || moved.ModelID != target.Items[0].ModelID {
		t.Errorf("ChangeModel() = %#v, want number %q", moved, wantNumber)
	}

	changed, err := repository.ChangeModelRate(ctx, actor, moved.ID, 30000)
	if err != nil {
		t.Fatalf("ChangeModelRate() error = %v", err)
	}
	if changed.Item.HourlyRateKopecks != 30000 || changed.AffectedItems != 3 {
		t.Errorf("ChangeModelRate() = %#v, want rate 30000 and 3 items", changed)
	}
	var rates []int64
	rows, err := pool.Query(ctx, `SELECT m.hourly_rate_kopecks FROM equipment e JOIN equipment_models m ON m.id=e.model_id WHERE m.id=$1 ORDER BY e.id`, moved.ModelID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var rate int64
		if err := rows.Scan(&rate); err != nil {
			t.Fatal(err)
		}
		rates = append(rates, rate)
	}
	rows.Close()
	if !reflect.DeepEqual(rates, []int64{30000, 30000, 30000}) {
		t.Errorf("model rates = %v", rates)
	}

	for action, want := range map[string]int{"equipment.model_changed": 1, "equipment.model_rate_changed": 1} {
		var count int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE actor_user_id=$1 AND action=$2 AND target_label LIKE '%' || $3 || '%'`, actor.ID, action, targetCode).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != want {
			t.Errorf("%s events = %d, want %d", action, count, want)
		}
	}
}

func TestEquipmentRepositoryAllocatesConcurrentModelMoves(t *testing.T) {
	pool, ctx := equipmentTestPool(t)
	repository := NewEquipmentRepository(pool)
	actor := equipmentTestAdmin(t, ctx, pool)
	sourceCode := uniqueEquipmentModelCode()
	targetCode := uniqueEquipmentModelCode()
	cleanupEquipmentModel(t, pool, actor.ID, sourceCode)
	cleanupEquipmentModel(t, pool, actor.ID, targetCode)

	source, err := repository.CreateBatch(ctx, actor, equipment.BatchCreateInput{
		Kind: equipment.KindPaddle, ModelCode: sourceCode, HourlyRateRubles: 350, Quantity: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateBatch(ctx, actor, equipment.BatchCreateInput{
		Kind: equipment.KindSUPBoard, ModelCode: targetCode, HourlyRateRubles: 500, Quantity: 1,
	}); err != nil {
		t.Fatal(err)
	}

	results := make(chan equipment.Item, 2)
	errorsChannel := make(chan error, 2)
	var wait sync.WaitGroup
	for _, item := range source.Items {
		wait.Add(1)
		go func(id int64) {
			defer wait.Done()
			moved, err := repository.ChangeModel(ctx, actor, id, equipment.ModelChangeInput{
				Kind: equipment.KindSUPBoard, ModelCode: targetCode, HourlyRateRubles: 500,
			})
			if err != nil {
				errorsChannel <- err
				return
			}
			results <- moved
		}(item.ID)
	}
	wait.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		t.Errorf("concurrent ChangeModel() error = %v", err)
	}
	close(results)
	sequences := map[int64]bool{}
	for item := range results {
		sequences[item.SequenceNumber] = true
	}
	if len(sequences) != 2 || !sequences[2] || !sequences[3] {
		t.Errorf("allocated sequences = %v, want 2 and 3", sequences)
	}
}

func TestEquipmentRepositoryRollsBackModelMutationsWhenAuditFails(t *testing.T) {
	pool, ctx := equipmentTestPool(t)
	actor := equipmentTestAdmin(t, ctx, pool)
	sourceCode := uniqueEquipmentModelCode()
	targetCode := uniqueEquipmentModelCode()
	cleanupEquipmentModel(t, pool, actor.ID, sourceCode)
	cleanupEquipmentModel(t, pool, actor.ID, targetCode)
	repository := NewEquipmentRepository(pool)
	source, err := repository.CreateBatch(ctx, actor, equipment.BatchCreateInput{
		Kind: equipment.KindPaddle, ModelCode: sourceCode, HourlyRateRubles: 350, Quantity: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	repository.writeAudit = func(context.Context, pgx.Tx, string, user.User, equipment.Item, equipmentAuditDetails) error {
		return errors.New("audit unavailable")
	}

	if _, err := repository.ChangeModel(ctx, actor, source.Items[0].ID, equipment.ModelChangeInput{
		Kind: equipment.KindLifeJacket, ModelCode: targetCode, HourlyRateRubles: 250,
	}); err == nil {
		t.Fatal("ChangeModel() returned nil error")
	}
	got, err := repository.Get(ctx, source.Items[0].ID)
	if err != nil || got.ModelID != source.Items[0].ModelID || got.InventoryNumber != source.Items[0].InventoryNumber {
		t.Errorf("equipment after model rollback = %#v, %v", got, err)
	}
	var targetModels int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM equipment_models WHERE model_code=$1`, targetCode).Scan(&targetModels); err != nil {
		t.Fatal(err)
	}
	if targetModels != 0 {
		t.Errorf("target models after rollback = %d, want 0", targetModels)
	}

	if _, err := repository.ChangeModelRate(ctx, actor, source.Items[0].ID, 40000); err == nil {
		t.Fatal("ChangeModelRate() returned nil error")
	}
	got, err = repository.Get(ctx, source.Items[0].ID)
	if err != nil || got.HourlyRateKopecks != 35000 {
		t.Errorf("equipment after rate rollback = %#v, %v", got, err)
	}
}

func TestEquipmentBatchAuditDetails(t *testing.T) {
	details := equipmentAuditDetails{Batch: &equipmentBatchAuditDetails{Kind: equipment.KindPaddle, ModelCode: "CARBON", HourlyRateKopecks: 35000, Quantity: 3, FirstInventoryNumber: "PADDLE-CARBON-1", LastInventoryNumber: "PADDLE-CARBON-3"}}
	encoded, err := json.Marshal(details)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"model_code":"CARBON"`, `"quantity":3`, `"hourly_rate_kopecks":35000`} {
		if !strings.Contains(string(encoded), want) {
			t.Errorf("details %s do not contain %s", encoded, want)
		}
	}
}

func equipmentTestPool(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	connectionString := os.Getenv("TEST_DATABASE_URL")
	if connectionString == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	pool, err := Open(ctx, connectionString)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, ctx
}

func uniqueEquipmentModelCode() string {
	return fmt.Sprintf("TEST-%d", time.Now().UnixNano())
}

func cleanupEquipmentModel(t *testing.T, pool *pgxpool.Pool, actorID int64, code string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(ctx, `DELETE FROM audit_events WHERE actor_user_id=$1 AND (target_label LIKE '%' || $2 || '%' OR details->'batch'->>'model_code'=$2 OR details->'model_rate'->>'model_code'=$2)`, actorID, code)
		_, _ = pool.Exec(ctx, `DELETE FROM equipment WHERE model_id IN (SELECT id FROM equipment_models WHERE model_code=$1)`, code)
		_, _ = pool.Exec(ctx, `DELETE FROM equipment_models WHERE model_code=$1`, code)
	})
}

func equipmentTestAdmin(t *testing.T, ctx context.Context, pool *pgxpool.Pool) user.User {
	t.Helper()
	actor, created := operatorTestAdmin(t, ctx, pool)
	if created {
		t.Cleanup(func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _ = pool.Exec(cleanupCtx, "DELETE FROM users WHERE id = $1", actor.ID)
		})
	}
	return actor
}
