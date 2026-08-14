package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/Dyuzhovsergey/sup-rental/internal/client"
	"github.com/Dyuzhovsergey/sup-rental/internal/equipment"
	"github.com/Dyuzhovsergey/sup-rental/internal/rental"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRentalRepositoryCreateGetAndUpdateDraft(t *testing.T) {
	pool, ctx := rentalTestPool(t)
	fixture := newRentalRepositoryFixture(t, ctx, pool)
	repository := NewRentalRepository(pool)

	start := time.Date(2026, time.September, 1, 10, 0, 0, 0, time.UTC)
	draft := newRentalDraft(t, fixture.firstClientID, start, start.Add(90*time.Minute), fixture.items[:2])
	created, err := repository.Create(ctx, draft)
	if err != nil {
		t.Fatalf("Create() error = %v; apply migrations to TEST_DATABASE_URL first", err)
	}
	if created.ID <= 0 {
		t.Fatalf("Create() ID = %d, want positive", created.ID)
	}

	got, err := repository.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	assertRentalEqual(t, got, created)
	total, err := got.PlannedTotalKopecks()
	if err != nil || total != 150_000 {
		t.Fatalf("PlannedTotalKopecks() = %d, %v; want 150000", total, err)
	}

	if _, err := pool.Exec(
		ctx,
		"UPDATE equipment_models SET hourly_rate_kopecks = 60000 WHERE id = $1",
		fixture.modelID,
	); err != nil {
		t.Fatalf("change current equipment rate: %v", err)
	}
	storedSnapshot, err := repository.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() after rate change error = %v", err)
	}
	for _, item := range storedSnapshot.Items() {
		if item.HourlyRateKopecks != 50_000 {
			t.Errorf("stored snapshot rate = %d, want 50000", item.HourlyRateKopecks)
		}
	}

	updatedStart := start.Add(24 * time.Hour)
	updated := restoreRentalDraft(
		t,
		created.ID,
		fixture.secondClientID,
		updatedStart,
		updatedStart.Add(time.Hour),
		[]rental.Item{fixture.items[2], fixture.items[0]},
	)
	updated, err = repository.UpdateDraft(ctx, updated)
	if err != nil {
		t.Fatalf("UpdateDraft() error = %v", err)
	}
	got, err = repository.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() updated error = %v", err)
	}
	assertRentalEqual(t, got, updated)
	if items := got.Items(); items[0].EquipmentID != fixture.items[2].EquipmentID ||
		items[1].EquipmentID != fixture.items[0].EquipmentID {
		t.Fatalf("updated item order = %#v", items)
	}
}

func TestRentalRepositoryCreatesEmptyDraft(t *testing.T) {
	pool, ctx := rentalTestPool(t)
	fixture := newRentalRepositoryFixture(t, ctx, pool)
	repository := NewRentalRepository(pool)

	start := time.Date(2026, time.September, 2, 10, 0, 0, 0, time.UTC)
	draft := newRentalDraft(t, fixture.firstClientID, start, start.Add(30*time.Minute), nil)
	created, err := repository.Create(ctx, draft)
	if err != nil {
		t.Fatalf("Create() empty draft error = %v", err)
	}
	got, err := repository.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() empty draft error = %v", err)
	}
	if got.ItemCount() != 0 {
		t.Fatalf("ItemCount() = %d, want 0", got.ItemCount())
	}
}

