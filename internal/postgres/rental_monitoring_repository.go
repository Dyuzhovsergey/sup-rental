package postgres

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/Dyuzhovsergey/sup-rental/internal/rental"
	"github.com/jackc/pgx/v5"
)

// Monitoring возвращает согласованный read-only снимок текущей работы проката.
// Счётчики и обе выборки читаются в одной repeatable-read транзакции.
func (r *RentalRepository) Monitoring(
	ctx context.Context,
	query rental.MonitoringQuery,
) (rental.MonitoringData, error) {
	if query.Now.IsZero() || query.DayStart.IsZero() || !query.DayEnd.After(query.DayStart) || query.Limit <= 0 {
		return rental.MonitoringData{}, fmt.Errorf("invalid rental monitoring query")
	}

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return rental.MonitoringData{}, fmt.Errorf("begin rental monitoring transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const countQuery = `
		SELECT
			count(*) FILTER (
				WHERE status IN ('confirmed', 'active')
				  AND planned_start_at < $2
				  AND $1 < planned_end_at
			),
			count(*) FILTER (
				WHERE status = 'confirmed'
				  AND planned_start_at < $2
				  AND $1 < planned_end_at
			),
			count(*) FILTER (WHERE status = 'active'),
			count(*) FILTER (
				WHERE status = 'active'
				  AND planned_end_at < $3
			)
		FROM rentals
	`
	var data rental.MonitoringData
	if err := tx.QueryRow(ctx, countQuery, query.DayStart, query.DayEnd, query.Now).Scan(
		&data.TodayTotal, &data.ConfirmedTotal, &data.ActiveTotal, &data.OverdueTotal,
	); err != nil {
		return rental.MonitoringData{}, fmt.Errorf("count rental monitoring: %w", err)
	}

	const confirmedQuery = `
		SELECT r.id, r.client_id, c.full_name,
		       r.planned_start_at, r.planned_end_at, r.status,
		       count(ri.equipment_id),
		       COALESCE(sum(ri.hourly_rate_kopecks), 0)
		FROM rentals AS r
		JOIN clients AS c ON c.id = r.client_id
		LEFT JOIN rental_items AS ri ON ri.rental_id = r.id
		WHERE r.status = 'confirmed'
		  AND r.planned_start_at < $2
		  AND $1 < r.planned_end_at
		GROUP BY r.id, c.full_name
		ORDER BY r.planned_start_at, r.id
		LIMIT $3
	`
	data.Confirmed, err = queryMonitoringSummaries(
		ctx, tx, confirmedQuery, query.DayStart, query.DayEnd, query.Limit,
	)
	if err != nil {
		return rental.MonitoringData{}, fmt.Errorf("query confirmed rental monitoring: %w", err)
	}

	const activeQuery = `
		SELECT r.id, r.client_id, c.full_name,
		       r.planned_start_at, r.planned_end_at, r.status,
		       count(ri.equipment_id),
		       COALESCE(sum(ri.hourly_rate_kopecks), 0)
		FROM rentals AS r
		JOIN clients AS c ON c.id = r.client_id
		LEFT JOIN rental_items AS ri ON ri.rental_id = r.id
		WHERE r.status = 'active'
		GROUP BY r.id, c.full_name
		ORDER BY (r.planned_end_at < $1) DESC, r.planned_end_at, r.id
		LIMIT $2
	`
	data.Active, err = queryMonitoringSummaries(ctx, tx, activeQuery, query.Now, query.Limit)
	if err != nil {
		return rental.MonitoringData{}, fmt.Errorf("query active rental monitoring: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return rental.MonitoringData{}, fmt.Errorf("commit rental monitoring transaction: %w", err)
	}
	return data, nil
}

func queryMonitoringSummaries(
	ctx context.Context,
	tx pgx.Tx,
	query string,
	args ...any,
) ([]rental.Summary, error) {
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	summaries := make([]rental.Summary, 0)
	for rows.Next() {
		var (
			summary       rental.Summary
			start         time.Time
			end           time.Time
			hourlyRateSum int64
		)
		if err := rows.Scan(
			&summary.ID, &summary.ClientID, &summary.ClientName,
			&start, &end, &summary.Status, &summary.ItemCount, &hourlyRateSum,
		); err != nil {
			return nil, err
		}
		if !summary.Status.Valid() {
			return nil, rental.ErrInvalidStatus
		}
		interval, err := rental.NewInterval(start, end)
		if err != nil {
			return nil, err
		}
		summary.Interval = interval
		halfHourlyRate := hourlyRateSum / 2
		if halfHourlyRate > 0 && int64(interval.SlotCount()) > math.MaxInt64/halfHourlyRate {
			return nil, rental.ErrPriceOverflow
		}
		summary.PlannedTotalKopecks = halfHourlyRate * int64(interval.SlotCount())
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return summaries, nil
}
