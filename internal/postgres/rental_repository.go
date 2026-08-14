package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Dyuzhovsergey/sup-rental/internal/client"
	"github.com/Dyuzhovsergey/sup-rental/internal/equipment"
	"github.com/Dyuzhovsergey/sup-rental/internal/rental"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	rentalClientForeignKeyConstraint = "rentals_client_id_fkey"
	rentalItemForeignKeyConstraint   = "rental_items_equipment_id_fkey"
	rentalItemUniqueEquipmentKey     = "rental_items_rental_equipment_key"
)

// RentalRepository хранит аренды и их упорядоченный состав в PostgreSQL.
type RentalRepository struct {
	pool *pgxpool.Pool
}

// NewRentalRepository создаёт PostgreSQL repository аренды.
func NewRentalRepository(pool *pgxpool.Pool) *RentalRepository {
	return &RentalRepository{pool: pool}
}

// Create атомарно сохраняет новый черновик и его текущий состав.
func (r *RentalRepository) Create(ctx context.Context, value rental.Rental) (rental.Rental, error) {
	if err := validateNewRental(value); err != nil {
		return rental.Rental{}, err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return rental.Rental{}, fmt.Errorf("begin create rental transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const insertRentalQuery = `
		INSERT INTO rentals (client_id, planned_start_at, planned_end_at, status)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`
	var id int64
	if err := tx.QueryRow(
		ctx,
		insertRentalQuery,
		value.ClientID,
		value.Interval.Start(),
		value.Interval.End(),
		value.Status,
	).Scan(&id); err != nil {
		if mapped := mapRentalReferenceError(err); mapped != nil {
			return rental.Rental{}, mapped
		}
		return rental.Rental{}, fmt.Errorf("insert rental: %w", err)
	}

	if err := insertRentalItems(ctx, tx, id, value.Items()); err != nil {
		return rental.Rental{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return rental.Rental{}, fmt.Errorf("commit create rental transaction: %w", err)
	}

	created, err := rental.Restore(
		id,
		value.ClientID,
		value.Interval,
		value.Status,
		value.Items(),
	)
	if err != nil {
		return rental.Rental{}, fmt.Errorf("restore created rental: %w", err)
	}
	return created, nil
}

// Get возвращает аренду с составом в сохранённом порядке.
func (r *RentalRepository) Get(ctx context.Context, id int64) (rental.Rental, error) {
	if id <= 0 {
		return rental.Rental{}, rental.ErrInvalidRentalID
	}

	const query = `
		SELECT r.id, r.client_id, r.planned_start_at, r.planned_end_at, r.status,
		       ri.equipment_id,
		       COALESCE(ri.inventory_number, ''),
		       COALESCE(ri.kind, ''),
		       COALESCE(ri.model_code, ''),
		       COALESCE(ri.hourly_rate_kopecks, 0)
		FROM rentals AS r
		LEFT JOIN rental_items AS ri ON ri.rental_id = r.id
		WHERE r.id = $1
		ORDER BY ri.position
	`
	rows, err := r.pool.Query(ctx, query, id)
	if err != nil {
		return rental.Rental{}, fmt.Errorf("query rental: %w", err)
	}
	defer rows.Close()

	var (
		found    bool
		clientID int64
		start    time.Time
		end      time.Time
		status   rental.Status
		items    []rental.Item
	)
	for rows.Next() {
		var (
			rowID             int64
			equipmentID       *int64
			inventoryNumber   string
			kind              equipment.Kind
			modelCode         string
			hourlyRateKopecks int64
		)
		if err := rows.Scan(
			&rowID,
			&clientID,
			&start,
			&end,
			&status,
			&equipmentID,
			&inventoryNumber,
			&kind,
			&modelCode,
			&hourlyRateKopecks,
		); err != nil {
			return rental.Rental{}, fmt.Errorf("scan rental: %w", err)
		}
		found = true
		if equipmentID != nil {
			items = append(items, rental.Item{
				EquipmentID:       *equipmentID,
				InventoryNumber:   inventoryNumber,
				Kind:              kind,
				ModelCode:         modelCode,
				HourlyRateKopecks: hourlyRateKopecks,
			})
		}
	}
	if err := rows.Err(); err != nil {
		return rental.Rental{}, fmt.Errorf("iterate rental: %w", err)
	}
	if !found {
		return rental.Rental{}, rental.ErrRentalNotFound
	}

	interval, err := rental.NewInterval(start, end)
	if err != nil {
		return rental.Rental{}, fmt.Errorf("restore rental interval: %w", err)
	}
	restored, err := rental.Restore(id, clientID, interval, status, items)
	if err != nil {
		return rental.Rental{}, fmt.Errorf("restore rental: %w", err)
	}
	return restored, nil
}

// UpdateDraft атомарно заменяет клиента, интервал и полный состав черновика.
func (r *RentalRepository) UpdateDraft(
	ctx context.Context,
	value rental.Rental,
) (rental.Rental, error) {
	if err := validateDraftRental(value); err != nil {
		return rental.Rental{}, err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return rental.Rental{}, fmt.Errorf("begin update rental draft transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const lockQuery = `
		SELECT status
		FROM rentals
		WHERE id = $1
		FOR UPDATE
	`
	var storedStatus rental.Status
	if err := tx.QueryRow(ctx, lockQuery, value.ID).Scan(&storedStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return rental.Rental{}, rental.ErrRentalNotFound
		}
		return rental.Rental{}, fmt.Errorf("lock rental draft: %w", err)
	}
	if storedStatus != rental.StatusDraft {
		return rental.Rental{}, rental.ErrRentalNotEditable
	}

	const updateQuery = `
		UPDATE rentals
		SET client_id = $1, planned_start_at = $2, planned_end_at = $3
		WHERE id = $4
	`
	if _, err := tx.Exec(
		ctx,
		updateQuery,
		value.ClientID,
		value.Interval.Start(),
		value.Interval.End(),
		value.ID,
	); err != nil {
		if mapped := mapRentalReferenceError(err); mapped != nil {
			return rental.Rental{}, mapped
		}
		return rental.Rental{}, fmt.Errorf("update rental draft: %w", err)
	}

	if _, err := tx.Exec(ctx, "DELETE FROM rental_items WHERE rental_id = $1", value.ID); err != nil {
		return rental.Rental{}, fmt.Errorf("delete previous rental draft items: %w", err)
	}
	if err := insertRentalItems(ctx, tx, value.ID, value.Items()); err != nil {
		return rental.Rental{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return rental.Rental{}, fmt.Errorf("commit update rental draft transaction: %w", err)
	}

	updated, err := rental.Restore(
		value.ID,
		value.ClientID,
		value.Interval,
		value.Status,
		value.Items(),
	)
	if err != nil {
		return rental.Rental{}, fmt.Errorf("restore updated rental draft: %w", err)
	}
	return updated, nil
}

func validateNewRental(value rental.Rental) error {
	if value.ID != 0 {
		return rental.ErrRentalAlreadyPersisted
	}
	if value.Status != rental.StatusDraft {
		return rental.ErrRentalNotEditable
	}

	draft, err := rental.New(value.ClientID, value.Interval)
	if err != nil {
		return err
	}
	for _, item := range value.Items() {
		if err := draft.AddItem(item); err != nil {
			return err
		}
	}
	return nil
}

func validateDraftRental(value rental.Rental) error {
	if value.ID <= 0 {
		return rental.ErrInvalidRentalID
	}
	if value.Status != rental.StatusDraft {
		return rental.ErrRentalNotEditable
	}
	_, err := rental.Restore(
		value.ID,
		value.ClientID,
		value.Interval,
		value.Status,
		value.Items(),
	)
	return err
}

func insertRentalItems(
	ctx context.Context,
	tx pgx.Tx,
	rentalID int64,
	items []rental.Item,
) error {
	const query = `
		INSERT INTO rental_items (
			rental_id, equipment_id, position, inventory_number,
			kind, model_code, hourly_rate_kopecks
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	for index, item := range items {
		if _, err := tx.Exec(
			ctx,
			query,
			rentalID,
			item.EquipmentID,
			index+1,
			item.InventoryNumber,
			item.Kind,
			item.ModelCode,
			item.HourlyRateKopecks,
		); err != nil {
			if mapped := mapRentalReferenceError(err); mapped != nil {
				return mapped
			}
			if mapped := mapRentalItemConstraintError(err); mapped != nil {
				return mapped
			}
			return fmt.Errorf("insert rental item at position %d: %w", index+1, err)
		}
	}
	return nil
}

func mapRentalReferenceError(err error) error {
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != foreignKeyViolationCode {
		return nil
	}
	switch postgresError.ConstraintName {
	case rentalClientForeignKeyConstraint:
		return client.ErrClientNotFound
	case rentalItemForeignKeyConstraint:
		return equipment.ErrEquipmentNotFound
	default:
		return nil
	}
}

func mapRentalItemConstraintError(err error) error {
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		return nil
	}
	if postgresError.ConstraintName == rentalItemUniqueEquipmentKey {
		return rental.ErrEquipmentAlreadyAdded
	}
	return nil
}
