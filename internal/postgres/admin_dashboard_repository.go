package postgres

import (
	"context"
	"fmt"

	"github.com/Dyuzhovsergey/sup-rental/internal/dashboard"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AdminDashboardRepository читает показатели и платёжные операции панели администратора.
type AdminDashboardRepository struct {
	pool *pgxpool.Pool
}

// NewAdminDashboardRepository создаёт repository с обязательным пулом PostgreSQL.
func NewAdminDashboardRepository(pool *pgxpool.Pool) *AdminDashboardRepository {
	return &AdminDashboardRepository{pool: pool}
}

// Snapshot возвращает согласованные агрегаты одним SQL-запросом.
func (r *AdminDashboardRepository) Snapshot(ctx context.Context, query dashboard.Query) (dashboard.Snapshot, error) {
	if query.Now.IsZero() || query.TodayStart.IsZero() || !query.TodayEnd.After(query.TodayStart) ||
		query.PaymentStart.IsZero() || !query.PaymentEnd.After(query.PaymentStart) {
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
			       count(*) FILTER (WHERE status = 'active' AND expected_return_at < $3) AS overdue,
			       count(*) FILTER (
			           WHERE status IN ('confirmed', 'active')
			             AND planned_start_at >= $1 AND planned_start_at < $2
			       ) AS starting_today,
			       count(*) FILTER (
			           WHERE status IN ('confirmed', 'active')
			             AND planned_end_at >= $1 AND planned_end_at < $2
			       ) AS ending_today
			FROM rentals
		), payment_counts AS (
			SELECT COALESCE(sum(amount_kopecks) FILTER (
			           WHERE kind = 'base' AND occurred_at >= $4 AND occurred_at < $5
			       ), 0)::bigint AS base,
			       COALESCE(sum(amount_kopecks) FILTER (
			           WHERE kind = 'overdue' AND occurred_at >= $4 AND occurred_at < $5
			       ), 0)::bigint AS overdue,
			       COALESCE(sum(amount_kopecks) FILTER (
			           WHERE kind = 'refund' AND occurred_at >= $4 AND occurred_at < $5
			       ), 0)::bigint AS refund
			FROM rental_payments
		)
		SELECT e.total, e.available, e.maintenance, e.retired, e.issued,
		       r.active, r.overdue, r.starting_today, r.ending_today,
		       p.base, p.overdue, p.refund, p.base + p.overdue - p.refund
		FROM equipment_counts AS e
		CROSS JOIN rental_counts AS r
		CROSS JOIN payment_counts AS p
	`
	var snapshot dashboard.Snapshot
	err := r.pool.QueryRow(
		ctx, statement, query.TodayStart, query.TodayEnd, query.Now,
		query.PaymentStart, query.PaymentEnd,
	).Scan(
		&snapshot.EquipmentTotal, &snapshot.EquipmentAvailable,
		&snapshot.EquipmentMaintenance, &snapshot.EquipmentRetired,
		&snapshot.EquipmentIssued, &snapshot.RentalsActive,
		&snapshot.RentalsOverdue, &snapshot.RentalsStartingToday,
		&snapshot.RentalsEndingToday, &snapshot.PaymentsBaseKopecks,
		&snapshot.PaymentsOverdueKopecks, &snapshot.PaymentsRefundKopecks,
		&snapshot.PaymentsNetKopecks,
	)
	if err != nil {
		return dashboard.Snapshot{}, fmt.Errorf("query admin dashboard: %w", err)
	}
	return snapshot, nil
}

// PaymentOperations возвращает страницу платежей за заданный финансовый период.
func (r *AdminDashboardRepository) PaymentOperations(
	ctx context.Context,
	query dashboard.PaymentOperationsQuery,
) (dashboard.PaymentOperationsPage, error) {
	if query.PeriodStart.IsZero() || !query.PeriodEnd.After(query.PeriodStart) ||
		query.Page <= 0 || (query.PageSize != 5 && query.PageSize != 10 && query.PageSize != 15) {
		return dashboard.PaymentOperationsPage{}, fmt.Errorf("invalid payment operations query")
	}

	const countStatement = `
		SELECT count(*)
		FROM rental_payments
		WHERE occurred_at >= $1 AND occurred_at < $2
	`
	var total int64
	if err := r.pool.QueryRow(ctx, countStatement, query.PeriodStart, query.PeriodEnd).Scan(&total); err != nil {
		return dashboard.PaymentOperationsPage{}, fmt.Errorf("count payment operations: %w", err)
	}

	const listStatement = `
		SELECT p.id, p.rental_id, c.full_name, p.kind, p.amount_kopecks,
		       p.occurred_at, u.login
		FROM rental_payments AS p
		JOIN rentals AS r ON r.id = p.rental_id
		JOIN clients AS c ON c.id = r.client_id
		JOIN users AS u ON u.id = p.actor_user_id
		WHERE p.occurred_at >= $1 AND p.occurred_at < $2
		ORDER BY p.occurred_at DESC, p.id DESC
		LIMIT $3 OFFSET $4
	`
	rows, err := r.pool.Query(
		ctx, listStatement, query.PeriodStart, query.PeriodEnd,
		query.PageSize, (query.Page-1)*query.PageSize,
	)
	if err != nil {
		return dashboard.PaymentOperationsPage{}, fmt.Errorf("query payment operations: %w", err)
	}
	defer rows.Close()

	operations := make([]dashboard.PaymentOperation, 0, query.PageSize)
	for rows.Next() {
		var operation dashboard.PaymentOperation
		if err := rows.Scan(
			&operation.ID, &operation.RentalID, &operation.ClientName,
			&operation.Kind, &operation.AmountKopecks, &operation.OccurredAt,
			&operation.ActorLogin,
		); err != nil {
			return dashboard.PaymentOperationsPage{}, fmt.Errorf("scan payment operation: %w", err)
		}
		operations = append(operations, operation)
	}
	if err := rows.Err(); err != nil {
		return dashboard.PaymentOperationsPage{}, fmt.Errorf("iterate payment operations: %w", err)
	}

	return dashboard.PaymentOperationsPage{
		Operations: operations,
		Total:      total,
		Page:       query.Page,
		PageSize:   query.PageSize,
	}, nil
}