func TestRentalRepositoryRollsBackDraftUpdate(t *testing.T) {
	pool, ctx := rentalTestPool(t)
	fixture := newRentalRepositoryFixture(t, ctx, pool)
	repository := NewRentalRepository(pool)

	start := time.Date(2026, time.September, 3, 10, 0, 0, 0, time.UTC)
	original, err := repository.Create(
		ctx,
		newRentalDraft(t, fixture.firstClientID, start, start.Add(time.Hour), fixture.items[:2]),
	)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	missing := fixture.items[2]
	missing.EquipmentID = 9_000_000_000
	missing.InventoryNumber = "SUP-" + fixture.modelCode + "-999"
	updatedStart := start.Add(48 * time.Hour)
	update := restoreRentalDraft(
		t,
		original.ID,
		fixture.secondClientID,
		updatedStart,
		updatedStart.Add(90*time.Minute),
		[]rental.Item{fixture.items[2], missing},
	)

	_, err = repository.UpdateDraft(ctx, update)
	if !errors.Is(err, equipment.ErrEquipmentNotFound) {
		t.Fatalf("UpdateDraft() error = %v, want ErrEquipmentNotFound", err)
	}
	stored, getErr := repository.Get(ctx, original.ID)
	if getErr != nil {
		t.Fatalf("Get() after rollback error = %v", getErr)
	}
	assertRentalEqual(t, stored, original)
}

func TestRentalRepositoryMapsMissingReferences(t *testing.T) {
	pool, ctx := rentalTestPool(t)
	fixture := newRentalRepositoryFixture(t, ctx, pool)
	repository := NewRentalRepository(pool)
	start := time.Date(2026, time.September, 4, 10, 0, 0, 0, time.UTC)

	missingClientDraft := newRentalDraft(t, 9_000_000_000, start, start.Add(time.Hour), nil)
	if _, err := repository.Create(ctx, missingClientDraft); !errors.Is(err, client.ErrClientNotFound) {
		t.Errorf("Create() missing client error = %v, want ErrClientNotFound", err)
	}

	missingItem := fixture.items[0]
	missingItem.EquipmentID = 9_000_000_000
	missingItem.InventoryNumber = "SUP-" + fixture.modelCode + "-999"
	missingEquipmentDraft := newRentalDraft(
		t,
		fixture.firstClientID,
		start,
		start.Add(time.Hour),
		[]rental.Item{missingItem},
	)
	if _, err := repository.Create(ctx, missingEquipmentDraft); !errors.Is(err, equipment.ErrEquipmentNotFound) {
		t.Errorf("Create() missing equipment error = %v, want ErrEquipmentNotFound", err)
	}

	if _, err := repository.Get(ctx, 9_000_000_000); !errors.Is(err, rental.ErrRentalNotFound) {
		t.Errorf("Get() missing rental error = %v, want ErrRentalNotFound", err)
	}
}

func TestRentalRepositoryRejectsNonDraftUpdate(t *testing.T) {
	pool, ctx := rentalTestPool(t)
	fixture := newRentalRepositoryFixture(t, ctx, pool)
	repository := NewRentalRepository(pool)
	start := time.Date(2026, time.September, 5, 10, 0, 0, 0, time.UTC)

	created, err := repository.Create(
		ctx,
		newRentalDraft(t, fixture.firstClientID, start, start.Add(time.Hour), fixture.items[:1]),
	)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE rentals SET status = 'confirmed' WHERE id = $1", created.ID); err != nil {
		t.Fatalf("set confirmed fixture: %v", err)
	}

	proposal := restoreRentalDraft(
		t,
		created.ID,
		fixture.secondClientID,
		start,
		start.Add(90*time.Minute),
		fixture.items[:2],
	)
	if _, err := repository.UpdateDraft(ctx, proposal); !errors.Is(err, rental.ErrRentalNotEditable) {
		t.Fatalf("UpdateDraft() error = %v, want ErrRentalNotEditable", err)
	}
	confirmed, err := repository.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() confirmed error = %v", err)
	}
	if confirmed.Status != rental.StatusConfirmed || confirmed.ClientID != fixture.firstClientID {
		t.Fatalf("confirmed rental changed = %#v", confirmed)
	}
}

