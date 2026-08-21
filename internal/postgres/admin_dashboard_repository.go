package postgres

import (
	"context"
	"fmt"

	"github.com/Dyuzhovsergey/sup-rental/internal/dashboard"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AdminDashboardRepository читает агрегированные показатели панели администратора.
type AdminDashboardRepository struct {
	pool *pgxpool.Pool
}

// NewAdminDashboardRepository создаёт repository с обязательным пулом PostgreSQL.
func NewAdminDashboardRepository(pool *pgxpool.Pool) *AdminDashboardRepository {
	return &AdminDashboardRepository{pool: pool}
}

// Snapshot возвращает согласованные агрегаты одним SQL-запросом.
func (r *AdminDashboardRepository) Snapshot(ctx context.Context, query dashboard.Query) (dashboard.Snapshot, error) {
	if query.Now.IsZero() || query.DayStart.IsZero() || !query.DayEnd.After(query.DayStart) {
		return dashboard.Snapshot{}, fmt.Errorf("invalid admin dashboard query")
	}

	const statement = `
		WITH equipment_counts AS (
			SELECT count(*) AS total,
			       count(*) FILTER (WHERE status = 'available') AS available,
			       count(*) FILTER (WHERE status = 'maintenance') AS maintenance,
			       count(*) FILTER (WHERE status = 'retired') AS retired,
			       count(*) FILTER (WHERE status = 'issued') AS issued
			FROM equipment
		), rental_counts AS (
			SELECT count(*) FILTER (WHERE status = 'active') AS active,
			       count(*) FILTER (WHERE status = 'active' AND planned_end_at < $3) AS overdue,
			       count(*) FILTER (
			           WHERE status IN ('confirmed', 'active')
			             AND planned_start_at >= $1 AND planned_start_at < $2
			       ) AS starting_today,
			       count(*) FILTER (
			           WHERE status IN ('confirmed', 'active')
			             AND planned_end_at >= $1 AND planned_end_at < $2
			       ) AS ending_today
			FROM rentals
		)
		SELECT e.total, e.available, e.maintenance, e.retired, e.issued,
		       r.active, r.overdue, r.starting_today, r.ending_today
		FROM equipment_counts AS e CROSS JOIN rental_counts AS r
	`
	var snapshot dashboard.Snapshot
	err := r.pool.QueryRow(ctx, statement, query.DayStart, query.DayEnd, query.Now).Scan(
		&snapshot.EquipmentTotal, &snapshot.EquipmentAvailable,
		&snapshot.EquipmentMaintenance, &snapshot.EquipmentRetired,
		&snapshot.EquipmentIssued, &snapshot.RentalsActive,
		&snapshot.RentalsOverdue, &snapshot.RentalsStartingToday,
		&snapshot.RentalsEndingToday,
	)
	if err != nil {
		return dashboard.Snapshot{}, fmt.Errorf("query admin dashboard: %w", err)
	}
	return snapshot, nil
}
