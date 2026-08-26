package postgres

import (
	"testing"
	"time"

	"github.com/Dyuzhovsergey/sup-rental/internal/dashboard"
	"github.com/Dyuzhovsergey/sup-rental/internal/rental"
)

func TestAdminDashboardRepositoryReturnsActualCounts(t *testing.T) {
	pool, ctx := rentalTestPool(t)
	fixture := newRentalRepositoryFixture(t, ctx, pool, 4)
	dashboardRepository := NewAdminDashboardRepository(pool)
	rentalRepository := NewRentalRepository(pool)
	location := time.FixedZone("test-msk", 3*60*60)
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, location)
	query := dashboard.Query{
		Now:      now,
		DayStart: time.Date(2026, 9, 8, 0, 0, 0, 0, location),
		DayEnd:   time.Date(2026, 9, 9, 0, 0, 0, 0, location),
	}
	baseline, err := dashboardRepository.Snapshot(ctx, query)
	if err != nil {
		t.Fatalf("baseline Snapshot() error = %v", err)
	}

	if _, err := pool.Exec(ctx, "UPDATE equipment SET status = 'maintenance' WHERE id = $1", fixture.equipmentIDs[0]); err != nil {
		t.Fatalf("mark equipment maintenance: %v", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE equipment SET status = 'retired' WHERE id = $1", fixture.equipmentIDs[1]); err != nil {
		t.Fatalf("mark equipment retired: %v", err)
	}

	confirmed, err := rentalRepository.CreateConfirmed(
		ctx, fixture.actor, fixture.firstClientID,
		rentalTestInterval(t, now.Add(time.Hour)),
		[]rental.ModelSelection{{ModelID: fixture.modelID, Quantity: 1}},
	)
	if err != nil {
		t.Fatalf("create confirmed rental: %v", err)
	}
	active, err := rentalRepository.CreateConfirmed(
		ctx, fixture.actor, fixture.secondClientID,
		rentalTestInterval(t, now.Add(-2*time.Hour)),
		[]rental.ModelSelection{{ModelID: fixture.modelID, Quantity: 1}},
	)
	if err != nil {
		t.Fatalf("create active rental: %v", err)
	}
	if _, err := rentalRepository.Issue(ctx, fixture.actor, active.ID, now.Add(-2*time.Hour)); err != nil {
		t.Fatalf("issue active rental: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE rental_payments
		SET occurred_at = CASE rental_id WHEN $1 THEN $3::timestamptz ELSE $4::timestamptz END
		WHERE rental_id = ANY($2) AND kind = 'base'`,
		confirmed.ID, []int64{confirmed.ID, active.ID}, query.DayStart.Add(time.Hour), query.DayStart.Add(2*time.Hour)); err != nil {
		t.Fatalf("move base payments into dashboard day: %v", err)
	}
	var basePaymentID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM rental_payments
		WHERE rental_id = $1 AND kind = 'base'`, confirmed.ID).Scan(&basePaymentID); err != nil {
		t.Fatalf("load base payment id: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO rental_payments
		(rental_id, kind, amount_kopecks, occurred_at, actor_user_id)
		VALUES ($1, 'overdue', 25000, $2, $3)`, active.ID, query.DayStart.Add(3*time.Hour), fixture.actor.ID); err != nil {
		t.Fatalf("insert overdue payment: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO rental_payments
		(rental_id, kind, amount_kopecks, occurred_at, actor_user_id, related_payment_id)
		VALUES ($1, 'refund', 10000, $2, $3, $4)`, confirmed.ID, query.DayStart.Add(4*time.Hour), fixture.actor.ID, basePaymentID); err != nil {
		t.Fatalf("insert refund payment: %v", err)
	}

	got, err := dashboardRepository.Snapshot(ctx, query)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if got.EquipmentTotal != baseline.EquipmentTotal ||
		got.EquipmentAvailable != baseline.EquipmentAvailable-3 ||
		got.EquipmentMaintenance != baseline.EquipmentMaintenance+1 ||
		got.EquipmentRetired != baseline.EquipmentRetired+1 ||
		got.EquipmentIssued != baseline.EquipmentIssued+1 ||
		got.RentalsActive != baseline.RentalsActive+1 ||
		got.RentalsOverdue != baseline.RentalsOverdue+1 ||
		got.RentalsStartingToday != baseline.RentalsStartingToday+2 ||
		got.RentalsEndingToday != baseline.RentalsEndingToday+2 ||
		got.PaymentsBaseTodayKopecks != baseline.PaymentsBaseTodayKopecks+100_000 ||
		got.PaymentsOverdueTodayKopecks != baseline.PaymentsOverdueTodayKopecks+25_000 ||
		got.PaymentsRefundTodayKopecks != baseline.PaymentsRefundTodayKopecks+10_000 ||
		got.PaymentsNetTodayKopecks != baseline.PaymentsNetTodayKopecks+115_000 {
		t.Fatalf("Snapshot() = %+v, baseline = %+v", got, baseline)
	}
}

func TestAdminDashboardRepositoryRejectsInvalidQuery(t *testing.T) {
	pool, ctx := rentalTestPool(t)
	if _, err := NewAdminDashboardRepository(pool).Snapshot(ctx, dashboard.Query{}); err == nil {
		t.Fatal("Snapshot() error = nil")
	}
}