func TestRentalsTablesEnforceConstraints(t *testing.T) {
	pool, ctx := rentalTestPool(t)
	fixture := newRentalRepositoryFixture(t, ctx, pool)
	start := time.Date(2026, time.September, 6, 10, 0, 0, 0, time.UTC)

	rentalCases := []struct {
		name   string
		start  time.Time
		end    time.Time
		status string
	}{
		{name: "invalid status", start: start, end: start.Add(time.Hour), status: "unknown"},
		{name: "short interval", start: start, end: start.Add(15 * time.Minute), status: "draft"},
		{name: "unaligned start", start: start.Add(5 * time.Minute), end: start.Add(time.Hour), status: "draft"},
		{name: "unaligned end", start: start, end: start.Add(65 * time.Minute), status: "draft"},
	}
	for _, tt := range rentalCases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := pool.Exec(ctx, `INSERT INTO rentals
				(client_id, planned_start_at, planned_end_at, status)
				VALUES ($1, $2, $3, $4)`, fixture.firstClientID, tt.start, tt.end, tt.status)
			assertPostgresCode(t, err, "23514")
		})
	}

	var rentalID int64
	if err := pool.QueryRow(ctx, `INSERT INTO rentals
		(client_id, planned_start_at, planned_end_at, status)
		VALUES ($1, $2, $3, 'draft') RETURNING id`,
		fixture.firstClientID, start, start.Add(time.Hour)).Scan(&rentalID); err != nil {
		t.Fatalf("insert rental constraint fixture: %v", err)
	}

	itemCases := []struct {
		name            string
		position        int
		inventoryNumber string
		kind            string
		modelCode       string
		rate            int64
	}{
		{name: "invalid position", position: 0, inventoryNumber: fixture.items[0].InventoryNumber, kind: "sup_board", modelCode: fixture.modelCode, rate: 50_000},
		{name: "invalid kind", position: 1, inventoryNumber: fixture.items[0].InventoryNumber, kind: "unknown", modelCode: fixture.modelCode, rate: 50_000},
		{name: "invalid model", position: 1, inventoryNumber: fixture.items[0].InventoryNumber, kind: "sup_board", modelCode: "invalid", rate: 50_000},
		{name: "mismatched inventory number", position: 1, inventoryNumber: "PADDLE-" + fixture.modelCode + "-1", kind: "sup_board", modelCode: fixture.modelCode, rate: 50_000},
		{name: "odd rate", position: 1, inventoryNumber: fixture.items[0].InventoryNumber, kind: "sup_board", modelCode: fixture.modelCode, rate: 50_001},
	}
	for _, tt := range itemCases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := pool.Exec(ctx, `INSERT INTO rental_items
				(rental_id, equipment_id, position, inventory_number, kind, model_code, hourly_rate_kopecks)
				VALUES ($1, $2, $3, $4, $5, $6, $7)`,
				rentalID, fixture.items[0].EquipmentID, tt.position,
				tt.inventoryNumber, tt.kind, tt.modelCode, tt.rate)
			assertPostgresCode(t, err, "23514")
		})
	}
}

func TestRentalItemPreventsEquipmentDeletion(t *testing.T) {
	pool, ctx := rentalTestPool(t)
	fixture := newRentalRepositoryFixture(t, ctx, pool)
	repository := NewRentalRepository(pool)
	start := time.Date(2026, time.September, 7, 10, 0, 0, 0, time.UTC)

	if _, err := repository.Create(
		ctx,
		newRentalDraft(t, fixture.firstClientID, start, start.Add(time.Hour), fixture.items[:1]),
	); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	_, err := pool.Exec(ctx, "DELETE FROM equipment WHERE id = $1", fixture.items[0].EquipmentID)
	assertPostgresCode(t, err, "23503")
}

type rentalRepositoryFixture struct {
	firstClientID  int64
	secondClientID int64
	modelID        int64
	modelCode      string
	items          []rental.Item
}

func newRentalRepositoryFixture(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) rentalRepositoryFixture {
	t.Helper()

	unique := time.Now().UnixNano()
	fixture := rentalRepositoryFixture{modelCode: fmt.Sprintf("RENTAL-%d", unique)}
	for index, target := range []*int64{&fixture.firstClientID, &fixture.secondClientID} {
		phone := fmt.Sprintf("+70%09d", (unique+int64(index))%1_000_000_000)
		if err := pool.QueryRow(ctx, `INSERT INTO clients (full_name, phone)
			VALUES ($1, $2) RETURNING id`, fmt.Sprintf("Rental Test Client %d", index+1), phone).Scan(target); err != nil {
			t.Fatalf("insert rental test client: %v", err)
		}
	}
	if err := pool.QueryRow(ctx, `INSERT INTO equipment_models
		(kind, model_code, hourly_rate_kopecks)
		VALUES ('sup_board', $1, 50000) RETURNING id`, fixture.modelCode).Scan(&fixture.modelID); err != nil {
		t.Fatalf("insert rental test equipment model: %v", err)
	}

	for sequence := int64(1); sequence <= 3; sequence++ {
		var equipmentID int64
		if err := pool.QueryRow(ctx, `INSERT INTO equipment
			(model_id, sequence_number, status)
			VALUES ($1, $2, 'available') RETURNING id`, fixture.modelID, sequence).Scan(&equipmentID); err != nil {
			t.Fatalf("insert rental test equipment: %v", err)
		}
		fixture.items = append(fixture.items, rental.Item{
			EquipmentID:       equipmentID,
			InventoryNumber:   fmt.Sprintf("SUP-%s-%d", fixture.modelCode, sequence),
			Kind:              equipment.KindSUPBoard,
			ModelCode:         fixture.modelCode,
			HourlyRateKopecks: 50_000,
		})
	}

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, "DELETE FROM rentals WHERE client_id = ANY($1)", []int64{fixture.firstClientID, fixture.secondClientID})
		_, _ = pool.Exec(cleanupCtx, "DELETE FROM equipment WHERE model_id = $1", fixture.modelID)
		_, _ = pool.Exec(cleanupCtx, "DELETE FROM equipment_models WHERE id = $1", fixture.modelID)
		_, _ = pool.Exec(cleanupCtx, "DELETE FROM clients WHERE id = ANY($1)", []int64{fixture.firstClientID, fixture.secondClientID})
	})
	return fixture
}

func newRentalDraft(
	t *testing.T,
	clientID int64,
	start time.Time,
	end time.Time,
	items []rental.Item,
) rental.Rental {
	t.Helper()

	interval, err := rental.NewInterval(start, end)
	if err != nil {
		t.Fatalf("NewInterval() error = %v", err)
	}
	draft, err := rental.New(clientID, interval)
	if err != nil {
		t.Fatalf("rental.New() error = %v", err)
	}
	for _, item := range items {
		if err := draft.AddItem(item); err != nil {
			t.Fatalf("AddItem() error = %v", err)
		}
	}
	return draft
}

func restoreRentalDraft(
	t *testing.T,
	id int64,
	clientID int64,
	start time.Time,
	end time.Time,
	items []rental.Item,
) rental.Rental {
	t.Helper()

	interval, err := rental.NewInterval(start, end)
	if err != nil {
		t.Fatalf("NewInterval() error = %v", err)
	}
	draft, err := rental.Restore(id, clientID, interval, rental.StatusDraft, items)
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	return draft
}

func assertRentalEqual(t *testing.T, got, want rental.Rental) {
	t.Helper()

	if got.ID != want.ID || got.ClientID != want.ClientID || got.Status != want.Status ||
		!got.Interval.Start().Equal(want.Interval.Start()) ||
		!got.Interval.End().Equal(want.Interval.End()) ||
		!reflect.DeepEqual(got.Items(), want.Items()) {
		t.Errorf("rental = %#v items %#v, want %#v items %#v", got, got.Items(), want, want.Items())
	}
}

func assertPostgresCode(t *testing.T, err error, wantCode string) {
	t.Helper()

	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != wantCode {
		t.Fatalf("PostgreSQL error = %v, want code %s", err, wantCode)
	}
}

func rentalTestPool(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()

	connectionString := os.Getenv("TEST_DATABASE_URL")
	if connectionString == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	pool, err := Open(ctx, connectionString)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, ctx
}
